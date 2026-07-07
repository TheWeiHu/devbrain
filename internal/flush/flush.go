// Package flush ports scripts/flush.sh: durably push the data repo
// off-machine. Pull-rebase first, commit anything new under an impersonal
// identity, push if a remote is set. Fails open — always exits 0.
package flush

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	data := config.DataDir()
	reason := "capture"
	if len(args) > 0 && args[0] != "" {
		reason = args[0]
	}
	if fi, err := os.Stat(filepath.Join(data, ".git")); err != nil || !fi.IsDir() {
		fmt.Fprintf(stdout, "no data repo at %s\n", data)
		return 0
	}

	// Name origin and the branch explicitly (and -u on push): a bare
	// pull/push needs branch.<name>.remote, which history scrubs and
	// remote re-adds silently drop — stranding commits on this machine.
	branch := gitOut(data, "rev-parse", "--abbrev-ref", "HEAD")
	canSync := branch != "" && branch != "HEAD" &&
		gitOut(data, "remote", "get-url", "origin") != ""

	// Pull first so the local commit lands on top of any other machine's pushes.
	if canSync {
		_ = git(data, stdout, stderr, "pull", "--rebase", "--autostash", "--quiet", "origin", branch)
		// A conflicted pull leaves a rebase in progress; the add -A below
		// would commit conflict markers. Abort and retry on a later flush.
		if dirExists(filepath.Join(data, ".git", "rebase-merge")) ||
			dirExists(filepath.Join(data, ".git", "rebase-apply")) {
			_ = git(data, stdout, stderr, "rebase", "--abort")
			return 0
		}
	}

	// Nothing to do?
	if gitOut(data, "status", "--porcelain") == "" {
		// Re-push commits stranded by an earlier failed push.
		if canSync && pushNeeded(data, branch) {
			_ = git(data, stdout, stderr, "push", "--quiet", "-u", "origin", branch)
		}
		return 0
	}
	_ = git(data, stdout, stderr, "add", "-A")
	if git(data, io.Discard, io.Discard, "diff", "--cached", "--quiet") == nil {
		return 0 // nothing staged after add
	}

	// Commit identity: env override → repo's git config → impersonal default.
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
		host = strings.SplitN(h, ".", 2)[0] // hostname -s
	}
	msg := fmt.Sprintf("%s: %s on %s", reason, Now().Format("2006-01-02 15:04:05 -0700"), host)

	if git(data, stdout, stderr, "-c", "user.name="+name, "-c", "user.email="+email,
		"commit", "--quiet", "-m", msg) != nil {
		return 0
	}
	if canSync {
		_ = git(data, stdout, stderr, "push", "--quiet", "-u", "origin", branch)
	}
	return 0
}
