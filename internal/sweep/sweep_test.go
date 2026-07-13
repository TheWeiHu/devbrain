package sweep

import (
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fixture builds isolated HOME/CODEX_HOME/DEVBRAIN_DATA roots plus a cursor
// dir, and returns the codex sessions dir for planting rollouts.
func fixture(t *testing.T) (dataDir, codexSessions string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dataDir = filepath.Join(home, "devbrain-data")
	os.MkdirAll(dataDir, 0o755)
	t.Setenv("DEVBRAIN_DATA", dataDir)
	codex := filepath.Join(home, ".codex")
	t.Setenv("CODEX_HOME", codex)
	t.Setenv("DEVBRAIN_SWEEP_CURSOR_DIR", filepath.Join(home, ".config", "devbrain"))
	codexSessions = filepath.Join(codex, "sessions", "2026", "07", "14")
	os.MkdirAll(codexSessions, 0o755)
	os.MkdirAll(filepath.Join(home, ".claude", "projects"), 0o755)
	return dataDir, codexSessions
}

func plantRollout(t *testing.T, dir, name, cwd string) string {
	t.Helper()
	sid := fmt.Sprintf("019f0000-1111-2222-3333-%012x", crc32.ChecksumIEEE([]byte(name)))
	body := `{"timestamp":"2026-07-14T10:00:00Z","type":"session_meta","payload":{"id":"` + sid + `","cwd":"` + cwd + `"}}
{"type":"turn_context","payload":{"cwd":"` + cwd + `","model":"gpt-5.5"}}
{"type":"event_msg","timestamp":"2026-07-14T10:00:01Z","payload":{"type":"user_message","message":"do the thing"}}
{"type":"event_msg","timestamp":"2026-07-14T10:00:09Z","payload":{"type":"agent_message","message":"Did the thing."}}
{"type":"event_msg","timestamp":"2026-07-14T10:00:10Z","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":100,"cached_input_tokens":0,"output_tokens":10}}}}
`
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// gitWorktree makes cwd routable to a real project key.
func gitWorktree(t *testing.T, home string) string {
	t.Helper()
	wt := filepath.Join(home, "src", "demo")
	os.MkdirAll(wt, 0o755)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", wt}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("remote", "add", "origin", "https://github.com/owner/demo.git")
	return wt
}

func TestSweepHarvestsAndAdvancesCursor(t *testing.T) {
	dataDir, sessions := fixture(t)
	wt := gitWorktree(t, os.Getenv("HOME"))
	plantRollout(t, sessions, "rollout-a.jsonl", wt)

	if rc := Run(nil, io.Discard, io.Discard); rc != 0 {
		t.Fatalf("sweep rc = %d", rc)
	}
	logs, _ := filepath.Glob(filepath.Join(dataDir, "projects", "owner__demo", "log", "*", "*.md"))
	if len(logs) != 1 {
		t.Fatalf("swept logs = %v, want the rollout's session log", logs)
	}
	b, _ := os.ReadFile(logs[0])
	if !strings.Contains(string(b), "do the thing") {
		t.Fatalf("log missing prompt:\n%s", b)
	}
	tok, _ := os.ReadFile(filepath.Join(dataDir, "projects", "owner__demo", "tokens.jsonl"))
	if !strings.Contains(string(tok), `"model": "gpt-5.5"`) {
		t.Fatalf("tokens missing:\n%s", tok)
	}
	if readCursor() == 0 {
		t.Fatal("cursor not advanced")
	}
}

func TestSweepIdleTickIsNoOp(t *testing.T) {
	dataDir, sessions := fixture(t)
	wt := gitWorktree(t, os.Getenv("HOME"))
	p := plantRollout(t, sessions, "rollout-a.jsonl", wt)
	// Backdate the rollout past the cursor's deliberate 1-second overlap
	// window so the second tick is genuinely idle.
	past := time.Now().Add(-time.Minute)
	os.Chtimes(p, past, past)
	if rc := Run(nil, io.Discard, io.Discard); rc != 0 {
		t.Fatal("first sweep failed")
	}
	first := readCursor()

	// No new files: the second sweep must not touch the cursor or re-import.
	// Delete the swept log; an idle tick must NOT resurrect it.
	logs, _ := filepath.Glob(filepath.Join(dataDir, "projects", "owner__demo", "log", "*", "*.md"))
	os.Remove(logs[0])
	if rc := Run(nil, io.Discard, io.Discard); rc != 0 {
		t.Fatal("idle sweep failed")
	}
	if got := readCursor(); got != first {
		t.Fatalf("idle tick moved cursor %d -> %d", first, got)
	}
	if logs, _ := filepath.Glob(filepath.Join(dataDir, "projects", "owner__demo", "log", "*", "*.md")); len(logs) != 0 {
		t.Fatalf("idle tick re-imported: %v", logs)
	}
}

func TestSweepForceIgnoresCursor(t *testing.T) {
	dataDir, sessions := fixture(t)
	wt := gitWorktree(t, os.Getenv("HOME"))
	plantRollout(t, sessions, "rollout-a.jsonl", wt)
	if rc := Run(nil, io.Discard, io.Discard); rc != 0 {
		t.Fatal("first sweep failed")
	}
	logs, _ := filepath.Glob(filepath.Join(dataDir, "projects", "owner__demo", "log", "*", "*.md"))
	os.Remove(logs[0])

	if rc := Run([]string{"--force"}, io.Discard, io.Discard); rc != 0 {
		t.Fatal("forced sweep failed")
	}
	if logs, _ := filepath.Glob(filepath.Join(dataDir, "projects", "owner__demo", "log", "*", "*.md")); len(logs) != 1 {
		t.Fatalf("forced sweep did not re-harvest: %v", logs)
	}
}

func TestSweepNewRolloutAfterCursor(t *testing.T) {
	dataDir, sessions := fixture(t)
	wt := gitWorktree(t, os.Getenv("HOME"))
	plantRollout(t, sessions, "rollout-a.jsonl", wt)
	if rc := Run(nil, io.Discard, io.Discard); rc != 0 {
		t.Fatal("first sweep failed")
	}
	// A rollout written after the cursor is picked up next tick.
	p := plantRollout(t, sessions, "rollout-b.jsonl", wt)
	future := time.Now().Add(2 * time.Second)
	os.Chtimes(p, future, future)
	if rc := Run(nil, io.Discard, io.Discard); rc != 0 {
		t.Fatal("second sweep failed")
	}
	logs, _ := filepath.Glob(filepath.Join(dataDir, "projects", "owner__demo", "log", "*", "*.md"))
	if len(logs) != 2 {
		t.Fatalf("logs after new rollout = %v, want 2 sessions", logs)
	}
}

func TestSweepUnknownArg(t *testing.T) {
	if rc := Run([]string{"--bogus"}, io.Discard, io.Discard); rc != 2 {
		t.Fatalf("rc = %d, want 2", rc)
	}
}
