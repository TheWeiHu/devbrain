package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixedClock(t *testing.T) {
	t.Helper()
	old := Now
	Now = func() time.Time {
		return time.Date(2026, 6, 20, 10, 30, 0, 0, time.UTC)
	}
	t.Cleanup(func() { Now = old })
}

func setup(t *testing.T) string {
	t.Helper()
	data := t.TempDir()
	t.Setenv("DEVBRAIN_DATA", data)
	t.Setenv("DEVBRAIN_PROJECT", "fix__demo")
	t.Setenv("DEVBRAIN_HARNESS", "")
	fixedClock(t)
	return data
}

func payload(t *testing.T, m map[string]any) *Event {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return &Event{Payload: b}
}

func claudeTranscript(t *testing.T, dir string) string {
	t.Helper()
	lines := []string{
		`{"type":"user","timestamp":"2026-06-20T10:29:00Z","cwd":"/x","message":{"content":"do the thing"}}`,
		`{"type":"assistant","timestamp":"2026-06-20T10:29:50Z","message":{"id":"m1","model":"claude-opus-4-8","usage":{"input_tokens":100,"output_tokens":40,"cache_creation_input_tokens":5,"cache_read_input_tokens":900},"content":[{"type":"text","text":"Working on it.\n\nDone: the thing now works."},{"type":"tool_use","name":"Edit","input":{"file_path":"/a/b/c.go"}}]}}`,
	}
	p := filepath.Join(dir, "t.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestGbrainFastBailAndRecord(t *testing.T) {
	data := setup(t)
	// payload without "gbrain" anywhere: nothing happens
	if err := Gbrain(payload(t, map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": "ls"}, "cwd": t.TempDir()})); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(data, "projects")); !os.IsNotExist(err) {
		t.Error("fast bail must write nothing")
	}
	// non-Bash tool: skip
	Gbrain(payload(t, map[string]any{"tool_name": "Edit", "tool_input": map[string]any{"command": `gbrain search "x"`}, "cwd": t.TempDir()}))
	if _, err := os.Stat(filepath.Join(data, "projects")); !os.IsNotExist(err) {
		t.Error("non-Bash must write nothing")
	}
	// real call: one JSON line
	ev := payload(t, map[string]any{
		"tool_name":     "Bash",
		"tool_input":    map[string]any{"command": `gbrain search "flaky tests"`},
		"tool_response": map[string]any{"stdout": "[0.9] fix__demo/testing -- notes\n"},
		"cwd":           t.TempDir(),
	})
	if err := Gbrain(ev); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(data, "projects", "fix__demo", "gbrain-queries.log"))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"ts": "2026-06-20T10:30:00Z", "project": "fix__demo", "cmd": "gbrain search \"flaky tests\"", "modes": ["search"], "hits": 1, "slugs": ["fix__demo/testing"], "auto": false}` + "\n"
	if string(b) != want {
		t.Errorf("record:\n got %q\nwant %q", b, want)
	}
}

func TestGbrainSlugRouting(t *testing.T) {
	data := setup(t)
	t.Setenv("DEVBRAIN_PROJECT", "") // routing only applies without the override
	ev := payload(t, map[string]any{
		"tool_name":     "Bash",
		"tool_input":    map[string]any{"command": `gbrain search "y"`},
		"tool_response": "[0.8] other__repo/page -- body\n",
		"cwd":           t.TempDir(), // no git repo -> would be miscellaneous
	})
	if err := Gbrain(ev); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(data, "projects", "other__repo", "gbrain-queries.log")); err != nil {
		t.Error("slug prefix in output must route the record to that project")
	}
}

func TestCdTarget(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ cmd, cwd, want string }{
		{`cd /abs/repo && gbrain search "x"`, "/w", "/abs/repo"},
		{`cd rel && gbrain get a/b`, "/w", "/w/rel"},
		{`v="/var/repo"; (cd "$v" && gbrain search "q")`, "/w", "/var/repo"},
		{`V=/var/x; cd ${V} && gbrain list`, "/w", "/var/x"},
		// legacy quirk: "cd" inside prose still matches; the caller's IsDir
		// check is what rejects it (same as the bash script's [ -d ])
		{`gbrain search "no cd here"`, "/w", `/w/here"`},
		{`echo done`, "/w", ""},
	} {
		if got := cdTarget(c.cmd, c.cwd); got != c.want {
			t.Errorf("cdTarget(%q) = %q, want %q", c.cmd, got, c.want)
		}
	}
}

func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	f()
	w.Close()
	os.Stdout = old
	b, _ := os.ReadFile(r.Name())
	if len(b) == 0 {
		buf := make([]byte, 1<<16)
		n, _ := r.Read(buf)
		b = buf[:n]
	}
	r.Close()
	return string(b)
}

func TestSessionStartNudge(t *testing.T) {
	data := setup(t)
	// no brain, no tasks -> silence
	out := captureStdout(t, func() { SessionStart(payload(t, map[string]any{"cwd": t.TempDir()})) })
	if out != "" {
		t.Errorf("empty project must be silent, got %q", out)
	}
	// pages + open tasks -> counts in the nudge
	pdir := filepath.Join(data, "projects", "fix__demo")
	os.MkdirAll(filepath.Join(pdir, "brain"), 0o755)
	os.WriteFile(filepath.Join(pdir, "brain", "a.md"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(pdir, "brain", "b.md"), []byte("x"), 0o644)
	os.MkdirAll(filepath.Join(pdir, "todo"), 0o755)
	os.WriteFile(filepath.Join(pdir, "todo", "0001-t.md"), []byte("---\nid: 0001-t\nstatus: open\n---\n\n# T\n"), 0o644)
	os.WriteFile(filepath.Join(pdir, "todo", "0002-d.md"), []byte("---\nid: 0002-d\nstatus: done\n---\n\n# D\n"), 0o644)
	out = captureStdout(t, func() { SessionStart(payload(t, map[string]any{"cwd": t.TempDir()})) })
	var wrap map[string]map[string]string
	if err := json.Unmarshal([]byte(out), &wrap); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, out)
	}
	msg := wrap["hookSpecificOutput"]["additionalContext"]
	if !strings.Contains(msg, "project `fix__demo` with 2 brain pages and 1 open task.") {
		t.Errorf("counts wrong: %s", msg)
	}
	if wrap["hookSpecificOutput"]["hookEventName"] != "SessionStart" {
		t.Error("hookEventName missing")
	}
}

func TestTurnMarker(t *testing.T) {
	setup(t)
	t.Setenv("NIGHTSHIFT_MARKER", "")
	// unset -> no-op (registered globally, ordinary sessions must not litter)
	if err := TurnMarker(payload(t, map[string]any{"session_id": "s"})); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "ns", "turns.log")
	t.Setenv("NIGHTSHIFT_MARKER", marker)
	if err := TurnMarker(payload(t, map[string]any{"session_id": "sX", "stop_hook_active": true})); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "2026-06-20T10:30:00Z\tsX\tstop_active=true\n" {
		t.Errorf("marker line %q", b)
	}
}

func agentTranscript(t *testing.T, dir string) string {
	t.Helper()
	lines := []string{
		`{"type":"user","isSidechain":true,"timestamp":"2026-06-20T10:31:00Z","cwd":"/x","message":{"content":"explore the code"}}`,
		`{"type":"assistant","isSidechain":true,"timestamp":"2026-06-20T10:31:40Z","message":{"id":"a1","model":"claude-haiku-4-5","usage":{"input_tokens":50,"output_tokens":20,"cache_creation_input_tokens":0,"cache_read_input_tokens":300},"content":[{"type":"text","text":"Found it."}]}}`,
	}
	p := filepath.Join(dir, "agent-abc123.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}
