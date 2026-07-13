package flush

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
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

	if rc := Run(nil, io.Discard, io.Discard); rc != 0 {
		t.Fatalf("Run = %d, want 0", rc)
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

func stampDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DEVBRAIN_FLUSH_STAMP_DIR", dir)
	return dir
}

func writeStampAt(t *testing.T, dir string, at time.Time) {
	t.Helper()
	os.WriteFile(filepath.Join(dir, "flush-stamp"),
		[]byte(strconv.FormatInt(at.Unix(), 10)+"\n"), 0o644)
}

// The first scheduled commit stamps the machine-local window; the next
// scheduled tick defers (files stay on disk); a manual flush is unthrottled
// and resets the window.
func TestScheduledFlushThrottlesCommits(t *testing.T) {
	dir := stampDir(t)
	data, _ := setup(t)

	// Never committed on this machine -> the first scheduled tick commits.
	os.WriteFile(filepath.Join(data, "a"), []byte("x\n"), 0o644)
	if rc := Run([]string{"--scheduled"}, io.Discard, io.Discard); rc != 0 {
		t.Fatalf("Run = %d, want 0", rc)
	}
	if got := mustGit(t, data, "log", "-1", "--format=%s", "main"); !strings.HasPrefix(got, "capture:") {
		t.Fatalf("first scheduled flush did not commit: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "flush-stamp")); err != nil {
		t.Fatal("commit did not write the flush stamp")
	}

	// Inside the window -> defer; the swept file stays on disk uncommitted.
	os.WriteFile(filepath.Join(data, "b"), []byte("y\n"), 0o644)
	if rc := Run([]string{"--scheduled"}, io.Discard, io.Discard); rc != 0 {
		t.Fatalf("Run = %d, want 0", rc)
	}
	if st := mustGit(t, data, "status", "--porcelain"); st == "" {
		t.Fatal("deferred flush should leave the new file uncommitted on disk")
	}

	// Manual flush: no throttle.
	if rc := Run(nil, io.Discard, io.Discard); rc != 0 {
		t.Fatalf("manual Run = %d, want 0", rc)
	}
	if st := mustGit(t, data, "status", "--porcelain"); st != "" {
		t.Fatalf("manual flush should commit the deferred file, tree still dirty:\n%s", st)
	}
}

// Once the machine's stamp is older than commitEvery, the scheduled tick
// commits — even if HEAD is fresh (e.g. a commit just pulled from another
// machine must not starve this one's).
func TestScheduledFlushCommitsAfterInterval(t *testing.T) {
	dir := stampDir(t)
	data, origin := setup(t) // setup's init commit = fresh HEAD
	writeStampAt(t, dir, time.Now().Add(-commitEvery-time.Minute))
	os.WriteFile(filepath.Join(data, "new"), []byte("x\n"), 0o644)

	if rc := Run([]string{"--scheduled"}, io.Discard, io.Discard); rc != 0 {
		t.Fatalf("Run = %d, want 0", rc)
	}
	if got := mustGit(t, origin, "log", "-1", "--format=%s", "main"); !strings.HasPrefix(got, "capture:") {
		t.Fatalf("scheduled flush past the interval did not commit+push: %q", got)
	}
}

// A stamp in the future (clock skew, restored backup) must not freeze the
// throttle: the negative age reads as "huge", so the tick commits.
func TestScheduledFlushSurvivesFutureStamp(t *testing.T) {
	dir := stampDir(t)
	data, _ := setup(t)
	writeStampAt(t, dir, time.Now().Add(48*time.Hour))
	os.WriteFile(filepath.Join(data, "new"), []byte("x\n"), 0o644)

	if rc := Run([]string{"--scheduled"}, io.Discard, io.Discard); rc != 0 {
		t.Fatalf("Run = %d, want 0", rc)
	}
	if st := mustGit(t, data, "status", "--porcelain"); st != "" {
		t.Fatalf("future stamp froze the throttle; tree still dirty:\n%s", st)
	}
}

// Stranded commits are pushed by a scheduled tick even inside the throttle
// window — the throttle defers new commits, never durability.
func TestScheduledFlushStillRepushesStranded(t *testing.T) {
	dir := stampDir(t)
	writeStampAt(t, dir, time.Now())
	data, origin := setup(t)
	os.WriteFile(filepath.Join(data, "s"), []byte("x\n"), 0o644)
	mustGit(t, data, "add", ".")
	mustGit(t, data, "commit", "-qm", "stranded: local only")

	if rc := Run([]string{"--scheduled"}, io.Discard, io.Discard); rc != 0 {
		t.Fatalf("Run = %d, want 0", rc)
	}
	if got := mustGit(t, origin, "log", "-1", "--format=%s", "main"); !strings.HasPrefix(got, "stranded:") {
		t.Fatalf("stranded commit not pushed by scheduled tick: %q", got)
	}
}
