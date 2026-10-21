// Package gitx is a thin `git -C dir` runner.
package gitx

import (
	"fmt"
	"os"
	"os/exec"
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
