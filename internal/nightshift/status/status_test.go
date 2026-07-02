package status

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"
)

// RunIdentity table — ported verbatim from the retired
// scripts/test-nightshift-runid.py.
func TestRunIdentity(t *testing.T) {
	t.Parallel()
	const now = "2026-06-27T12:00:00Z"
	const later = "2026-06-27T13:30:00Z"
	prior := map[string]any{"run_id": "4242", "started": now, "history": []any{map[string]any{"t": "12:00"}}}

	for _, c := range []struct {
		name              string
		prior             map[string]any
		running           bool
		pid, at           string
		wantID, wantStart string
		wantReset         bool
	}{
		{"fresh start mints pid identity", map[string]any{}, true, "4242", now, "4242", now, true},
		{"continuing run keeps identity", prior, true, "4242", later, "4242", now, false},
		{"restart adopts new pid + resets", prior, true, "9999", later, "9999", later, true},
		{"stopped keeps last identity", prior, false, "", later, "4242", now, false},
		{"stopped with no prior is empty", map[string]any{}, false, "", later, "", "", false},
	} {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			id, started, reset := RunIdentity(c.prior, c.running, c.pid, c.at)
			if id != c.wantID || started != c.wantStart || reset != c.wantReset {
				t.Errorf("got (%q,%q,%v) want (%q,%q,%v)", id, started, reset, c.wantID, c.wantStart, c.wantReset)
			}
		})
	}
}

// The marshaled Doc must carry exactly the key set of the frozen fixture —
// the dashboard's Nightshift tab and queue.py's stale-run pruning read it.
func TestDocKeySetMatchesFixture(t *testing.T) {
	t.Parallel()
	fixture, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "dashboard-fixture", "nightshift-status.json"))
	if err != nil {
		t.Fatal(err)
	}
	var want map[string]any
	if err := json.Unmarshal(fixture, &want); err != nil {
		t.Fatal(err)
	}
	var doc Doc
	if err := json.Unmarshal(fixture, &doc); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(keySet(want), keySet(got)) {
		t.Errorf("top-level keys differ:\n want %v\n got  %v", keySet(want), keySet(got))
	}
	for _, sub := range []string{"queue", "tokens_min", "tokens_total"} {
		if !reflect.DeepEqual(keySet(want[sub].(map[string]any)), keySet(got[sub].(map[string]any))) {
			t.Errorf("%s keys differ", sub)
		}
	}
	ww := want["workers"].([]any)[0].(map[string]any)
	gw := got["workers"].([]any)[0].(map[string]any)
	if !reflect.DeepEqual(keySet(ww), keySet(gw)) {
		t.Errorf("worker keys differ:\n want %v\n got  %v", keySet(ww), keySet(gw))
	}
	// round-trip values survive (spot checks on the load-bearing ones)
	if doc.CostTotal != 45.3 || !doc.Running || doc.RunID != "12345" {
		t.Errorf("fixture values lost in round trip: %+v", doc)
	}
}

func keySet(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// A minimal end-to-end emit on a synthetic repo: no tmux, no orchestrator,
// one fake worker worktree with a turn log — the headless reconstruction
// path, the stopped stamp, and the atomic write.
func TestEmitHeadlessReconstruction(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	w0 := repo + "-w0"
	os.MkdirAll(filepath.Join(repo, ".nightshift"), 0o755)
	os.MkdirAll(filepath.Join(w0, ".nightshift"), 0o755)
	os.WriteFile(filepath.Join(w0, ".nightshift", "turn.log"), []byte("line1\nfinal output line\n"), 0o644)
	os.WriteFile(filepath.Join(repo, ".nightshift", "orchestrator.log"), []byte("02:00 boot\n02:25 tick\n"), 0o644)

	e := NewEmitter(repo)
	e.ClaudeProjects = t.TempDir() // no transcripts -> zero token counts
	e.TodoOutput = func(args ...string) string {
		if len(args) > 0 && args[0] == "list" {
			return "queue: p (all)\n  [ 10] open    0001-alpha  Alpha\n  [  5] done    0002-beta  Beta\n  [  1] held    0003-gamma  Gamma\n"
		}
		if len(args) > 0 && args[0] == "show" {
			return "---\nid: 0003-gamma\nstatus: held\nreason: needs a human\n---\n\n# Gamma\n"
		}
		return ""
	}
	fixed := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	old := Now
	Now = func() time.Time { return fixed }
	defer func() { Now = old }()

	retire, err := e.Emit()
	if err != nil {
		t.Fatal(err)
	}
	if retire {
		t.Error("first stopped tick must not retire (10-minute grace)")
	}
	b, err := os.ReadFile(filepath.Join(repo, ".nightshift", "status.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc Doc
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Running {
		t.Error("no orchestrator -> not running")
	}
	if doc.StoppedAt != "2026-07-02T12:00:00Z" {
		t.Errorf("stopped_at = %q", doc.StoppedAt)
	}
	if doc.Queue != (QueueCounts{Open: 1, Done: 1, Review: 0}) {
		t.Errorf("queue counts = %+v", doc.Queue)
	}
	if len(doc.Workers) != 1 || doc.Workers[0].State != "idle" || doc.Workers[0].Pane != "line1\nfinal output line" {
		t.Errorf("worker reconstruction = %+v", doc.Workers)
	}
	if len(doc.Parked) != 1 || doc.Parked[0].Reason != "needs a human" {
		t.Errorf("parked = %+v", doc.Parked)
	}
	if len(doc.Log) != 2 {
		t.Errorf("log tail = %v", doc.Log)
	}

	// second tick 11 minutes later -> retire (stopped_at carried from prior)
	Now = func() time.Time { return fixed.Add(11 * time.Minute) }
	retire, err = e.Emit()
	if err != nil {
		t.Fatal(err)
	}
	if !retire {
		t.Error("stopped >10min must retire the emit loop")
	}
	// history accumulated across ticks without reset
	os.ReadFile(filepath.Join(repo, ".nightshift", "status.json"))
	var doc2 Doc
	b2, _ := os.ReadFile(filepath.Join(repo, ".nightshift", "status.json"))
	json.Unmarshal(b2, &doc2)
	if len(doc2.History) != 2 {
		t.Errorf("history should carry across stopped ticks: %+v", doc2.History)
	}
	if doc2.StoppedAt != "2026-07-02T12:00:00Z" {
		t.Errorf("stopped_at must keep the FIRST stopped stamp: %q", doc2.StoppedAt)
	}
}

// A --only run's card counts ONLY its launched subset; without the fence file
// the same queue counts whole. Matches by 4-digit number across slug/bare forms.
func TestCountScopedToOnlySet(t *testing.T) {
	repo := t.TempDir()
	os.MkdirAll(filepath.Join(repo, ".nightshift"), 0o755)
	list := func(*Emitter) *Emitter {
		e := NewEmitter(repo)
		e.TodoOutput = func(args ...string) string {
			return "queue: p (all)\n" +
				"  [ 10] open    0001-alpha  Alpha\n" +
				"  [  9] open    0002-beta   Beta\n" +
				"  [  5] done    0003-gamma  Gamma\n" +
				"  [  1] review  0004-delta  Delta\n"
		}
		return e
	}

	// Full-drain (no only.txt): a nil set, so the whole queue counts.
	full := list(nil)
	if full.onlySet() != nil {
		t.Error("no only.txt must yield a nil (unscoped) set")
	}
	if got := [3]int{full.count("open", nil), full.count("done", nil), full.count("review", nil)}; got != [3]int{2, 1, 1} {
		t.Fatalf("unscoped counts = %v, want [2 1 1]", got)
	}

	// Fixed-set: only 0001 (bare number) and 0003-gamma (full slug) are counted.
	os.WriteFile(filepath.Join(repo, ".nightshift", "only.txt"), []byte("0001,0003-gamma\n"), 0o644)
	scoped := list(nil)
	only := scoped.onlySet()
	if got := [3]int{scoped.count("open", only), scoped.count("done", only), scoped.count("review", only)}; got != [3]int{1, 1, 0} {
		t.Fatalf("scoped counts = %v, want [1 1 0] (0002 open + 0004 review out of set)", got)
	}
}
