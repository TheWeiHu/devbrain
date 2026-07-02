package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// One fixture built in TestMain, no network: a bare origin with main +
// feature + conflict branches, and a clone the tests drive.
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
	// feature: a clean branch off main
	mustGit(fxClone, "checkout", "-qb", "todo/0001-feat")
	os.WriteFile(filepath.Join(fxClone, "feat"), []byte("work\n"), 0o644)
	mustGit(fxClone, "add", ".")
	mustGit(fxClone, "commit", "-qm", "feat: 0001")
	mustGit(fxClone, "push", "-q", "origin", "todo/0001-feat")
	// conflict: rewrites f differently than main will
	mustGit(fxClone, "checkout", "-q", "main")
	mustGit(fxClone, "checkout", "-qb", "conflict")
	os.WriteFile(filepath.Join(fxClone, "f"), []byte("theirs\n"), 0o644)
	mustGit(fxClone, "commit", "-aqm", "conflict side")
	// advance main so the conflict is real
	mustGit(fxClone, "checkout", "-q", "main")
	os.WriteFile(filepath.Join(fxClone, "f"), []byte("ours\n"), 0o644)
	mustGit(fxClone, "commit", "-aqm", "main side")
	mustGit(fxClone, "push", "-q", "origin", "main")
	mustGit(fxClone, "fetch", "-q", "origin")
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

func TestRevParseAndBranches(t *testing.T) {
	r := Repo{Dir: fxClone}
	if r.RevParse("main") == "" || r.RevParse("nope-xyz") != "" {
		t.Error("RevParse")
	}
	if r.CurrentBranch() != "main" {
		t.Errorf("CurrentBranch = %q", r.CurrentBranch())
	}
	if !r.IsAncestor("origin/main~1", "origin/main") {
		t.Error("IsAncestor parent→child should hold")
	}
	if r.IsAncestor("origin/todo/0001-feat", "origin/main") {
		t.Error("feature is not an ancestor of main")
	}
	if !r.RemoteBranchExists("todo/0001-feat") || r.RemoteBranchExists("todo/9999-nope") {
		t.Error("RemoteBranchExists")
	}
}

func TestLsRemoteAndLog(t *testing.T) {
	r := Repo{Dir: fxClone}
	heads := r.LsRemoteHeads("todo/*")
	if len(heads) != 1 || heads[0] != "todo/0001-feat" {
		t.Errorf("LsRemoteHeads = %v", heads)
	}
	if r.LsRemoteHeads("zzz/*") != nil {
		t.Error("no match should be nil")
	}
	subs := r.LogSubjects("origin/main")
	if len(subs) < 2 || subs[0] != "main side" {
		t.Errorf("LogSubjects = %v", subs)
	}
	if !r.LogGrepHit("main side", "origin/main") || r.LogGrepHit("no such subject", "origin/main") {
		t.Error("LogGrepHit")
	}
	if r.RevListCount("origin/main~1..origin/main") != 1 {
		t.Error("RevListCount")
	}
	if r.RevListCount("bad..range") != 0 {
		t.Error("RevListCount failure → 0")
	}
}

func TestWorktreesAndReset(t *testing.T) {
	r := Repo{Dir: fxClone}
	wt := filepath.Join(t.TempDir(), "wt")
	if err := r.WorktreeAdd(wt, "origin/main", true); err != nil {
		t.Fatal(err)
	}
	w := Repo{Dir: wt}
	if w.CurrentBranch() != "" {
		t.Error("detached worktree should have no branch")
	}
	// put the worktree ON a branch, find it via WorktreesOn, detach it
	if _, err := w.Run("checkout", "-qb", "held-branch"); err != nil {
		t.Fatal(err)
	}
	on := r.WorktreesOn("held-branch")
	if len(on) != 1 || !strings.HasSuffix(on[0], "/wt") {
		t.Errorf("WorktreesOn = %v", on)
	}
	if err := w.CheckoutDetach(""); err != nil {
		t.Fatal(err)
	}
	if len(r.WorktreesOn("held-branch")) != 0 {
		t.Error("detached worktree still reported on branch")
	}
	// dirty file + untracked file → ResetHard + CleanFD restore pristine
	os.WriteFile(filepath.Join(wt, "f"), []byte("dirty\n"), 0o644)
	os.WriteFile(filepath.Join(wt, "untracked"), []byte("x\n"), 0o644)
	if err := w.ResetHard("origin/main"); err != nil {
		t.Fatal(err)
	}
	w.CleanFD()
	if b, _ := os.ReadFile(filepath.Join(wt, "f")); string(b) != "ours\n" {
		t.Error("ResetHard did not restore f")
	}
	if _, err := os.Stat(filepath.Join(wt, "untracked")); err == nil {
		t.Error("CleanFD left the untracked file")
	}
	if err := w.DeleteBranch("held-branch"); err != nil {
		t.Fatal(err)
	}
	r.Ok("worktree", "remove", "--force", wt)
	r.WorktreePrune()
}

func TestMergeNoFFAndConflict(t *testing.T) {
	r := Repo{Dir: fxClone}
	// clean merge in a scratch worktree branch
	wt := filepath.Join(t.TempDir(), "stage")
	if err := r.WorktreeAdd(wt, "origin/main", true); err != nil {
		t.Fatal(err)
	}
	w := Repo{Dir: wt}
	if _, err := w.Run("checkout", "-qb", "stage-br"); err != nil {
		t.Fatal(err)
	}
	if err := w.MergeNoFF("nightshift: merge todo/0001-feat into nightshift", "origin/todo/0001-feat"); err != nil {
		t.Fatalf("clean merge failed: %v", err)
	}
	subs := w.LogSubjects("stage-br")
	if len(subs) == 0 || subs[0] != "nightshift: merge todo/0001-feat into nightshift" {
		t.Errorf("merge subject = %v", subs)
	}
	// conflicting merge → typed error with the file list, and aborted
	err := w.MergeNoFF("m", "conflict")
	ce, ok := err.(*ConflictError)
	if !ok {
		t.Fatalf("want *ConflictError, got %v", err)
	}
	if len(ce.Files) != 1 || ce.Files[0] != "f" {
		t.Errorf("conflict files = %v", ce.Files)
	}
	if out, _ := w.Run("status", "--porcelain"); out != "" {
		t.Errorf("merge not aborted cleanly: %q", out)
	}
	r.Ok("worktree", "remove", "--force", wt)
	r.Ok("branch", "-qD", "stage-br")
}

func TestPushAndDelete(t *testing.T) {
	r := Repo{Dir: fxClone}
	if _, err := r.Run("branch", "-f", "tmp-push", "origin/main"); err != nil {
		t.Fatal(err)
	}
	if err := r.Push([]string{"DEVBRAIN_GATE_SKIP=1"}, "tmp-push"); err != nil {
		t.Fatal(err)
	}
	if !r.RemoteBranchExists("tmp-push") {
		t.Error("push did not create the remote branch")
	}
	if err := r.PushDelete("tmp-push"); err != nil {
		t.Fatal(err)
	}
	if r.RemoteBranchExists("tmp-push") {
		t.Error("PushDelete left the remote branch")
	}
	if err := r.DeleteBranch("tmp-push"); err != nil {
		t.Fatal(err)
	}
	if err := r.ForceBranch("nightshift", "origin/main"); err != nil {
		t.Fatal(err)
	}
	if err := r.PushForce("nightshift"); err != nil {
		t.Fatal(err)
	}
	if !r.RemoteBranchExists("nightshift") {
		t.Error("PushForce")
	}
	r.PushDelete("nightshift")
	r.DeleteBranch("nightshift")
}
