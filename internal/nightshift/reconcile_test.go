package nightshift_test

// Port of scripts/test-nightshift-reconcile.sh

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TheWeiHu/devbrain/internal/clitest"
)

func TestNightshiftReconcile(t *testing.T) {
	h := clitest.New(t)

	binDir := filepath.Join(h.Data, "bin")
	clitest.WriteExec(t, filepath.Join(binDir, "claude"), "#!/usr/bin/env bash\nexit 0\n")
	h.Env["PATH"] = binDir + string(os.PathListSeparator) + os.Getenv("PATH")
	h.Env["GIT_AUTHOR_NAME"] = "t"
	h.Env["GIT_AUTHOR_EMAIL"] = "t@t"
	h.Env["GIT_COMMITTER_NAME"] = "t"
	h.Env["GIT_COMMITTER_EMAIL"] = "t@t"

	origin := filepath.Join(h.Data, "origin.git")
	base := filepath.Join(h.Data, "repo")
	clitest.Git(t, "", "init", "-q", "--bare", origin)
	clitest.Git(t, "", "init", "-q", base)
	clitest.Git(t, base, "remote", "add", "origin", origin)
	clitest.Git(t, base, "commit", "--allow-empty", "-qm", "init")

	// A nightshift branch carrying a merge commit that names the merged task's branch.
	// The task's todo/* branch is intentionally NEVER pushed — pure branchless orphan.
	clitest.Git(t, base, "checkout", "-q", "-b", "nightshift")
	clitest.Git(t, base, "commit", "--allow-empty", "-qm",
		"nightshift: merge todo/0010-merged into nightshift")
	clitest.Git(t, base, "push", "-q", "origin", "nightshift")
	clitest.Git(t, base, "fetch", "-q", "origin")

	h.Project = "test__repo"
	td := filepath.Join(h.Data, "projects", "test__repo", "todo")
	if err := os.MkdirAll(td, 0o755); err != nil {
		t.Fatal(err)
	}

	writeTask := func(id, status, title string) {
		clitest.WriteFile(t, filepath.Join(td, id+".md"),
			"---\nid: "+id+"\nstatus: "+status+"\npriority: 50\n"+
				"created: 2026-06-25T00:00:00Z\nclaimed_by:\nclaimed_at:\npr:\n---\n# "+title+"\n")
	}
	writeTask("0010-merged", "review", "work landed in nightshift but status stuck at review")
	writeTask("0011-pending", "review", "PR still open — branch genuinely awaiting merge")
	writeTask("0012-landed", "open", "remote todo branch already landed in nightshift")

	// Push a todo/0012-landed branch pointing at origin/nightshift.
	clitest.Git(t, base, "branch", "-qf", "todo/0012-landed", "origin/nightshift")
	clitest.Git(t, base, "push", "-q", "origin", "todo/0012-landed")

	ns := func(args ...string) clitest.Result {
		full := append([]string{"nightshift", "internal"}, args...)
		full = append(full, "--repo", base)
		return h.RunWith(clitest.RunOpts{Dir: base}, full...)
	}

	nsTaskStatus := func(id string) string {
		t.Helper()
		r := h.RunWith(clitest.RunOpts{Dir: base}, "todo", "show", id)
		for _, ln := range strings.Split(r.Stdout, "\n") {
			if v, ok := strings.CutPrefix(ln, "status:"); ok {
				return strings.TrimSpace(v)
			}
		}
		return ""
	}

	// Before reconcile: state check.
	if got := nsTaskStatus("0010-merged"); got != "review" {
		t.Errorf("before reconcile: orphan status = %q, want review", got)
	}
	if got := nsTaskStatus("0012-landed"); got != "open" {
		t.Errorf("before reconcile: landed live branch status = %q, want open", got)
	}

	// reconcile-task closes a live branch already in nightshift.
	ns("reconcile-task", "0012-landed")
	if got := nsTaskStatus("0012-landed"); got != "done" {
		t.Errorf("reconcile-task: status = %q, want done", got)
	}

	// reconcile-task prunes the spent remote branch.
	lsOut, _ := exec.Command("git", "-C", base, "ls-remote", "--heads", "origin", "todo/0012-landed").Output()
	if strings.TrimSpace(string(lsOut)) != "" {
		t.Error("reconcile-task: remote todo/0012-landed branch should have been pruned")
	}

	// reconcile closes the landed branchless orphan.
	ns("reconcile")
	if got := nsTaskStatus("0010-merged"); got != "done" {
		t.Errorf("reconcile: orphan status = %q, want done", got)
	}

	// reconcile leaves a genuinely-pending review.
	if got := nsTaskStatus("0011-pending"); got != "review" {
		t.Errorf("reconcile: pending review status = %q, want review", got)
	}

	// With the orphan closed, fixed-set wind-down no longer waits on it forever.
	r := ns("unresolved", "--only", "0010-merged,0011-pending")
	if strings.TrimSpace(r.Stdout) != "1" {
		t.Errorf("wind-down counts only the pending review: unresolved = %q, want 1", r.Stdout)
	}

	// Idempotent: re-running heals nothing new and errors on nothing.
	ns("reconcile")
	if got := nsTaskStatus("0010-merged"); got != "done" {
		t.Errorf("reconcile idempotent: orphan status = %q, want done", got)
	}
}
