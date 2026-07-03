package nightshift_test

// Integration coverage for the green-gate → serial-merge stage of the core loop:
// a GREEN branch lands through the gate onto nightshift; a RED branch is BLOCKED
// and requeued with a last_failure for the next worker. That safety + feedback
// property is the loop's whole point, and TestHeadlessTurnEndToEnd exercises the
// drain→spawn→merge path only with --no-gate — so the gate stage was untested.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TheWeiHu/devbrain/internal/clitest"
)

func TestNightshiftGateMerge(t *testing.T) {
	h := clitest.New(t)
	h.Project = "test__repo"

	binDir := filepath.Join(h.Data, "bin")
	clitest.WriteExec(t, filepath.Join(binDir, "claude"), "#!/usr/bin/env bash\nexit 0\n")
	h.Env["PATH"] = binDir + string(os.PathListSeparator) + os.Getenv("PATH")

	// bare "remote" with main, plus a clone standing in for the base checkout.
	rem := filepath.Join(h.Data, "rem.git")
	seed := filepath.Join(h.Data, "seed")
	base := filepath.Join(h.Data, "repo")
	clitest.Git(t, "", "init", "-q", "--bare", rem)
	clitest.Git(t, "", "clone", "-q", rem, seed)
	clitest.WriteFile(t, filepath.Join(seed, "f"), "base\n")
	clitest.Git(t, seed, "add", "f")
	clitest.Git(t, seed, "commit", "-qm", "init")
	clitest.Git(t, seed, "push", "-q", "origin", "HEAD:main")
	clitest.Git(t, "", "clone", "-q", rem, base)

	td := filepath.Join(h.Data, "projects", "test__repo", "todo")
	if err := os.MkdirAll(td, 0o755); err != nil {
		t.Fatal(err)
	}
	// tasks a worker already claimed + pushed a branch for.
	mkTask := func(id, title string) {
		clitest.WriteFile(t, filepath.Join(td, id+".md"),
			"---\nid: "+id+"\nstatus: taken\npriority: 50\ncreated: 2026-06-25T00:00:00Z\n"+
				"claimed_by: w\nclaimed_at: 2026-06-25T00:00:00Z\npr:\nlast_failure:\n---\n# "+title+"\n")
	}
	mkTask("0001-green", "Green task")
	mkTask("0002-red", "Red task")

	ns := func(args ...string) clitest.Result {
		full := append([]string{"nightshift", "internal"}, args...)
		full = append(full, "--repo", base)
		return h.RunWith(clitest.RunOpts{Dir: base}, full...)
	}
	show := func(id, field string) string {
		t.Helper()
		r := h.RunWith(clitest.RunOpts{Dir: base}, "todo", "show", id)
		return clitest.Field(r.Stdout, field)
	}

	// Build origin/nightshift + the $BASE-stage worktree the merge runs in.
	// --test-cmd pins a trivial gate so setup skips the pytest venv build.
	if r := ns("setup-nightshift", "--test-cmd", "true"); r.Code != 0 {
		t.Fatalf("setup-nightshift failed (exit %d):\n%s\n%s", r.Code, r.Stdout, r.Stderr)
	}

	// pushTodoBranch forks todo/<id> off the CURRENT origin/nightshift, commits a
	// real change, and pushes it — a worker's finished turn.
	pushTodoBranch := func(id, file string) {
		t.Helper()
		clitest.Git(t, base, "fetch", "-q", "origin")
		clitest.Git(t, base, "checkout", "-q", "-B", "todo/"+id, "origin/nightshift")
		clitest.WriteFile(t, filepath.Join(base, file), id+"\n")
		clitest.Git(t, base, "add", file)
		clitest.Git(t, base, "commit", "-qm", "work "+id)
		clitest.Git(t, base, "push", "-q", "-f", "origin", "todo/"+id)
		clitest.Git(t, base, "checkout", "-q", "--detach") // free the branch for merge/prune
	}

	// ── green branch passes the gate → lands on nightshift, task done ──
	pushTodoBranch("0001-green", "green.txt")
	if r := ns("merge", "todo/0001-green", "0001-green", "--test-cmd", "true"); r.Code != 0 {
		t.Fatalf("green merge rc=%d, want 0 (MergeNew):\n%s\n%s", r.Code, r.Stdout, r.Stderr)
	}
	clitest.Git(t, base, "fetch", "-q", "origin")
	if subj := nsLogSubjects(t, base, "origin/nightshift"); !strings.Contains(subj, "nightshift: merge todo/0001-green into nightshift") {
		t.Errorf("green work not on nightshift with the frozen subject:\n%s", subj)
	}
	if st := show("0001-green", "status"); st != "done" {
		t.Errorf("green task status = %q, want done", st)
	}
	if nsLsRemote(t, base, "refs/heads/todo/0001-green") != "" {
		t.Error("merged todo/0001-green branch should have been pruned from origin")
	}

	// ── red branch fails the gate → BLOCKED from nightshift, requeued ──
	pushTodoBranch("0002-red", "red.txt")
	preRed := nsRevParse(t, base, "origin/nightshift")
	if r := ns("merge", "todo/0002-red", "0002-red", "--test-cmd", "exit 1"); r.Code != 1 {
		t.Fatalf("red merge rc=%d, want 1 (MergeFailed):\n%s\n%s", r.Code, r.Stdout, r.Stderr)
	}
	clitest.Git(t, base, "fetch", "-q", "origin")
	if post := nsRevParse(t, base, "origin/nightshift"); post != preRed {
		t.Errorf("a red branch must NOT move origin/nightshift: %s -> %s", preRed, post)
	}
	if nsIsAncestor(t, base, "origin/todo/0002-red", "origin/nightshift") {
		t.Error("red branch must not be an ancestor of nightshift (it failed the gate)")
	}
	// requeued: released back to open with a last_failure the next worker reads.
	if st := show("0002-red", "status"); st != "open" {
		t.Errorf("failed-gate task status = %q, want open (requeued)", st)
	}
	if lf := show("0002-red", "last_failure"); !strings.Contains(lf, "attempt 1") || !strings.Contains(lf, "gate") {
		t.Errorf("failed-gate task last_failure = %q, want an attempt-1 gate note", lf)
	}
	if nsLsRemote(t, base, "refs/heads/todo/0002-red") == "" {
		t.Error("unmerged todo/0002-red branch must survive on origin for the retry")
	}
}

// nsLogSubjects returns the newline-joined commit subjects of ref in dir.
func nsLogSubjects(t *testing.T, dir, ref string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "log", "--format=%s", ref).Output()
	if err != nil {
		t.Fatalf("git log %s in %s: %v", ref, dir, err)
	}
	return string(out)
}

// nsIsAncestor reports whether a is an ancestor of b (git merge-base --is-ancestor).
func nsIsAncestor(t *testing.T, dir, a, b string) bool {
	t.Helper()
	return exec.Command("git", "-C", dir, "merge-base", "--is-ancestor", a, b).Run() == nil
}
