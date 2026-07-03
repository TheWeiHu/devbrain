package nightshift_test

// Port of scripts/test-nightshift-reset.sh

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TheWeiHu/devbrain/internal/clitest"
)

func TestNightshiftReset(t *testing.T) {
	// A bare "remote" with main, plus a clone standing in for ~/nightshift/<repo>.
	tmp := t.TempDir()
	rem := filepath.Join(tmp, "rem.git")
	seed := filepath.Join(tmp, "seed")
	base := filepath.Join(tmp, "clone")

	clitest.Git(t, "", "init", "-q", "--bare", rem)
	clitest.Git(t, "", "clone", "-q", rem, seed)
	clitest.WriteFile(t, filepath.Join(seed, "f"), "x\n")
	clitest.Git(t, seed, "add", "f")
	clitest.Git(t, seed, "commit", "-qm", "init")
	clitest.Git(t, seed, "push", "-q", "origin", "HEAD:main")
	clitest.Git(t, "", "clone", "-q", rem, base)
	clitest.Git(t, base, "branch", "-f", "nightshift", "origin/main")

	// Simulate a prior run: a stage worktree checked out on nightshift.
	stageWT := filepath.Join(tmp, "clone-stage")
	clitest.Git(t, base, "worktree", "add", "-q", stageWT, "nightshift")

	// Move main forward on the remote so a reset is meaningful.
	clitest.WriteFile(t, filepath.Join(seed, "f"), "x\ny\n")
	clitest.Git(t, seed, "commit", "-aqm", "next")
	clitest.Git(t, seed, "push", "-q", "origin", "HEAD:main")
	clitest.Git(t, base, "fetch", "-q", "origin")

	// Verify the pre-condition: branch -f fails while stage holds nightshift.
	cmd := exec.Command("git", "-C", base, "branch", "-f", "nightshift", "origin/main")
	if err := cmd.Run(); err == nil {
		t.Error("repro: expected branch -f to FAIL while stage holds nightshift, but it succeeded")
	}

	// The fix: detach any worktree on nightshift, then reset.
	exec.Command("git", "-C", base, "worktree", "prune").Run()
	nsDetachNightshiftWorktrees(t, base)
	clitest.Git(t, base, "branch", "-f", "nightshift", "origin/main")

	// nightshift == origin/main
	nsHash := nsRevParse(t, base, "nightshift")
	originHash := nsRevParse(t, base, "origin/main")
	if nsHash != originHash {
		t.Errorf("nightshift does not equal origin/main after reset: %q vs %q", nsHash, originHash)
	}

	// The stage worktree must be detached (not on nightshift).
	symref := nsSymref(t, stageWT)
	if symref != "" {
		t.Errorf("stage worktree is not detached; symbolic-ref = %q", symref)
	}

	// ── Go port: setup-nightshift takes the same trap ──
	// Re-arm stage on nightshift, move main forward again.
	clitest.Git(t, stageWT, "checkout", "-q", "nightshift")
	clitest.WriteFile(t, filepath.Join(seed, "f"), "x\ny\nz\n")
	clitest.Git(t, seed, "commit", "-aqm", "third")
	clitest.Git(t, seed, "push", "-q", "origin", "HEAD:main")
	clitest.Git(t, base, "fetch", "-q", "origin")

	h := clitest.New(t)
	h.Project = "test__repo"
	r := h.RunWith(clitest.RunOpts{}, "nightshift", "internal", "setup-nightshift", "--repo", base, "--no-gate")
	if r.Code != 0 {
		t.Fatalf("go setup-nightshift failed (exit %d):\nstdout: %s\nstderr: %s", r.Code, r.Stdout, r.Stderr)
	}

	clitest.Git(t, base, "fetch", "-q", "origin")
	nsHash2 := nsRevParse(t, base, "nightshift")
	originHash2 := nsRevParse(t, base, "origin/main")
	if nsHash2 != originHash2 {
		t.Errorf("go setup-nightshift: nightshift does not equal new origin/main: %s vs %s", nsHash2, originHash2)
	}

	remoteNS := nsLsRemote(t, base, "refs/heads/nightshift")
	if remoteNS != originHash2 {
		t.Errorf("go setup-nightshift: remote nightshift = %q, want %q", remoteNS, originHash2)
	}
}

// nsDetachNightshiftWorktrees detaches any linked worktree whose HEAD branch is nightshift.
func nsDetachNightshiftWorktrees(t *testing.T, base string) {
	t.Helper()
	out, err := exec.Command("git", "-C", base, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return
	}
	var wtPath string
	for _, line := range strings.Split(string(out), "\n") {
		if after, ok := strings.CutPrefix(line, "worktree "); ok {
			wtPath = strings.TrimSpace(after)
		} else if strings.TrimSpace(line) == "branch refs/heads/nightshift" && wtPath != "" && wtPath != base {
			exec.Command("git", "-C", wtPath, "checkout", "-q", "--detach").Run()
			wtPath = ""
		}
	}
}

// nsRevParse runs git rev-parse <ref> in dir and returns the trimmed SHA.
func nsRevParse(t *testing.T, dir, ref string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", ref).Output()
	if err != nil {
		t.Fatalf("git rev-parse %s in %s: %v", ref, dir, err)
	}
	return strings.TrimSpace(string(out))
}

// nsSymref runs git symbolic-ref -q --short HEAD in dir and returns the branch name,
// or "" if HEAD is detached.
func nsSymref(t *testing.T, dir string) string {
	t.Helper()
	out, _ := exec.Command("git", "-C", dir, "symbolic-ref", "-q", "--short", "HEAD").Output()
	return strings.TrimSpace(string(out))
}

// nsLsRemote returns the SHA for a specific ref on origin in dir.
func nsLsRemote(t *testing.T, dir, ref string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "ls-remote", "origin", ref).Output()
	if err != nil {
		t.Fatalf("git ls-remote origin %s: %v", ref, err)
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}
