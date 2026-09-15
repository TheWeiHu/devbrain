package main_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TheWeiHu/devbrain/internal/clitest"
)

type brainFixture struct {
	h                             *clitest.Harness
	home, personal, work, project string
}

func newBrainFixture(t *testing.T) brainFixture {
	t.Helper()
	h := clitest.New(t)
	home := t.TempDir()
	home, _ = filepath.EvalSymlinks(home)
	f := brainFixture{h, home, filepath.Join(home, "personal"), filepath.Join(home, "work"), filepath.Join(home, "code")}
	for _, dir := range []string{f.personal, f.work, f.project} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{{"init", "-q", dir}, {"-C", dir, "remote", "add", "origin", "https://github.com/example/project.git"}} {
			if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
				t.Fatalf("git: %v: %s", err, out)
			}
		}
	}
	h.Env = map[string]string{"HOME": home, "XDG_CONFIG_HOME": filepath.Join(home, "config"), "DEVBRAIN_DATA": "", "DEVBRAIN_PROJECT": "", "DEVBRAIN_BRAIN": "", "DEVBRAIN_GBRAIN": "missing-gbrain", "DEVBRAIN_SWEEP_CURSOR_DIR": filepath.Join(home, "state")}
	f.writeConfig(t, "work")
	return f
}

func (f brainFixture) writeConfig(t *testing.T, assigned string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"data": f.personal, "brains": map[string]string{"work": f.work}, "project_brains": map[string]string{"example__project": assigned}})
	clitest.WriteFile(t, filepath.Join(f.home, "config", "devbrain", "config.json"), string(body))
}

func (f brainFixture) run(args ...string) clitest.Result {
	return f.h.RunWith(clitest.RunOpts{Dir: f.project}, args...)
}

func TestNamedBrainCommandsStayWithinSelectedStore(t *testing.T) {
	f := newBrainFixture(t)
	if r := f.run("data-dir"); r.Code != 0 || r.Out() != f.work {
		t.Fatalf("assigned: %+v", r)
	}
	if r := f.run("--brain", "default", "data-dir"); r.Code != 0 || r.Out() != f.personal {
		t.Fatalf("override: %+v", r)
	}
	if r := f.run("--brain", "typo", "todo", "add", "Must Not Exist"); r.Code == 0 {
		t.Fatalf("unknown brain accepted: %+v", r)
	}
	r := f.run("todo", "add", "Work Task")
	if r.Code != 0 {
		t.Fatalf("todo: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(f.work, "projects", "example__project", "todo", r.Out()+".md")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.personal, "projects")); !os.IsNotExist(err) {
		t.Fatal("work task reached personal brain")
	}
	clitest.WriteFile(t, filepath.Join(f.personal, "projects", "example__project", "brain", "secret.md"), "# Personal Sentinel\n\nneedle personal-secret")
	clitest.WriteFile(t, filepath.Join(f.work, "projects", "example__project", "brain", "secret.md"), "# Work Sentinel\n\nneedle work-secret")
	f.h.Env["DEVBRAIN_GBRAIN"] = "/bin/echo" // a global engine must never receive this request
	r = f.run("brain", "search", "needle", "--global")
	if r.Code != 0 || !strings.Contains(r.Stdout, "work-secret") || strings.Contains(r.Stdout, "personal-secret") {
		t.Fatalf("search isolation: %+v", r)
	}
	r = f.run("brain", "get", "example__project/secret")
	if r.Code != 0 || !strings.Contains(r.Stdout, "work-secret") || strings.Contains(r.Stdout, "personal-secret") {
		t.Fatalf("get isolation: %+v", r)
	}
	r = f.run("--brain", "default", "brain", "get", "example__project/secret")
	if r.Code != 0 || !strings.Contains(r.Stdout, "personal-secret") {
		t.Fatalf("explicit read: %+v", r)
	}
}

func TestNamedBrainsUnavailableAndOverlapFailClosed(t *testing.T) {
	f := newBrainFixture(t)
	if err := os.Rename(f.work, f.work+"-offline"); err != nil {
		t.Fatal(err)
	}
	if r := f.run("todo", "add", "Must Not Fall Back"); r.Code == 0 {
		t.Fatalf("missing checkout accepted: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(f.personal, "projects")); !os.IsNotExist(err) {
		t.Fatal("fallback wrote to personal brain")
	}
	if r := f.run("brains", "add", "nested", "--data", filepath.Join(f.personal, "nested")); r.Code == 0 {
		t.Fatal("nested destination accepted")
	}
}

func TestNamedBrainInstallPreservesDefaultAndPreferences(t *testing.T) {
	f := newBrainFixture(t)
	clitest.WriteFile(t, filepath.Join(f.personal, "preferences", "global.md"), "personal-secret")
	clitest.WriteFile(t, filepath.Join(f.work, "preferences", "global.md"), "work-secret")
	clitest.WriteFile(t, filepath.Join(f.home, ".codex", "AGENTS.md"), "Keep This User Instruction\n")
	r := f.run("install", "--only", "skills", "--yes", "--without-gbrain")
	if r.Code != 0 {
		t.Fatalf("install: %+v", r)
	}
	r = f.run("--brain", "default", "data-dir")
	if r.Code != 0 || r.Out() != f.personal {
		t.Fatalf("install changed default: %+v", r)
	}
}

func (f brainFixture) transcript(t *testing.T, sid, cwd, prompt string) {
	t.Helper()
	user, _ := json.Marshal(map[string]any{"type": "user", "cwd": cwd, "timestamp": "2026-08-10T10:00:00Z", "message": map[string]any{"content": prompt}})
	assistant, _ := json.Marshal(map[string]any{"type": "assistant", "cwd": cwd, "timestamp": "2026-08-10T10:00:02Z", "message": map[string]any{"id": sid, "model": "claude-sonnet-4-6", "content": []map[string]string{{"type": "text", "text": "Captured the fixture session and verified its result."}}, "usage": map[string]int{"input_tokens": 100, "output_tokens": 20}}})
	clitest.WriteFile(t, filepath.Join(f.home, ".claude", "projects", "fixture", sid+".jsonl"), string(user)+"\n"+string(assistant)+"\n")
}

func TestCapturePartitionsAndKeepsExistingSessionsInPlace(t *testing.T) {
	f := newBrainFixture(t)
	f.writeConfig(t, "default")
	f.transcript(t, "old-session", f.project, "Old session sentinel")
	if r := f.run("import", "--apply"); r.Code != 0 {
		t.Fatalf("first capture: %+v", r)
	}
	oldPath := filepath.Join(f.personal, "projects", "example__project", "log", "2026-08-10", "code.old-session.md")
	before, err := os.ReadFile(oldPath)
	if err != nil {
		t.Fatal(err)
	}
	f.writeConfig(t, "work")
	f.transcript(t, "new-session", f.project, "New work sentinel")
	other := filepath.Join(f.home, "other-code")
	if out, err := exec.Command("git", "init", "-q", other).CombinedOutput(); err != nil {
		t.Fatalf("git: %v %s", err, out)
	}
	if out, err := exec.Command("git", "-C", other, "remote", "add", "origin", "https://github.com/example/personal.git").CombinedOutput(); err != nil {
		t.Fatalf("git: %v %s", err, out)
	}
	f.transcript(t, "personal-session", other, "Personal sentinel")
	for i := 0; i < 2; i++ {
		if r := f.run("import", "--apply"); r.Code != 0 {
			t.Fatalf("capture %d: %+v", i, r)
		}
	}
	after, err := os.ReadFile(oldPath)
	if err != nil || string(before) != string(after) {
		t.Fatalf("existing log changed: %v", err)
	}
	workLogs, _ := filepath.Glob(filepath.Join(f.work, "projects", "*", "log", "*", "*.md"))
	if len(workLogs) != 1 || !strings.HasSuffix(workLogs[0], "code.new-session.md") {
		t.Fatalf("work logs: %v", workLogs)
	}
	personalLogs, _ := filepath.Glob(filepath.Join(f.personal, "projects", "*", "log", "*", "*.md"))
	if len(personalLogs) != 2 {
		t.Fatalf("personal logs: %v", personalLogs)
	}
	for _, tc := range []struct{ root, project, session string }{{f.work, "example__project", "new-session"}, {f.personal, "example__project", "old-session"}, {f.personal, "example__personal", "personal-session"}} {
		raw, err := os.ReadFile(filepath.Join(tc.root, "projects", tc.project, "tokens.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
		if len(lines) != 1 || !strings.Contains(lines[0], tc.session) {
			t.Fatalf("duplicate or misrouted tokens: %s", raw)
		}
	}
}

func TestSelectedSweepDoesNotConsumeOtherBrainCursor(t *testing.T) {
	f := newBrainFixture(t)
	f.transcript(t, "work-session", f.project, "Work sentinel")
	f.transcript(t, "misc-session", filepath.Join(f.home, "notes"), "Default sentinel")
	if r := f.run("--brain", "work", "sweep", "--force"); r.Code != 0 {
		t.Fatalf("selected: %+v", r)
	}
	personalLogs, _ := filepath.Glob(filepath.Join(f.personal, "projects", "*", "log", "*", "*.md"))
	if len(personalLogs) != 0 {
		t.Fatalf("selected sweep harvested other brain: %v", personalLogs)
	}
	if r := f.run("sweep"); r.Code != 0 {
		t.Fatalf("all: %+v", r)
	}
	personalLogs, _ = filepath.Glob(filepath.Join(f.personal, "projects", "*", "log", "*", "*.md"))
	if len(personalLogs) != 1 {
		t.Fatalf("selected cursor starved default brain: %v", personalLogs)
	}
}

func TestUnavailableBrainCaptureRetriesWithoutFallback(t *testing.T) {
	f := newBrainFixture(t)
	f.transcript(t, "work-session", f.project, "Private work sentinel")
	if err := os.Rename(f.work, f.work+"-offline"); err != nil {
		t.Fatal(err)
	}
	r := f.run("sweep", "--force")
	if !strings.Contains(r.Stderr, "will retry") {
		t.Fatalf("missing retry: %+v", r)
	}
	logs, _ := filepath.Glob(filepath.Join(f.personal, "projects", "*", "log", "*", "*.md"))
	if len(logs) != 0 {
		t.Fatalf("fallback leak: %v", logs)
	}
	if err := os.Rename(f.work+"-offline", f.work); err != nil {
		t.Fatal(err)
	}
	if r := f.run("sweep"); r.Code != 0 {
		t.Fatalf("retry: %+v", r)
	}
	logs, _ = filepath.Glob(filepath.Join(f.work, "projects", "*", "log", "*", "*.md"))
	if len(logs) != 1 {
		t.Fatalf("session lost on retry: %v", logs)
	}
}

func TestBrainRegistrationClonesAndRejectsWrongRemote(t *testing.T) {
	f := newBrainFixture(t)
	remote := filepath.Join(f.home, "third.git")
	if out, err := exec.Command("git", "init", "--bare", "-q", remote).CombinedOutput(); err != nil {
		t.Fatalf("git: %v %s", err, out)
	}
	data := filepath.Join(f.home, "third")
	r := f.run("brains", "add", "third", "--repo", remote, "--data", data)
	if r.Code != 0 {
		t.Fatalf("register clone: %+v", r)
	}
	r = f.run("--brain", "third", "data-dir")
	if r.Code != 0 || r.Out() != data {
		t.Fatalf("new brain not selectable: %+v", r)
	}
	if r = f.run("brains", "add", "wrong", "--repo", "different/repository", "--data", data); r.Code == 0 {
		t.Fatal("wrong remote was registered")
	}
	if r = f.run("brains", "assign", "third"); r.Code != 0 {
		t.Fatalf("assign: %+v", r)
	}
	if r = f.run("data-dir"); r.Code != 0 || r.Out() != data {
		t.Fatalf("assignment: %+v", r)
	}
	if r = f.run("brains", "default", "third"); r.Code != 0 {
		t.Fatalf("default: %+v", r)
	}
	r = f.h.RunWith(clitest.RunOpts{Dir: f.home}, "data-dir")
	if r.Code != 0 || r.Out() != data {
		t.Fatalf("unassigned default: %+v", r)
	}
}

func TestUnknownBrainCannotCaptureIntoLegacyDefault(t *testing.T) {
	f := newBrainFixture(t)
	body, _ := json.Marshal(map[string]string{"data": f.personal})
	clitest.WriteFile(t, filepath.Join(f.home, "config", "devbrain", "config.json"), string(body))
	f.transcript(t, "private-session", f.project, "Must not fall back")
	for _, args := range [][]string{{"--brain", "missing", "import", "--apply"}, {"--brain", "missing", "sweep", "--force"}} {
		if r := f.run(args...); r.Code == 0 {
			t.Fatalf("unknown brain accepted: %+v", r)
		}
	}
	if _, err := os.Stat(filepath.Join(f.personal, "projects")); !os.IsNotExist(err) {
		t.Fatal("unknown brain captured into default")
	}
}
