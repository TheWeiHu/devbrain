// Package gitx wraps the exact git invocations the nightshift orchestrator
// makes. Thin by design: each helper mirrors one shell call site (flags
// included, -q where the script used it) so the Go port stays auditable
// against scripts/nightshift-orchestrate.sh.
package gitx

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Repo is a git working directory (or bare repo) addressed with `git -C dir`.
type Repo struct{ Dir string }

// Run executes git with the repo's -C prefix and returns trimmed stdout.
// On failure the error carries the trimmed stderr (what `2>&1` would show).
func (r Repo) Run(args ...string) (string, error) {
	return r.RunEnv(nil, args...)
}

// RunEnv is Run with extra KEY=VALUE pairs appended to the environment.
func (r Repo) RunEnv(env []string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", r.Dir}, args...)...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return strings.TrimSpace(out.String()), fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(out.String()), nil
}

// Ok reports whether the git command exits 0 (the `cmd >/dev/null 2>&1` shape).
func (r Repo) Ok(args ...string) bool {
	_, err := r.Run(args...)
	return err == nil
}

// Fetch is `git fetch -q origin` (best-effort at most call sites).
func (r Repo) Fetch() error {
	_, err := r.Run("fetch", "-q", "origin")
	return err
}

// RevParse resolves a ref to a SHA ("" on failure, like `2>/dev/null`).
func (r Repo) RevParse(ref string) string {
	out, err := r.Run("rev-parse", ref)
	if err != nil {
		return ""
	}
	return out
}

// CurrentBranch is `git branch --show-current` ("" when detached/failed).
func (r Repo) CurrentBranch() string {
	out, err := r.Run("branch", "--show-current")
	if err != nil {
		return ""
	}
	return out
}

// IsAncestor is `git merge-base --is-ancestor a b`.
func (r Repo) IsAncestor(a, b string) bool {
	return r.Ok("merge-base", "--is-ancestor", a, b)
}

// RemoteBranchExists is `git ls-remote --exit-code --heads origin branch`.
func (r Repo) RemoteBranchExists(branch string) bool {
	return r.Ok("ls-remote", "--exit-code", "--heads", "origin", branch)
}

// WorktreeAdd is `git worktree add -f [--detach] path ref`.
func (r Repo) WorktreeAdd(path, ref string, detach bool) error {
	args := []string{"worktree", "add", "-f"}
	if detach {
		args = append(args, "--detach")
	}
	args = append(args, path, ref)
	_, err := r.Run(args...)
	return err
}

// WorktreePrune is `git worktree prune` (best-effort in the script).
func (r Repo) WorktreePrune() { r.Ok("worktree", "prune") }

// WorktreesOn lists the worktree paths currently checked out on branch —
// the porcelain/awk pair setup_nightshift uses to find who holds `nightshift`.
func (r Repo) WorktreesOn(branch string) []string {
	out, err := r.Run("worktree", "list", "--porcelain")
	if err != nil {
		return nil
	}
	var paths []string
	cur := ""
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "worktree ") {
			cur = strings.TrimPrefix(line, "worktree ")
		} else if line == "branch refs/heads/"+branch && cur != "" {
			paths = append(paths, cur)
		}
	}
	return paths
}

// CheckoutDetach is `git checkout -q --detach [ref]`.
func (r Repo) CheckoutDetach(ref string) error {
	args := []string{"checkout", "-q", "--detach"}
	if ref != "" {
		args = append(args, ref)
	}
	_, err := r.Run(args...)
	return err
}

// Checkout is `git checkout -q ref`.
func (r Repo) Checkout(ref string) error {
	_, err := r.Run("checkout", "-q", ref)
	return err
}

// ResetHard is `git reset -q --hard ref`.
func (r Repo) ResetHard(ref string) error {
	_, err := r.Run("reset", "-q", "--hard", ref)
	return err
}

// CleanFD is `git clean -qfd` — no -x, so ignored paths survive (the script
// relies on that to keep venv/build dirs listed in info/exclude).
func (r Repo) CleanFD() { r.Ok("clean", "-qfd") }

// DeleteBranch is `git branch -qD branch`.
func (r Repo) DeleteBranch(branch string) error {
	_, err := r.Run("branch", "-qD", branch)
	return err
}

// ForceBranch is `git branch -f name ref` (fails while a worktree holds name).
func (r Repo) ForceBranch(name, ref string) error {
	_, err := r.Run("branch", "-f", name, ref)
	return err
}

// PushDelete is `git push -q origin --delete branch`.
func (r Repo) PushDelete(branch string) error {
	_, err := r.Run("push", "-q", "origin", "--delete", branch)
	return err
}

// ConflictError is a merge that stopped on conflicts; Files lists the
// unmerged paths (`diff --name-only --diff-filter=U`). The merge has been
// aborted by the time it is returned.
type ConflictError struct {
	Files []string
	Err   error
}

func (e *ConflictError) Error() string {
	return "merge conflict in: " + strings.Join(e.Files, " ")
}

// MergeNoFF is `git merge --no-ff -q -m msg ref`. On failure it collects the
// conflicted file list, runs `merge --abort`, and returns a *ConflictError.
func (r Repo) MergeNoFF(msg, ref string) error {
	_, err := r.Run("merge", "--no-ff", "-q", "-m", msg, ref)
	if err == nil {
		return nil
	}
	files, _ := r.Run("diff", "--name-only", "--diff-filter=U")
	r.Ok("merge", "--abort")
	var list []string
	if files != "" {
		list = strings.Split(files, "\n")
	}
	return &ConflictError{Files: list, Err: err}
}

// Push is `git push -q origin refspecs...` with optional extra env
// (e.g. DEVBRAIN_GATE_SKIP=1 to bypass the pre-push hook's re-gate).
func (r Repo) Push(env []string, refspecs ...string) error {
	args := append([]string{"push", "-q", "origin"}, refspecs...)
	_, err := r.RunEnv(env, args...)
	return err
}

// PushForce is `git push -f -q origin refspecs...`.
func (r Repo) PushForce(refspecs ...string) error {
	args := append([]string{"push", "-f", "-q", "origin"}, refspecs...)
	_, err := r.Run(args...)
	return err
}

// LogSubjects returns the commit subjects of `git log --format=%s range`.
func (r Repo) LogSubjects(rng string) []string {
	out, err := r.Run("log", "--format=%s", rng)
	if err != nil || out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// LogGrepHit reports whether a commit whose subject contains needle (fixed
// string) exists on ref — task_in_nightshift's surviving-merge-subject probe.
func (r Repo) LogGrepHit(needle, ref string) bool {
	out, err := r.Run("log", "-n1", "--fixed-strings", "--grep="+needle, "--format=%H", ref)
	return err == nil && out != ""
}

// LsRemoteHeads lists remote branch names matching pattern
// (`git ls-remote --heads origin pattern` with the refs/heads/ prefix cut).
func (r Repo) LsRemoteHeads(pattern string) []string {
	out, err := r.Run("ls-remote", "--heads", "origin", pattern)
	if err != nil || out == "" {
		return nil
	}
	var branches []string
	for _, line := range strings.Split(out, "\n") {
		if i := strings.Index(line, "refs/heads/"); i >= 0 {
			branches = append(branches, line[i+len("refs/heads/"):])
		}
	}
	return branches
}

// RevListCount is `git rev-list --count range` (0 on failure, like `|| echo 0`).
func (r Repo) RevListCount(rng string) int {
	out, err := r.Run("rev-list", "--count", rng)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(out)
	return n
}
