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

func TestCaptureWritesHeaderAndEntry(t *testing.T) {
	data := setup(t)
	cwd := t.TempDir()
	ev := payload(t, map[string]any{
		"prompt": "fix the bug with sk-abcdefghijklmnopqrstuvwx now", "cwd": cwd, "session_id": "Sess One!"})
	if err := Capture(ev); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Base(cwd) // sanitized below
	file := filepath.Join(data, "projects", "fix__demo", "log", "2026-06-20",
		strings.ToLower(wt)+".sess-one.md")
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("log not written: %v", err)
	}
	got := string(b)
	for _, want := range []string{
		"# fix__demo — 2026-06-20 — session sess-one\n",
		"> devbrain Stage A raw prompt log. Append-only, source of truth.\n",
		"> agent: claude · worktree: ", "· cwd: " + cwd + " · times in UTC\n",
		"> cost: `tokens:` lines are per-turn best-effort;",
		"## 10:30:00\n\nfix the bug with [REDACTED] now\n\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("log missing %q:\n%s", want, got)
		}
	}
	// second prompt appends without a second header
	if err := Capture(payload(t, map[string]any{"prompt": "again", "cwd": cwd, "session_id": "Sess One!"})); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(file)
	if strings.Count(string(b), "# fix__demo — ") != 1 {
		t.Error("header written twice")
	}
	if !strings.HasSuffix(string(b), "## 10:30:00\n\nagain\n\n") {
		t.Errorf("append shape wrong: %q", string(b))
	}
}

func TestCaptureSkipsSyntheticAndEmpty(t *testing.T) {
	data := setup(t)
	for _, p := range []string{"", "<system-reminder>noise</system-reminder>", "  <skill><name>x</name>"} {
		if err := Capture(payload(t, map[string]any{"prompt": p, "cwd": t.TempDir(), "session_id": "s"})); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(data, "projects")); !os.IsNotExist(err) {
		t.Error("synthetic/empty prompts must write nothing")
	}
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

func TestResponseAppendsTraceAndSidecar(t *testing.T) {
	data := setup(t)
	cwd := t.TempDir()
	tr := claudeTranscript(t, t.TempDir())
	// a captured prompt first, so the log file exists
	if err := Capture(payload(t, map[string]any{"prompt": "do the thing", "cwd": cwd, "session_id": "s1"})); err != nil {
		t.Fatal(err)
	}
	ev := payload(t, map[string]any{"transcript_path": tr, "cwd": cwd, "session_id": "s1"})
	if err := Response(ev); err != nil {
		t.Fatal(err)
	}
	logs, _ := filepath.Glob(filepath.Join(data, "projects", "fix__demo", "log", "2026-06-20", "*.s1.md"))
	if len(logs) != 1 {
		t.Fatalf("expected one log, got %v", logs)
	}
	got, _ := os.ReadFile(logs[0])
	for _, want := range []string{
		"↳ 10:30:00 — Done: the thing now works.\n",
		"   touched: c.go  ·  tools: Edit×1  ·  tokens: 100/40/5/900 · model: claude-opus-4-8\n",
		"   ⤷ response sample:\n",
		"   > Working on it.\n",
	} {
		if !strings.Contains(string(got), want) {
			t.Errorf("trace missing %q:\n%s", want, got)
		}
	}
	side, err := os.ReadFile(filepath.Join(data, "projects", "fix__demo", "tokens.jsonl"))
	if err != nil {
		t.Fatal("sidecar not written")
	}
	want := `{"ts": "2026-06-20T10:29:50Z", "session": "s1", "model": "claude-opus-4-8", "in": 100, "out": 40, "cache_create": 5, "cache_read": 900, "auto": false, "turn": "2026-06-20T10:29:00Z"}` + "\n"
	if string(side) != want {
		t.Errorf("sidecar:\n got %q\nwant %q", side, want)
	}
}

func TestResponseNoLogStillWritesSidecar(t *testing.T) {
	data := setup(t)
	tr := claudeTranscript(t, t.TempDir())
	// cwd under a nightshift path -> auto:true
	cwd := filepath.Join(t.TempDir(), "nightshift", "demo-w1")
	os.MkdirAll(cwd, 0o755)
	if err := Response(payload(t, map[string]any{"transcript_path": tr, "cwd": cwd, "session_id": "s2"})); err != nil {
		t.Fatal(err)
	}
	side, err := os.ReadFile(filepath.Join(data, "projects", "fix__demo", "tokens.jsonl"))
	if err != nil {
		t.Fatal("sidecar must be written even without a log file")
	}
	if !strings.Contains(string(side), `"auto": true`) {
		t.Errorf("nightshift cwd must mark auto: %s", side)
	}
	logs, _ := filepath.Glob(filepath.Join(data, "projects", "fix__demo", "log", "*", "*.md"))
	if len(logs) != 0 {
		t.Error("no human-readable trace without a captured prompt")
	}
}

func TestResponseMissingTranscript(t *testing.T) {
	data := setup(t)
	if err := Response(payload(t, map[string]any{"transcript_path": "/nope/gone.jsonl", "cwd": t.TempDir(), "session_id": "s"})); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(data, "projects")); !os.IsNotExist(err) {
		t.Error("missing transcript must write nothing")
	}
}

func TestMemoryMirrorsRedactedChangedOnly(t *testing.T) {
	data := setup(t)
	home := t.TempDir()
	memdir := filepath.Join(home, "proj-slug", "memory")
	os.MkdirAll(memdir, 0o755)
	os.WriteFile(filepath.Join(memdir, "note.md"), []byte("key is sk-abcdefghijklmnopqrstuvwx\n"), 0o644)
	os.WriteFile(filepath.Join(memdir, "skip.txt"), []byte("not md"), 0o644)
	tr := filepath.Join(home, "proj-slug", "sess.jsonl")
	os.WriteFile(tr, []byte("{}"), 0o644)

	ev := payload(t, map[string]any{"transcript_path": tr, "cwd": t.TempDir(), "session_id": "s"})
	if err := Memory(ev); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(data, "projects", "fix__demo", "memory", "note.md")
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	// command-substitution semantics: trailing newline stripped, secret redacted
	if string(b) != "key is [REDACTED]" {
		t.Errorf("mirrored content %q", b)
	}
	if _, err := os.Stat(filepath.Join(data, "projects", "fix__demo", "memory", "skip.txt")); !os.IsNotExist(err) {
		t.Error("non-md files must not be mirrored")
	}
	// unchanged second run leaves mtime alone (no churn)
	st1, _ := os.Stat(out)
	time.Sleep(10 * time.Millisecond)
	if err := Memory(ev); err != nil {
		t.Fatal(err)
	}
	st2, _ := os.Stat(out)
	if !st1.ModTime().Equal(st2.ModTime()) {
		t.Error("unchanged file was rewritten")
	}
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
		"tool_name": "Bash",
		"tool_input": map[string]any{"command": `gbrain search "flaky tests"`},
		"tool_response": map[string]any{"stdout": "[0.9] fix__demo/testing -- notes\n"},
		"cwd": t.TempDir(),
	})
	if err := Gbrain(ev); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(data, "projects", "fix__demo", "gbrain-queries.log"))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"ts": "2026-06-20T10:30:00Z", "project": "fix__demo", "cmd": "gbrain search \"flaky tests\"", "modes": ["search"], "hits": 1, "slugs": ["fix__demo/testing"]}` + "\n"
	if string(b) != want {
		t.Errorf("record:\n got %q\nwant %q", b, want)
	}
}

func TestGbrainSlugRouting(t *testing.T) {
	data := setup(t)
	t.Setenv("DEVBRAIN_PROJECT", "") // routing only applies without the override
	ev := payload(t, map[string]any{
		"tool_name": "Bash",
		"tool_input": map[string]any{"command": `gbrain search "y"`},
		"tool_response": "[0.8] other__repo/page -- body\n",
		"cwd": t.TempDir(), // no git repo -> would be miscellaneous
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

func TestSubagentResponseWritesSidecarOnly(t *testing.T) {
	data := setup(t)
	cwd := t.TempDir()
	ap := agentTranscript(t, t.TempDir())
	ev := payload(t, map[string]any{"agent_transcript_path": ap, "cwd": cwd, "session_id": "s1"})
	if err := SubagentResponse(ev); err != nil {
		t.Fatal(err)
	}
	side, err := os.ReadFile(filepath.Join(data, "projects", "fix__demo", "tokens.jsonl"))
	if err != nil {
		t.Fatal("sidecar not written")
	}
	want := `{"ts": "2026-06-20T10:31:40Z", "session": "s1", "model": "claude-haiku-4-5", "in": 50, "out": 20, "cache_create": 0, "cache_read": 300, "auto": false, "turn": "agent-abc123:2026-06-20T10:31:00Z"}` + "\n"
	if string(side) != want {
		t.Errorf("sidecar:\n got %q\nwant %q", side, want)
	}
	logs, _ := filepath.Glob(filepath.Join(data, "projects", "fix__demo", "log", "*", "*.md"))
	if len(logs) != 0 {
		t.Error("subagent capture must never write prompt-log entries")
	}
}

// An older harness whose SubagentStop payload names only the PARENT
// transcript must no-op: re-reading the parent here would double-capture
// the in-flight parent turn.
func TestSubagentResponseIgnoresParentTranscript(t *testing.T) {
	data := setup(t)
	tr := claudeTranscript(t, t.TempDir())
	ev := payload(t, map[string]any{"transcript_path": tr, "cwd": t.TempDir(), "session_id": "s1"})
	if err := SubagentResponse(ev); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(data, "projects")); !os.IsNotExist(err) {
		t.Error("parent transcript path must write nothing")
	}
}
