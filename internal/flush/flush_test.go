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
	mustGit(t, origin, "symbolic-ref", "HEAD", "refs/heads/main")
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

// A dirty-tree autostash conflict used to occur after pull's rebase completed,
// outside the rebase directories the old guard checked. Flush must commit first
// and must never push those conflict markers.
func TestFlushDoesNotPushAutostashApplyConflict(t *testing.T) {
	data, origin := setup(t)
	other := filepath.Join(t.TempDir(), "other")
	mustGit(t, filepath.Dir(other), "clone", "-q", origin, other)
	if err := os.WriteFile(filepath.Join(other, "f"), []byte("theirs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, other, "add", ".")
	mustGit(t, other, "commit", "-qm", "theirs")
	mustGit(t, other, "push", "-q", "origin", "main")

	if err := os.WriteFile(filepath.Join(data, "f"), []byte("ours\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rc := Run(nil, io.Discard, io.Discard); rc != 1 {
		t.Fatalf("Run = %d, want 1 for rebase conflict", rc)
	}
	remote := mustGit(t, origin, "show", "main:f")
	if remote != "theirs" {
		t.Fatalf("remote content = %q, want theirs", remote)
	}
	local, err := os.ReadFile(filepath.Join(data, "f"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(local), "<<<<<<<") || strings.Contains(string(local), ">>>>>>>") {
		t.Fatalf("local file contains conflict markers:\n%s", local)
	}
	if got := mustGit(t, data, "log", "-1", "--format=%s"); !strings.HasPrefix(got, "capture:") {
		t.Fatalf("local tip = %q, want durable capture commit", got)
	}
}

func TestFlushRejectsRelativeDataDir(t *testing.T) {
	t.Setenv("DEVBRAIN_DATA", "private-data")
	var errBuf strings.Builder
	if rc := Run(nil, io.Discard, &errBuf); rc != 1 {
		t.Fatalf("Run = %d, want 1", rc)
	}
	if !strings.Contains(errBuf.String(), "is relative") {
		t.Fatalf("stderr = %q, want relative-path diagnostic", errBuf.String())
	}
}

func TestFlushReportsSyncFailureAfterDurableLocalCommit(t *testing.T) {
	data, _ := setup(t)
	mustGit(t, data, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing.git"))
	if err := os.WriteFile(filepath.Join(data, "new"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var errBuf strings.Builder
	if rc := Run(nil, io.Discard, &errBuf); rc != 1 {
		t.Fatalf("Run = %d, want 1", rc)
	}
	if !strings.Contains(errBuf.String(), "local changes remain committed for retry") {
		t.Fatalf("stderr = %q, want retry diagnostic", errBuf.String())
	}
	if got := mustGit(t, data, "log", "-1", "--format=%s"); !strings.HasPrefix(got, "capture:") {
		t.Fatalf("local tip = %q, want durable capture commit", got)
	}
}

func TestConcurrentFlushSkipsWhileLockIsHeld(t *testing.T) {
	data, _ := setup(t)
	lock, err := acquireLock(data)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.close()
	var out strings.Builder
	if rc := Run(nil, &out, io.Discard); rc != 0 {
		t.Fatalf("Run = %d, want 0", rc)
	}
	if !strings.Contains(out.String(), "already running") {
		t.Fatalf("stdout = %q, want lock diagnostic", out.String())
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

// The tracking ref is gone post-scrub and an offline pull can't restore it;
// pushNeeded must not silently report false.
func TestPushNeededWithoutTrackingRef(t *testing.T) {
	data, _ := setup(t)
	mustGit(t, data, "update-ref", "-d", "refs/remotes/origin/main")
	if !pushNeeded(data, "main") {
		t.Fatal("pushNeeded = false with origin/main tracking ref absent")
	}
}

// A conflicted pull must abort, not commit conflict markers via add -A.
func TestFlushAbortsConflictedPull(t *testing.T) {
	data, origin := setup(t)
	other := filepath.Join(t.TempDir(), "other")
	mustGit(t, filepath.Dir(other), "clone", "-q", origin, other)
	os.WriteFile(filepath.Join(other, "f"), []byte("theirs\n"), 0o644)
	mustGit(t, other, "add", ".")
	mustGit(t, other, "commit", "-qm", "theirs")
	mustGit(t, other, "push", "-q", "origin", "main")

	os.WriteFile(filepath.Join(data, "f"), []byte("ours\n"), 0o644)
	mustGit(t, data, "add", ".")
	mustGit(t, data, "commit", "-qm", "ours")

	if rc := Run(nil, io.Discard, io.Discard); rc != 1 {
		t.Fatalf("Run = %d, want 1", rc)
	}
	if _, err := os.Stat(filepath.Join(data, ".git", "rebase-merge")); err == nil {
		t.Fatal("rebase left in progress")
	}
	if got := mustGit(t, data, "log", "-1", "--format=%s"); got != "ours" {
		t.Fatalf("local tip = %q, want %q (nothing committed mid-conflict)", got, "ours")
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
