// Package flush durably pushes the data repo off-machine. It commits local
// source-of-truth changes before rebasing, then pushes if a remote is set.
package flush

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/TheWeiHu/devbrain/internal/config"
)

// Now is the injectable clock for the commit-message timestamp.
var Now = func() time.Time { return time.Now() }

// git runs git -C data with the given stdio; returns the error (callers
// mostly ignore it — the script has no set -e).
func git(data string, stdout, stderr io.Writer, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = data
	cmd.Stdout, cmd.Stderr = stdout, stderr
	return cmd.Run()
}

// gitOut captures trimmed stdout ("" on failure), stderr discarded.
func gitOut(data string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = data
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(out), "\n")
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

type flushLock struct{ file *os.File }

var errFlushLocked = errors.New("flush already running")

func acquireLock(data string) (*flushLock, error) {
	gitDir := gitOut(data, "rev-parse", "--git-dir")
	if gitDir == "" {
		return nil, fmt.Errorf("cannot resolve git directory")
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(data, gitDir)
	}
	f, err := os.OpenFile(filepath.Join(gitDir, "devbrain-flush.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errFlushLocked
		}
		return nil, fmt.Errorf("lock data repo: %w", err)
	}
	return &flushLock{file: f}, nil
}

func (l *flushLock) close() {
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	_ = l.file.Close()
}

// pushNeeded reports local commits origin lacks — or no origin/<branch>
// tracking ref at all (a scrub deletes it and an offline pull can't restore
// it), where only attempting the push can tell.
func pushNeeded(data, branch string) bool {
	if gitOut(data, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+branch) == "" {
		return true
	}
	return gitOut(data, "rev-list", "-1", "origin/"+branch+".."+branch) != ""
}

// Run executes one flush. $1 = commit-message reason (default "capture").
func Run(args []string, stdout, stderr io.Writer) int {
	data, err := config.ResolveDataDir()
	if err != nil {
		fmt.Fprintf(stderr, "devbrain flush: %v\n", err)
		return 1
	}
	reason := "capture"
	if len(args) > 0 && args[0] != "" {
		reason = args[0]
	}
	if fi, err := os.Stat(filepath.Join(data, ".git")); err != nil || !fi.IsDir() {
		fmt.Fprintf(stdout, "no data repo at %s\n", data)
		return 0
	}
	lock, err := acquireLock(data)
	if err != nil {
		if errors.Is(err, errFlushLocked) {
			fmt.Fprintln(stdout, "another devbrain flush is already running")
			return 0
		}
		fmt.Fprintf(stderr, "devbrain flush: %v\n", err)
		return 1
	}
	defer lock.close()

	if unmerged := gitOut(data, "diff", "--name-only", "--diff-filter=U"); unmerged != "" {
		fmt.Fprintf(stderr, "devbrain flush: data repo has unresolved merge conflicts; refusing to commit\n%s\n", unmerged)
		return 1
	}

	// Name origin and the branch explicitly (and -u on push): a bare
	// pull/push needs branch.<name>.remote, which history scrubs and
	// remote re-adds silently drop — stranding commits on this machine.
	branch := gitOut(data, "rev-parse", "--abbrev-ref", "HEAD")
	canSync := branch != "" && branch != "HEAD" &&
		gitOut(data, "remote", "get-url", "origin") != ""

	// Commit first. Pulling a dirty tree with --autostash can fail while applying
	// the stash after a successful rebase, leaving conflict markers in ordinary
	// files with no rebase directory. A durable local commit is also recoverable
	// when the network is unavailable.
	if gitOut(data, "status", "--porcelain") != "" {
		if git(data, stdout, stderr, "add", "-A") != nil {
			fmt.Fprintln(stderr, "devbrain flush: could not stage data repo changes")
			return 1
		}
		if git(data, io.Discard, io.Discard, "diff", "--cached", "--quiet") != nil {
			name := os.Getenv("DEVBRAIN_GIT_NAME")
			if name == "" {
				name = gitOut(data, "config", "user.name")
			}
			if name == "" {
				name = "devbrain"
			}
			email := os.Getenv("DEVBRAIN_GIT_EMAIL")
			if email == "" {
				email = gitOut(data, "config", "user.email")
			}
			if email == "" {
				email = "devbrain@localhost"
			}
			host := "host"
			if h, err := os.Hostname(); err == nil && h != "" {
				host = strings.SplitN(h, ".", 2)[0]
			}
			msg := fmt.Sprintf("%s: %s on %s", reason, Now().Format("2006-01-02 15:04:05 -0700"), host)
			if git(data, stdout, stderr, "-c", "user.name="+name, "-c", "user.email="+email,
				"commit", "--quiet", "-m", msg) != nil {
				fmt.Fprintln(stderr, "devbrain flush: could not commit data repo changes")
				return 1
			}
		}
	}

	if canSync {
		if err := git(data, stdout, stderr, "pull", "--rebase", "--quiet", "origin", branch); err != nil {
			if dirExists(filepath.Join(data, ".git", "rebase-merge")) ||
				dirExists(filepath.Join(data, ".git", "rebase-apply")) {
				_ = git(data, io.Discard, io.Discard, "rebase", "--abort")
			}
			fmt.Fprintln(stderr, "devbrain flush: pull/rebase failed; local changes remain committed for retry")
			return 1
		}
	}
	if canSync {
		if pushNeeded(data, branch) {
			if err := git(data, stdout, stderr, "push", "--quiet", "-u", "origin", branch); err != nil {
				fmt.Fprintln(stderr, "devbrain flush: push failed; local commits remain for retry")
				return 1
			}
		}
	}
	return 0
}
