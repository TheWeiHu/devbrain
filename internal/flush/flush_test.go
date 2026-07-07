package flush

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimRight(string(out), "\n")
}

// setup returns a data-repo clone (with one pushed commit on main) and its
// bare origin.
func setup(t *testing.T) (data, origin string) {
	t.Helper()
	tmp := t.TempDir()
	origin = filepath.Join(tmp, "origin.git")
	data = filepath.Join(tmp, "data")
	mustGit(t, tmp, "init", "-q", "--bare", origin)
	mustGit(t, tmp, "clone", "-q", origin, data)
	mustGit(t, data, "checkout", "-q", "-B", "main")
	os.WriteFile(filepath.Join(data, "f"), []byte("base\n"), 0o644)
	mustGit(t, data, "add", ".")
	mustGit(t, data, "commit", "-qm", "init")
	mustGit(t, data, "push", "-q", "-u", "origin", "main")
	t.Setenv("DEVBRAIN_DATA", data)
	return data, origin
}

// A scrub-and-re-add of origin drops branch.main.remote; flush must still
// push new commits.
func TestFlushPushesWithoutUpstream(t *testing.T) {
	data, origin := setup(t)
	url := mustGit(t, data, "remote", "get-url", "origin")
	mustGit(t, data, "remote", "remove", "origin")
	mustGit(t, data, "remote", "add", "origin", url)

	os.WriteFile(filepath.Join(data, "new"), []byte("x\n"), 0o644)
	if rc := Run(nil, io.Discard, io.Discard); rc != 0 {
		t.Fatalf("Run = %d, want 0", rc)
	}
	if got := mustGit(t, origin, "log", "-1", "--format=%s", "main"); !strings.HasPrefix(got, "capture:") {
		t.Fatalf("origin main tip = %q, want capture commit", got)
	}
}

// Commits stranded by an earlier failed push go out on the next flush even
// when the working tree is clean.
func TestFlushRepushesStrandedCommits(t *testing.T) {
	data, origin := setup(t)
	os.WriteFile(filepath.Join(data, "stranded"), []byte("x\n"), 0o644)
	mustGit(t, data, "add", ".")
	mustGit(t, data, "commit", "-qm", "stranded")

	if rc := Run(nil, io.Discard, io.Discard); rc != 0 {
		t.Fatalf("Run = %d, want 0", rc)
	}
	if got := mustGit(t, origin, "log", "-1", "--format=%s", "main"); got != "stranded" {
		t.Fatalf("origin main tip = %q, want %q", got, "stranded")
	}
}

// No remote at all: flush commits locally and stays quiet.
func TestFlushNoRemote(t *testing.T) {
	data, _ := setup(t)
	mustGit(t, data, "remote", "remove", "origin")

	os.WriteFile(filepath.Join(data, "new"), []byte("x\n"), 0o644)
	var errBuf strings.Builder
	if rc := Run(nil, io.Discard, &errBuf); rc != 0 {
		t.Fatalf("Run = %d, want 0", rc)
	}
	if got := mustGit(t, data, "log", "-1", "--format=%s"); !strings.HasPrefix(got, "capture:") {
		t.Fatalf("local tip = %q, want capture commit", got)
	}
	if errBuf.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errBuf.String())
	}
}
