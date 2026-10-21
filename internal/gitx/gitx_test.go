package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// One fixture built in TestMain, no network: a bare origin with main and a
// clone the tests drive.
var (
	fxOrigin string
	fxClone  string
)

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := Repo{Dir: dir}.Run(args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return out
}

func mustGit(dir string, args ...string) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		panic("fixture git " + strings.Join(args, " ") + ": " + string(out))
	}
}

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "gitx-fixture")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmp)
	fxOrigin = filepath.Join(tmp, "origin.git")
	fxClone = filepath.Join(tmp, "clone")
	mustGit(tmp, "init", "-q", "--bare", fxOrigin)
	mustGit(tmp, "clone", "-q", fxOrigin, fxClone)
	os.WriteFile(filepath.Join(fxClone, "f"), []byte("base\n"), 0o644)
	mustGit(fxClone, "add", ".")
	mustGit(fxClone, "commit", "-qm", "init")
	mustGit(fxClone, "push", "-q", "origin", "HEAD:main")
	mustGit(fxClone, "checkout", "-qB", "main")
	os.Exit(m.Run())
}

func TestRunAndOk(t *testing.T) {
	r := Repo{Dir: fxClone}
	if out := run(t, fxClone, "rev-parse", "--abbrev-ref", "HEAD"); out != "main" {
		t.Errorf("Run trimmed output = %q", out)
	}
	if _, err := r.Run("rev-parse", "no-such-ref-xyz"); err == nil {
		t.Error("Run must fail on a bad ref")
	} else if !strings.Contains(err.Error(), "rev-parse") {
		t.Errorf("error should name the command: %v", err)
	}
	if !r.Ok("rev-parse", "HEAD") || r.Ok("rev-parse", "no-such-ref-xyz") {
		t.Error("Ok exit-0 semantics wrong")
	}
}
