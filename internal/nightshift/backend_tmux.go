package nightshift

// The interactive tmux backend — the FALLBACK worker mode (kept as a hedge
// against a future `claude -p` pricing change). Drives a persistent
// interactive claude in a tmux pane via send-keys, detects turn completion
// with the Stop-hook marker file, and scrapes the pane for state. Everything
// tmux goes through small exec wrappers; pane classification is a pure
// function over the captured text so it unit-tests on fixtures.

import (
	"fmt"
	"hash/crc32"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/TheWeiHu/devbrain/internal/nightshift/plan"
)

// PaneFlags classifies one captured pane. The regexes are the script's own
// (is_idle / handle_prompts / is_stuck_error / usage_limited), one table.
type PaneFlags struct {
	Footer       bool // idle footer present
	MidTurn      bool // "esc to interrupt" — a turn is running
	TrustPrompt  bool // folder-trust dialog
	Menu         bool // an option menu is blocking
	StuckError   bool // API error / overloaded / usage limit
	UsageLimited bool
}

var (
	footerRe = regexp.MustCompile(`bypass permissions|to cycle|\? for shortcuts`)
	midRe    = regexp.MustCompile(`esc to interrupt`)
	trustRe  = regexp.MustCompile(`(?i)trust this folder|trust the (files|authors)|Is this a project you`)
	menuRe   = regexp.MustCompile(`Enter to select|Tab/Arrow keys to navigate`)
	stuckRe  = regexp.MustCompile(`(?i)API Error|Overloaded|\b529\b|usage limit|resets at`)
	limPane  = regexp.MustCompile(`(?i)usage limit|limit reached|resets? (at|in)|approaching .*limit|out of .*credit|quota`)
)

// ClassifyPane is the pure classification over captured pane text.
func ClassifyPane(pane string) PaneFlags {
	return PaneFlags{
		Footer:       footerRe.MatchString(pane),
		MidTurn:      midRe.MatchString(pane),
		TrustPrompt:  trustRe.MatchString(pane),
		Menu:         menuRe.MatchString(pane),
		StuckError:   stuckRe.MatchString(pane),
		UsageLimited: limPane.MatchString(pane),
	}
}

// IsIdle mirrors is_idle: footer present AND not mid-turn.
func (f PaneFlags) IsIdle() bool { return f.Footer && !f.MidTurn }

// tmuxx is the thin shell-out wrapper (tmux stays an external tool).
type tmuxx struct{}

func (tmuxx) run(args ...string) string {
	out, _ := exec.Command("tmux", args...).Output()
	return string(out)
}
func (t tmuxx) hasSession(s string) bool {
	return exec.Command("tmux", "has-session", "-t", s).Run() == nil
}
func (t tmuxx) killSession(s string)    { exec.Command("tmux", "kill-session", "-t", s).Run() }
func (t tmuxx) pane(s string) string    { return t.run("capture-pane", "-t", s, "-p") }
func (t tmuxx) keys(s string, k string) { exec.Command("tmux", "send-keys", "-t", s, k).Run() }
func (t tmuxx) literal(s, text string)  { exec.Command("tmux", "send-keys", "-t", s, "-l", text).Run() }
func (t tmuxx) newSession(s, dir string) {
	exec.Command("tmux", "new-session", "-d", "-s", s, "-c", dir, "-x", "200", "-y", "50").Run()
}

// tmuxWorker is the per-worker interactive state (the script's arrays).
type tmuxWorker struct {
	sess       string
	marker     string
	baseCnt    int
	lastHash   uint32
	hashSet    bool
	lastChg    time.Time
	pending    bool
	promptSent string
}

type tmuxBackend struct {
	r  *Runner
	t  tmuxx
	ws []tmuxWorker
}

func newTmuxBackend(r *Runner) *tmuxBackend {
	return &tmuxBackend{r: r, ws: make([]tmuxWorker, r.Opt.Workers)}
}

func (b *tmuxBackend) hasSession(i int) bool { return b.t.hasSession(fmt.Sprintf("ns-w%d", i)) }

// spawn ports spawn_worker: worktree, fresh session, typed env+launch line,
// bypass-permissions wait with one Ctrl-C retry.
func (b *tmuxBackend) spawn(i int) {
	r := b.r
	wt := r.Opt.WorkerWT(i)
	sess := fmt.Sprintf("ns-w%d", i)
	marker := filepath.Join(wt, ".nightshift", fmt.Sprintf("w%d.turns", i))
	r.Base.Run("worktree", "prune")
	r.Base.Run("fetch", "-q", "origin")
	if !dirExists(wt) {
		r.Base.Run("worktree", "add", "-f", "--detach", wt, "origin/nightshift")
	}
	nsWT := filepath.Join(wt, ".nightshift")
	os.MkdirAll(nsWT, 0o755)
	// Same clean-slate stamp as the headless prepWorktree: the emitter shows a
	// worktree only when its run stamp matches the live run, so tmux workers must
	// be stamped too or their live ns-w* sessions would be hidden from the board.
	os.WriteFile(filepath.Join(nsWT, "run"), []byte(r.runID), 0o644)
	os.WriteFile(filepath.Join(nsWT, "turn.log"), nil, 0o644)
	b.t.killSession(sess)
	time.Sleep(1 * time.Second) // let the killed pane's processes go
	b.t.newSession(sess, wt)
	launch := fmt.Sprintf("claude --dangerously-skip-permissions --disallowedTools AskUserQuestion --append-system-prompt \"$(cat '%s')\"", r.Opt.RulesFile())
	// Wait for the shell to finish starting before typing — sending keystrokes
	// before the prompt is ready mangles the launch.
	time.Sleep(2 * time.Second)
	// The queue env is exported INSIDE the worker's session, deliberately —
	// the orchestrator itself never exports it (the #164/#169 leak class).
	wenv := fmt.Sprintf("export NIGHTSHIFT_MARKER='%s' DEVBRAIN_TODO_DERIVE_GIT=1 DEVBRAIN_TODO_ONLY='%s'", marker, r.Opt.Only)
	if d := workerGbrainDir(false); d != "" { // tmux panes may not inherit the orchestrator PATH
		wenv = fmt.Sprintf("export PATH=%s:\"$PATH\"; ", shSingleQuote(d)) + wenv
	}
	b.t.literal(sess, wenv+"; "+launch)
	b.t.keys(sess, "Enter")
	ok := false
	for range [15]int{} {
		if strings.Contains(b.t.pane(sess), "bypass permissions") {
			ok = true
			break
		}
		time.Sleep(1 * time.Second)
	}
	if !ok {
		b.t.keys(sess, "C-c")
		time.Sleep(1 * time.Second)
		b.t.literal(sess, wenv+"; "+launch)
		b.t.keys(sess, "Enter")
		r.logf("orch: worker %d launch retried (shell was slow to ready)", i)
	}
	r.workers[i] = worker{wt: wt, logPath: filepath.Join(wt, ".nightshift", "turn.log")}
	b.ws[i] = tmuxWorker{sess: sess, marker: marker, lastChg: time.Now()}
	r.logf("orch: spawned worker %d (%s) in %s", i, sess, wt)
}

// mcount is the marker file's line count — the machine turn signal.
func (b *tmuxBackend) mcount(i int) int {
	bts, err := os.ReadFile(b.ws[i].marker)
	if err != nil {
		return 0
	}
	return strings.Count(string(bts), "\n")
}

// sendPrompt ports send_prompt: clear stale menu/input, type, then Enter.
// The 0.5s pause lets the slash menu populate so Enter runs the command.
func (b *tmuxBackend) sendPrompt(sess, text string) {
	b.t.keys(sess, "Escape")
	b.t.keys(sess, "C-u")
	b.t.literal(sess, text)
	time.Sleep(500 * time.Millisecond)
	b.t.keys(sess, "Enter")
}

// handlePrompts auto-clears trust dialogs and menus so nothing blocks.
func (b *tmuxBackend) handlePrompts(i int, flags PaneFlags, pane string) bool {
	sess := b.ws[i].sess
	if flags.TrustPrompt {
		b.t.literal(sess, "1")
		b.t.keys(sess, "Enter")
		return true
	}
	if flags.Menu {
		f, err := os.OpenFile(filepath.Join(b.r.Opt.Repo, ".nightshift", "followups.md"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintf(f, "## menu @ %s [%s]\n%s\n\n", time.Now().UTC().Format("2006-01-02T15:04:05Z"), sess, pane)
			f.Close()
		}
		b.t.keys(sess, "Enter") // take the agent's recommended (highlighted) option
		return true
	}
	return false
}

// usageLimited scans every pane for a real usage limit (not a transient 529).
func (b *tmuxBackend) usageLimited() bool {
	for i := range b.ws {
		if b.ws[i].sess != "" && ClassifyPane(b.t.pane(b.ws[i].sess)).UsageLimited {
			return true
		}
	}
	return false
}

// step runs one poll step for tmux worker i (the main-loop else-branch).
func (b *tmuxBackend) step(i int, assigned *int, oc int) {
	r := b.r
	w := &b.ws[i]
	now := time.Now()
	if !b.t.hasSession(w.sess) {
		r.logf("orch: worker %d session gone — respawning", i)
		b.spawn(i)
		return
	}
	pane := b.t.pane(w.sess)
	flags := ClassifyPane(pane)
	if b.handlePrompts(i, flags, pane) {
		w.lastChg = now
		return // cleared a blocker
	}

	if cur := b.mcount(i); cur > w.baseCnt { // turn finished
		r.turns++
		w.baseCnt = cur
		w.pending = false
		r.logf("orch: worker %d finished a turn (total turns: %d)", i, r.turns)
		// tmux records no fork base — HarvestBranch("" base) takes the merge path
		if r.HarvestBranch(r.workers[i].wt, "") {
			r.noMerge = 0
		} else {
			r.noMerge++
		}
	}

	if flags.IsIdle() {
		if r.stalled || r.noMerge >= r.Opt.StallK {
			w.pending = false
			return // gone quiet → no new work
		}
		if w.pending {
			// sent a prompt the marker hasn't picked up — wait out the grace,
			// then resend (API error or the turn just hasn't started)
			if now.Sub(w.lastChg) < time.Duration(r.Opt.ResendGrace)*time.Second {
				return
			}
			if flags.StuckError {
				r.logf("orch: worker %d hit API/limit — resending", i)
			}
			b.sendPrompt(w.sess, w.promptSent)
			w.lastChg = now
			return
		}
		d := plan.PickTurn(plan.PolicyState{
			Stalled: r.stalled, NoMerge: r.noMerge, StallK: r.Opt.StallK,
			BaseRed: r.baseRed, BRAssigned: *assigned, Open: oc,
			FixedSet: r.Opt.FixedSet,
			Now:      now.Unix(), PlannedLast: plannedEpoch(r.planned), Replan: int64(r.Opt.Replan),
		})
		var prompt string
		switch d.Pick {
		case plan.PickWork:
			*assigned++
			prompt = "/work"
			r.logf("orch: worker %d → /work (open=%d)", i, oc)
		case plan.PickPlan:
			r.planned = now
			prompt = PlanRules(r.Opt.Repo)
			r.logf("orch: worker %d → planning (queue empty — replenish)", i)
		default:
			return
		}
		b.sendPrompt(w.sess, prompt)
		w.promptSent = prompt
		w.pending = true
		w.baseCnt = b.mcount(i)
		w.lastChg = now
		return
	}

	// busy: detect a hang via a frozen pane
	h := crc32.ChecksumIEEE([]byte(pane))
	if w.hashSet && h == w.lastHash {
		if flags.StuckError {
			w.lastChg = now // waiting out API/limit ≠ hang
		} else if now.Sub(w.lastChg) >= time.Duration(r.Opt.Hang)*time.Second {
			r.logf("orch: worker %d HUNG (%ds frozen) — restarting", i, r.Opt.Hang)
			b.t.killSession(w.sess) // kill FIRST, then wipe its branch
			r.ReleaseBranchTask(r.workers[i].wt)
			b.spawn(i)
		}
	} else {
		w.lastHash, w.hashSet, w.lastChg = h, true, now
	}
}
