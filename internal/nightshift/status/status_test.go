package status

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
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
	for _, sub := range []string{"queue", "tokens_min", "tokens_run"} {
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
	if doc.CostRun != 8.4 || !doc.Running || doc.RunID != "12345" {
		t.Errorf("fixture values lost in round trip: %+v", doc)
	}
}

// tokenRun tallies only events at/after `since` (this run's start), deduped by
// (id, requestId) so a replayed turn never double-counts.
func TestTokenRunScope(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	wt := repo + "-w0"
	e := NewEmitter(repo)
	e.ClaudeProjects = t.TempDir()
	dir := filepath.Join(e.ClaudeProjects, workerSlug(wt))
	os.MkdirAll(dir, 0o755)
	ev := func(id, ts string, in, out int64) string {
		return `{"requestId":"` + id + `","message":{"id":"` + id +
			`","model":"claude-x","usage":{"input_tokens":` +
			itoa(in) + `,"output_tokens":` + itoa(out) + `}},"timestamp":"` + ts + `"}`
	}
	// prior-run event (before the boundary), a current-run event, and a DUPLICATE
	// of the current one (same id+requestId — a replayed turn) that must not double-count.
	lines := ev("a", "2026-07-02T10:00:00Z", 100, 40) + "\n" +
		ev("b", "2026-07-02T12:05:00Z", 200, 80) + "\n" +
		ev("b", "2026-07-02T12:05:00Z", 200, 80) + "\n"
	os.WriteFile(filepath.Join(dir, "sess.jsonl"), []byte(lines), 0o644)

	since := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	run := e.tokenRun(wt, since)
	if run.in != 200 || run.out != 80 {
		t.Errorf("run = in %d out %d, want in 200 out 80 (only the post-boundary event)", run.in, run.out)
	}
	// zero `since` (no run boundary known) → count every event (a+b, replay deduped).
	if all := e.tokenRun(wt, time.Time{}); all.in != 300 || all.out != 120 {
		t.Errorf("zero since = in %d out %d, want in 300 out 120", all.in, all.out)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

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

// The emit loop reuses one Emitter across ticks, so the queue counts must be
// re-read each Emit — a cached row set would freeze open/done/review at the
// run-start snapshot while workers merge tasks.
func TestEmitRereadsQueueEachTick(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	os.MkdirAll(filepath.Join(repo, ".nightshift"), 0o755)
	e := NewEmitter(repo)
	e.ClaudeProjects = t.TempDir()
	listing := "queue: p (all)\n  [ 10] open  0001-alpha  Alpha\n  [  9] open  0002-beta  Beta\n"
	e.TodoOutput = func(args ...string) string {
		if len(args) > 0 && args[0] == "list" {
			return listing
		}
		return ""
	}
	fixed := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	old := Now
	Now = func() time.Time { return fixed }
	defer func() { Now = old }()

	read := func() QueueCounts {
		if _, err := e.Emit(); err != nil {
			t.Fatal(err)
		}
		var doc Doc
		b, _ := os.ReadFile(filepath.Join(repo, ".nightshift", "status.json"))
		json.Unmarshal(b, &doc)
		return doc.Queue
	}
	if got := read(); got != (QueueCounts{Open: 2}) {
		t.Fatalf("tick 1 queue = %+v, want {open:2}", got)
	}
	// A worker merged 0001 → now done. A frozen cache would still report open:2.
	listing = "queue: p (all)\n  [ 10] done  0001-alpha  Alpha\n  [  9] open  0002-beta  Beta\n"
	if got := read(); got != (QueueCounts{Open: 1, Done: 1}) {
		t.Fatalf("tick 2 queue = %+v, want {open:1 done:1} (a stale cache shows open:2)", got)
	}
}

func TestEmitScopesParkedTasksToOnlySet(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	os.MkdirAll(filepath.Join(repo, ".nightshift"), 0o755)
	os.WriteFile(filepath.Join(repo, ".nightshift", "only.txt"), []byte("0001\n"), 0o644)

	e := NewEmitter(repo)
	e.ClaudeProjects = t.TempDir()
	e.TodoOutput = func(args ...string) string {
		if len(args) > 0 && args[0] == "list" {
			return "queue: p (all)\n" +
				"  [ 10] held  0001-in-set   In set\n" +
				"  [  9] held  0002-out-set  Out of set\n"
		}
		if len(args) > 1 && args[0] == "show" {
			return "---\nid: " + args[1] + "\nstatus: held\nreason: needs a human\n---\n"
		}
		return ""
	}

	if _, err := e.Emit(); err != nil {
		t.Fatal(err)
	}
	var doc Doc
	b, _ := os.ReadFile(filepath.Join(repo, ".nightshift", "status.json"))
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Parked) != 1 || doc.Parked[0].ID != "0001-in-set" {
		t.Fatalf("parked tasks must be scoped to only.txt, got %+v", doc.Parked)
	}
}

func TestProcessWorkerUsesTurnPidAndTurnLogResponses(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	w0 := repo + "-w0"
	pid := strconv.Itoa(os.Getpid())
	os.MkdirAll(filepath.Join(repo, ".nightshift"), 0o755)
	os.MkdirAll(filepath.Join(w0, ".nightshift"), 0o755)
	os.WriteFile(filepath.Join(repo, ".nightshift", "orchestrator.pid"), []byte(pid+"\n"), 0o644)
	os.WriteFile(filepath.Join(w0, ".nightshift", "run"), []byte(pid+"\n"), 0o644)
	os.WriteFile(filepath.Join(w0, ".nightshift", "turn.pid"), []byte(pid+"\n"), 0o644)
	os.WriteFile(filepath.Join(w0, ".nightshift", "turn.log"), []byte("codex\nstill running tools\n"), 0o644)

	e := NewEmitter(repo)
	e.ClaudeProjects = t.TempDir()
	e.TodoOutput = func(...string) string { return "" }
	if _, err := e.Emit(); err != nil {
		t.Fatal(err)
	}
	var doc Doc
	b, _ := os.ReadFile(filepath.Join(repo, ".nightshift", "status.json"))
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Workers) != 1 {
		t.Fatalf("workers = %+v, want one process-backed worker", doc.Workers)
	}
	w := doc.Workers[0]
	if w.State != "working" {
		t.Fatalf("process worker state = %q want working", w.State)
	}
	if len(w.Responses) != 1 || !strings.Contains(w.Responses[0].Text, "still running tools") {
		t.Fatalf("process worker responses should fall back to turn.log, got %+v", w.Responses)
	}
}

func TestEmitCountsCodexSessionUsage(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	wt := repo + "-w0"
	pid := strconv.Itoa(os.Getpid())
	os.MkdirAll(filepath.Join(repo, ".nightshift"), 0o755)
	os.MkdirAll(filepath.Join(wt, ".nightshift"), 0o755)
	os.WriteFile(filepath.Join(repo, ".nightshift", "orchestrator.pid"), []byte(pid+"\n"), 0o644)
	os.WriteFile(filepath.Join(repo, ".nightshift", "status.json"),
		[]byte(`{"run_id":"`+pid+`","started":"2026-07-09T04:00:00Z","history":[]}`), 0o644)
	os.WriteFile(filepath.Join(wt, ".nightshift", "run"), []byte(pid+"\n"), 0o644)
	os.WriteFile(filepath.Join(wt, ".nightshift", "turn.pid"), []byte(pid+"\n"), 0o644)
	os.WriteFile(filepath.Join(wt, ".nightshift", "turn.log"), []byte("codex running\n"), 0o644)

	codexRoot := t.TempDir()
	sessionDir := filepath.Join(codexRoot, "2026", "07", "09")
	os.MkdirAll(sessionDir, 0o755)
	session := `{"timestamp":"2026-07-09T04:00:01Z","type":"session_meta","payload":{"cwd":"` + wt + `"}}
{"timestamp":"2026-07-09T04:00:02Z","type":"turn_context","payload":{"cwd":"` + wt + `","model":"gpt-5.5"}}
{"timestamp":"2026-07-09T04:00:03Z","type":"event_msg","payload":{"type":"user_message","message":"work"}}
{"timestamp":"2026-07-09T04:00:20Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":1000,"cached_input_tokens":400,"output_tokens":50}}}}
`
	os.WriteFile(filepath.Join(sessionDir, "rollout.jsonl"), []byte(session), 0o644)

	e := NewEmitter(repo)
	e.ClaudeProjects = t.TempDir()
	e.CodexSessions = codexRoot
	e.TodoOutput = func(...string) string { return "" }
	fixed := time.Date(2026, 7, 9, 4, 0, 30, 0, time.UTC)
	old := Now
	Now = func() time.Time { return fixed }
	defer func() { Now = old }()

	if _, err := e.Emit(); err != nil {
		t.Fatal(err)
	}
	var doc Doc
	b, _ := os.ReadFile(filepath.Join(repo, ".nightshift", "status.json"))
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.TokensRun != (TokenPair{In: 600, Out: 50}) {
		t.Fatalf("Codex run tokens = %+v, want non-cached in 600 out 50", doc.TokensRun)
	}
	if doc.TokensMin != (TokenPair{In: 600, Out: 50}) {
		t.Fatalf("Codex rate tokens = %+v, want in 600 out 50", doc.TokensMin)
	}
	if doc.CostRun <= 0 {
		t.Fatalf("Codex cost should be priced, got %.4f", doc.CostRun)
	}
	if len(doc.Workers) != 1 || doc.Workers[0].TIn != 600 || doc.Workers[0].TOut != 50 {
		t.Fatalf("worker Codex tokens missing: %+v", doc.Workers)
	}
}

// Worker cards are scoped to the live run's stamp: a leftover worktree from a
// prior run (stale .nightshift/run) is hidden, and skipping it does not stop
// enumeration of the matching worktree that follows.
func TestWorkerCardsScopedToRunStamp(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	os.MkdirAll(filepath.Join(repo, ".nightshift"), 0o755)
	// Stopped run identified as RUN2 (RunIdentity keeps the id while stopped).
	os.WriteFile(filepath.Join(repo, ".nightshift", "status.json"),
		[]byte(`{"run_id":"RUN2","started":"2026-07-02T11:00:00Z","stopped_at":"2026-07-02T11:30:00Z"}`), 0o644)
	mkWorker := func(suffix, stamp, log string) {
		wt := repo + suffix
		os.MkdirAll(filepath.Join(wt, ".nightshift"), 0o755)
		os.WriteFile(filepath.Join(wt, ".nightshift", "run"), []byte(stamp), 0o644)
		os.WriteFile(filepath.Join(wt, ".nightshift", "turn.log"), []byte(log), 0o644)
	}
	mkWorker("-w0", "RUN1", "stale prior-run output") // leftover from an old run
	mkWorker("-w1", "RUN2", "current output")         // this run's worker

	e := NewEmitter(repo)
	e.ClaudeProjects = t.TempDir()
	e.TodoOutput = func(...string) string { return "" }
	fixed := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	old := Now
	Now = func() time.Time { return fixed }
	defer func() { Now = old }()

	if _, err := e.Emit(); err != nil {
		t.Fatal(err)
	}
	var doc Doc
	b, _ := os.ReadFile(filepath.Join(repo, ".nightshift", "status.json"))
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Workers) != 1 {
		t.Fatalf("want exactly 1 card (only the RUN2 worktree), got %d: %+v", len(doc.Workers), doc.Workers)
	}
	if doc.Workers[0].I != 1 || doc.Workers[0].Pane != "current output" {
		t.Errorf("stale w0 must be skipped and w1 shown; got %+v", doc.Workers[0])
	}
}

// recentResponses drops transcripts older than the run start so a reused
// worktree's previous-run feed doesn't flash on drag-in.
func TestRecentResponsesDropsPriorRun(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	wt := repo + "-w0"
	e := NewEmitter(repo)
	e.ClaudeProjects = t.TempDir()
	dir := filepath.Join(e.ClaudeProjects, workerSlug(wt))
	os.MkdirAll(dir, 0o755)
	line := func(txt, ts string) []byte {
		return []byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"` + txt + `"}]},"timestamp":"` + ts + `"}` + "\n")
	}
	oldF := filepath.Join(dir, "sessA-old.jsonl")
	newF := filepath.Join(dir, "sessB-new.jsonl")
	os.WriteFile(oldF, line("prior run message", "2026-07-02T10:00:00Z"), 0o644)
	os.WriteFile(newF, line("current run message", "2026-07-02T12:05:00Z"), 0o644)
	started := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	os.Chtimes(oldF, started.Add(-time.Hour), started.Add(-time.Hour))
	os.Chtimes(newF, started.Add(time.Minute), started.Add(time.Minute))

	got := e.recentResponses(wt, 40, 8, started)
	if len(got) != 1 || got[0].Text != "current run message" {
		t.Fatalf("want only the post-start message, got %+v", got)
	}
}
