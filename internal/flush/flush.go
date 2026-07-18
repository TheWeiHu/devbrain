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
	"strconv"
	"strings"
	"time"

	"github.com/TheWeiHu/devbrain/internal/config"
	"github.com/TheWeiHu/devbrain/internal/install"
	"github.com/TheWeiHu/devbrain/internal/sweep"
)

// Sweep is the transcript harvest run before each flush; injectable so flush
// tests don't touch real ~/.claude / ~/.codex stores.
var Sweep = func(stdout, stderr io.Writer) { _ = sweep.Run(nil, stdout, stderr) }

// RefreshAgents keeps the preferences inlined in ~/.codex/AGENTS.md tracking
// the page (AGENTS.md has no @import); injectable like Sweep.
var RefreshAgents = func() { install.RefreshAgentsPrefs() }

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

// commitEvery throttles SCHEDULED commits: the flusher ticks (and sweeps)
// every minute so capture lands on disk fast, but each machine's git history
// only takes a commit each 15 minutes — one-commit-per-active-minute was pure
// noise. Manual/skill flushes are never throttled.
//
// The window is measured from a MACHINE-LOCAL stamp, not HEAD's commit time:
// HEAD moves when other machines' commits are pulled (their cadence must not
// starve this machine's), and commit timestamps can sit in the future after
// clock skew (which would freeze the throttle until the skew elapsed).
const commitEvery = 15 * time.Minute

// stampPath is the machine-local record of this machine's last flush commit.
func stampPath() string {
	if d := os.Getenv("DEVBRAIN_FLUSH_STAMP_DIR"); d != "" {
		return filepath.Join(d, "flush-stamp")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "devbrain", "flush-stamp")
}

// sinceLastCommit is the time since this machine's last flush commit — a huge
// value when it has never committed, so the first commit is never deferred.
func sinceLastCommit() time.Duration {
	b, err := os.ReadFile(stampPath())
	if err != nil {
		return time.Duration(1<<62 - 1)
	}
	sec, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil || sec <= 0 {
		return time.Duration(1<<62 - 1)
	}
	age := Now().Sub(time.Unix(sec, 0))
	if age < 0 {
		// A future stamp (clock skew, restored backup) must not freeze the
		// throttle until the skew elapses — treat it as long overdue.
		return time.Duration(1<<62 - 1)
	}
	return age
}

func writeStamp() {
	p := stampPath()
	if os.MkdirAll(filepath.Dir(p), 0o755) == nil {
		_ = os.WriteFile(p, []byte(strconv.FormatInt(Now().Unix(), 10)+"\n"), 0o644)
	}
}

// Run executes one flush. $1 = commit-message reason (default "capture");
// --scheduled marks the flusher's own tick and enables the commit throttle.
func Run(args []string, stdout, stderr io.Writer) int {
	data, err := config.ResolveDataDir()
	if err != nil {
		fmt.Fprintf(stderr, "flush: %v\n", err)
		return 1
	}
	reason := "capture"
	scheduled := false
	for _, a := range args {
		if a == "--scheduled" {
			scheduled = true
		} else if a != "" {
			reason = a
		}
	}
	if fi, err := os.Stat(filepath.Join(data, ".git")); err != nil || !fi.IsDir() {
		fmt.Fprintf(stdout, "no data repo at %s\n", data)
		return 0
	}

	// Capture rides every flush: sweep new agent transcripts into the data
	// repo first so they land in this tick's commit. Fail-open — a sweep
	// problem must never block the durability push.
	Sweep(stdout, stderr)
	// Before the idle-tick early return: a prefs-only edit must still reach
	// the inlined AGENTS.md copy even when the repo has nothing to commit.
	RefreshAgents()

	// Name origin and the branch explicitly (and -u on push): a bare
	// pull/push needs branch.<name>.remote, which history scrubs and
	// remote re-adds silently drop — stranding commits on this machine.
	branch := gitOut(data, "rev-parse", "--abbrev-ref", "HEAD")
	canSync := branch != "" && branch != "HEAD" &&
		gitOut(data, "remote", "get-url", "origin") != ""

	// Idle tick: nothing new locally and nothing stranded — skip the network
	// pull entirely so the one-minute flusher cadence is free when quiet.
	stranded := canSync && pushNeeded(data, branch)
	if gitOut(data, "status", "--porcelain") == "" && !stranded {
		return 0
	}

	// Scheduled tick inside the window: the sweep above already landed the
	// new files on disk (dashboard/gbrain read the working tree); defer the
	// commit until this machine's last one is commitEvery old. Stranded
	// pushes are never deferred, and manual flushes always commit now.
	if scheduled && !stranded && sinceLastCommit() < commitEvery {
		return 0
	}

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
	writeStamp() // manual commits also reset the scheduled window
	if canSync {
		_ = git(data, stdout, stderr, "push", "--quiet", "-u", "origin", branch)
	}
	return 0
}
