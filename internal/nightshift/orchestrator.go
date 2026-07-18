package nightshift

// The daemon half of the orchestrator: boot, the coordinator loop, and the
// headless turn machinery. Ports the loop topology of the legacy
// nightshift-orchestrate.sh onto the already-ported core (gate/fence/merge/
// policy). Concurrency model: ONE coordinator goroutine owns all mutable
// state and is the only caller of merge/reconcile/fence/todo mutations —
// the single loop IS the merge lock, as in the shell. Per-turn goroutines
// only cmd.Wait() and post a completion event.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/TheWeiHu/devbrain/internal/jsonedit"
	"github.com/TheWeiHu/devbrain/internal/nightshift/plan"
	"github.com/TheWeiHu/devbrain/internal/procutil"
)

// limitRe spots a usage limit in a finished turn's log (headless replaces
// pane-scraping with log-grepping).
var limitRe = regexp.MustCompile(`(?i)usage limit|limit reached|out of .*credit|quota|resets? (at|in)`)

// worker is the coordinator's view of one worker slot.
type worker struct {
	wt       string // worktree path
	logPath  string
	cancel   context.CancelFunc
	running  bool
	turnBase string // fork SHA recorded at launch (empty-turn detection)
	pid      int
	agent    agentKind
}

// turnDone is posted by a turn goroutine when its agent process exits.
type turnDone struct {
	i        int
	rc       int
	timedOut bool
}

// Runner drives the fleet. Built on Orch (options + git handles).
type Runner struct {
	*Orch
	workers []worker
	desired int // live worker-count target (re-read from desired-workers each tick)
	done    chan turnDone
	turns   int // TURNS_DONE
	noMerge int // NOMERGE
	stalled bool
	baseRed bool
	limit   bool // LIMIT_HIT
	planned time.Time
	// fixed-set completion bookkeeping
	fsReopened map[string]bool
	idleTicks  int  // fixed-set watchdog: consecutive ticks with no worker in flight
	wdRecov    bool // watchdog: a recovery attempt has been spent this idle episode
	cleanupOn  sync.Once
	tmux       *tmuxBackend // nil in headless mode
	runID      string       // this run's identity (orchestrator PID), stamped into each worktree
}

func NewRunner(o *Orch) *Runner {
	return &Runner{Orch: o, done: make(chan turnDone, o.Opt.Workers+1), fsReopened: map[string]bool{}}
}

func (r *Runner) logf(format string, a ...any) {
	fmt.Fprintf(r.Out, format+"\n", a...)
}

// ensureMarkerHook registers the turn-marker Stop hook globally (guarded by
// NIGHTSHIFT_MARKER, so it only fires for workers). Global — NOT
// per-worktree — because /work's stash flows would stash a local settings.
func (r *Runner) ensureMarkerHook() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	set := filepath.Join(home, ".claude", "settings.json")
	if _, err := os.Stat(set); err != nil {
		os.MkdirAll(filepath.Dir(set), 0o755)
		os.WriteFile(set, []byte("{}"), 0o644)
	}
	b, _ := os.ReadFile(set)
	if strings.Contains(string(b), "devbrain-turn-marker") || strings.Contains(string(b), "hook turn-marker") {
		return // already registered (legacy copy or this binary)
	}
	self, err := os.Executable()
	if err != nil {
		return
	}
	if err := jsonedit.RegisterHook(set, "Stop", "", self+" hook turn-marker"); err == nil {
		r.logf("orch: registered turn-marker Stop hook globally")
	}
}

// prepWorktree ensures worker i's worktree exists off origin/nightshift.
func (r *Runner) prepWorktree(i int) {
	wt := r.Opt.WorkerWT(i)
	r.Base.Run("worktree", "prune")
	r.Base.Run("fetch", "-q", "origin")
	if st, err := os.Stat(wt); err != nil || !st.IsDir() {
		r.Base.Run("worktree", "add", "-f", "--detach", wt, "origin/nightshift")
	}
	nsWT := filepath.Join(wt, ".nightshift")
	os.MkdirAll(nsWT, 0o755)
	// Clean slate for the dashboard: stamp the worktree with this run's id (the
	// emitter renders only worktrees whose stamp matches the live run) and clear
	// the previous run's turn log (the pane starts empty until the new turn writes).
	os.WriteFile(filepath.Join(nsWT, "run"), []byte(r.runID), 0o644)
	os.WriteFile(filepath.Join(nsWT, "turn.log"), nil, 0o644)
	agent := r.Opt.AgentFor(i)
	// Stamp the slot's agent so the dashboard can label the card.
	os.WriteFile(filepath.Join(nsWT, "agent"), []byte(agent), 0o644)
	r.workers[i] = worker{wt: wt, logPath: filepath.Join(nsWT, "turn.log"), agent: agent}
	r.logf("orch: worker %d worktree ready (%s) [headless %s]", i, wt, agent)
}

// launchTurn starts one headless turn for worker i with its slot's agent
// (`claude -p` / `codex exec`). Ports run_headless_turn: reset ritual,
// fork-base recording, group kill on timeout, turn.pid for an external
// `nightshift stop`.
func (r *Runner) launchTurn(ctx context.Context, i int, prompt string) {
	w := &r.workers[i]
	wt := w.wt
	g := wtRepo(wt)
	prev, _ := g.Run("branch", "--show-current")
	g.Run("checkout", "-q", "--detach", "origin/nightshift")
	g.Run("reset", "-q", "--hard", "origin/nightshift")
	g.Run("clean", "-qfd")
	if strings.HasPrefix(prev, "todo/") {
		g.Run("branch", "-qD", prev)
	}
	w.turnBase, _ = g.Run("rev-parse", "HEAD")
	os.WriteFile(w.logPath, nil, 0o644)

	rules, _ := os.ReadFile(r.Opt.RulesFile())
	turnCtx, cancel := context.WithTimeout(ctx, time.Duration(r.Opt.TurnMax)*time.Second)
	cmd := exec.CommandContext(turnCtx, w.agent.bin(), w.agent.turnArgs(prompt, string(rules), r.Opt.Model)...)
	cmd.Dir = wt
	cmd.Env = append(prependPATH(os.Environ(), workerGbrainDir(true)),
		"DEVBRAIN_TODO_DERIVE_GIT=1",
		"DEVBRAIN_TODO_ONLY="+r.Opt.Only)
	logF, err := os.OpenFile(w.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err == nil {
		cmd.Stdout, cmd.Stderr = logF, logF
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { // TERM the whole group; WaitDelay KILLs stragglers
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
	cmd.WaitDelay = 15 * time.Second
	if err := cmd.Start(); err != nil {
		if logF != nil {
			logF.Close()
		}
		cancel()
		r.logf("orch: worker %d failed to launch %s: %v", i, w.agent.bin(), err)
		return
	}
	w.cancel = cancel
	w.running = true
	w.pid = cmd.Process.Pid
	os.WriteFile(filepath.Join(wt, ".nightshift", "turn.pid"), []byte(fmt.Sprintf("%d\n", cmd.Process.Pid)), 0o644)
	go func(idx int) {
		err := cmd.Wait()
		if logF != nil {
			logF.Close()
		}
		rc := 0
		if ee, ok := err.(*exec.ExitError); ok {
			rc = ee.ExitCode()
		} else if err != nil {
			rc = 1
		}
		r.done <- turnDone{i: idx, rc: rc, timedOut: turnCtx.Err() == context.DeadlineExceeded}
	}(i)
}

// harvest handles a finished turn for worker i (on the coordinator).
func (r *Runner) harvest(ev turnDone) {
	w := &r.workers[ev.i]
	w.running = false
	if w.cancel != nil {
		w.cancel()
		w.cancel = nil
	}
	os.Remove(filepath.Join(w.wt, ".nightshift", "turn.pid"))
	r.turns++
	r.logf("orch: worker %d finished a turn rc=%d (total turns: %d)", ev.i, ev.rc, r.turns)
	limitHit := false
	if b, err := os.ReadFile(w.logPath); err == nil && limitRe.Match(b) {
		r.limit = true
		limitHit = true
	}
	// A limit-hit turn was cut off, not defeated — counting it as no-progress
	// trips the stall path, which holds every open task and lets the base-fix
	// dedup (it skips held) file a fresh priority-99 blocker each backoff loop.
	noProgress := func() {
		if !limitHit {
			r.noMerge++
		}
	}
	if ev.timedOut {
		r.logf("orch: worker %d turn TIMED OUT after %ds — discarding its branch + releasing its task", ev.i, r.Opt.TurnMax)
		r.ReleaseBranchTask(w.wt)
		noProgress()
		return
	}
	if r.HarvestBranch(w.wt, w.turnBase) {
		r.noMerge = 0
	} else {
		noProgress()
	}
}

// writeBackoff publishes (or clears) the usage-limit pause for the dashboard.
func (r *Runner) writeBackoff(on bool, seconds int) {
	f := r.Opt.BackoffFile()
	if !on {
		os.Remove(f)
		return
	}
	now := time.Now().UTC()
	b, _ := json.Marshal(map[string]any{
		"reason":  "usage limit",
		"since":   now.Format("2006-01-02T15:04:05Z"),
		"until":   now.Add(time.Duration(seconds) * time.Second).Format("2006-01-02T15:04:05Z"),
		"seconds": seconds,
	})
	os.WriteFile(f, b, 0o644)
}

// activeIDs lists the tasks currently owned by a LIVE worker turn.
func (r *Runner) activeIDs() map[string]bool {
	out := map[string]bool{}
	for i := range r.workers {
		alive := r.workers[i].running
		if r.tmux != nil {
			alive = r.tmux.hasSession(i)
		}
		if !alive {
			continue
		}
		g := wtRepo(r.workers[i].wt)
		if b, _ := g.Run("branch", "--show-current"); strings.HasPrefix(b, "todo/") {
			out[strings.TrimPrefix(b, "todo/")] = true
		}
	}
	return out
}

func (r *Runner) openCount() int {
	out, _ := r.todo("list")
	n := 0
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "[") {
			n++
		}
	}
	return n
}

// readDesiredWorkers returns the worker count requested via the control file,
// or 0 when it is absent/unreadable/non-numeric (caller keeps the current target).
func (r *Runner) readDesiredWorkers() int {
	b, err := os.ReadFile(r.Opt.DesiredWorkersFile())
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0
	}
	return n
}

// workerCap is the runtime ceiling on the worker count: addressable work (more
// workers than tasks is pointless), but never below what's already running so a
// cap can't force-drop an in-flight turn. Mirrors ParseOnly's launch-time cap.
// Takes the tick's open/unresolved counts so it doesn't re-shell what Run()
// already computed this poll.
func (r *Runner) workerCap(oc, unresolved int) int {
	running := 0
	for i := range r.workers {
		if r.workers[i].running {
			running++
		}
	}
	cap := oc + running // open + in-flight = work still to do
	if r.Opt.FixedSet {
		cap = unresolved // already counts the set's open+taken+review
	} else if r.Opt.Forever && cap < r.desired {
		// Forever replans, so a momentary empty queue isn't a lack of work — the
		// pending planning turn refills it. Hold the floor at the current target
		// so a transient drain can't collapse the fleet (a user rescale still
		// shrinks: its lower desired-workers value is < r.desired, so it never
		// hits this floor).
		cap = r.desired
	}
	if cap < running {
		cap = running
	}
	if cap < 1 {
		cap = 1
	}
	return cap
}

// resizeWorkers applies a live worker-count change requested via
// .nightshift/desired-workers (re-read each tick, like only.txt). GROWS by
// prepping new worktree slots; SHRINKS by letting each in-flight turn finish,
// then dropping trailing idle slots and clearing their run stamp so the
// dashboard hides them. Clamped to [1, workerCap]. Coordinator-only; headless
// only (tmux sizes its sessions once at spawn).
func (r *Runner) resizeWorkers(oc, unresolved int) {
	if r.tmux != nil {
		return
	}
	want := r.readDesiredWorkers()
	if want <= 0 {
		want = r.desired // no/invalid control file → keep the current target
	}
	if cap := r.workerCap(oc, unresolved); want > cap {
		want = cap
	}
	if want < 1 {
		want = 1
	}
	if want != r.desired {
		r.logf("orch: worker count %d → %d (live rescale)", r.desired, want)
		r.desired = want
	}
	for len(r.workers) < r.desired { // GROW
		i := len(r.workers)
		r.workers = append(r.workers, worker{})
		r.prepWorktree(i)
	}
	for len(r.workers) > r.desired { // SHRINK: only trailing idle slots
		last := len(r.workers) - 1
		if r.workers[last].running {
			break // in-flight turn finishes first; reaped a later tick
		}
		os.Remove(filepath.Join(r.workers[last].wt, ".nightshift", "run")) // hide from dashboard
		r.workers = r.workers[:last]
	}
}

// cleanup reaps in-flight turns + releases their tasks — idempotent, runs on
// every exit path (the shell EXIT/INT/TERM trap).
func (r *Runner) cleanup() {
	r.cleanupOn.Do(func() {
		if r.Opt.FixedSet {
			r.Unfence()
		}
		if r.tmux == nil { // headless only: tmux sessions stay alive for inspection
			r.logf("orch: shutting down — reaping in-flight turns + releasing their claimed tasks")
			for i := range r.workers {
				w := &r.workers[i]
				// Only workers with an UNHARVESTED in-flight turn (running is
				// cleared at harvest — the WTPID discipline from the shell). A
				// harvested worktree can sit on a HELD task's todo/ branch
				// (merge hit the retry cap → held); releasing it would defeat
				// the hold.
				if w.wt == "" || !w.running {
					continue
				}
				if w.pid > 0 {
					procutil.KillGroup(w.pid, syscall.SIGTERM)
					// give the turn's git a beat to exit before touching its worktree
					deadline := time.Now().Add(5 * time.Second)
					for w.running && time.Now().Before(deadline) {
						select {
						case ev := <-r.done:
							r.workers[ev.i].running = false
						case <-time.After(100 * time.Millisecond):
						}
					}
				}
				r.ReleaseBranchTask(w.wt)
				os.Remove(filepath.Join(w.wt, ".nightshift", "turn.pid"))
			}
			// backstop: return every still-`taken` task in scope to `open`
			out, _ := r.todo("list", "taken")
			for _, id := range listIDsLoose(out) {
				if _, err := r.todo("release", id); err == nil {
					r.logf("orch: released stranded claim %s (taken → open on shutdown)", id)
				}
			}
		}
		r.BackfillTokenCost()
	})
}

// Run is the orchestrator entrypoint (`devbrain nightshift run`). It blocks
// until a cap is hit or the process is signaled.
func (r *Runner) Run() int {
	opt := &r.Opt
	nsDir := filepath.Join(opt.Repo, ".nightshift")
	os.MkdirAll(nsDir, 0o755)

	// singleton per repo: O_EXCL pidfile (closes the pgrep race in `start`)
	pidfile := filepath.Join(nsDir, "orchestrator.pid")
	if err := procutil.CreatePidfile(pidfile, os.Getpid()); err != nil {
		r.logf("orch: FATAL — another orchestrator owns %s (%v)", pidfile, err)
		return 1
	}
	defer os.Remove(pidfile)
	// This run's identity — matches what orchestratorPID/RunIdentity resolve to.
	// Stamped into each worktree so the dashboard scopes cards to the live run.
	r.runID = fmt.Sprintf("%d", os.Getpid())

	// workers read the drain rules from a file at launch — NOT inline in the
	// command, so quotes/newlines in the text can't break anything
	os.WriteFile(opt.RulesFile(), []byte(DrainRules(opt.Repo)), 0o644)

	gateLabel := "on"
	if opt.NoGate {
		gateLabel = "off"
	}
	mode := opt.Mode
	extra := fmt.Sprintf(" turn-timeout=%ds", opt.TurnMax)
	if mode == "tmux" {
		extra = fmt.Sprintf(" hang=%ds", opt.Hang)
	}
	r.logf("orch: starting %d workers on %s | mode=%s gate=%s%s", opt.Workers, opt.Repo, mode, gateLabel, extra)
	if mode == "tmux" {
		r.ensureMarkerHook() // the Stop-hook marker is only needed for tmux
	}
	if err := r.SetupNightshift(); err != nil {
		r.logf("orch: FATAL — %v", err)
		return 1
	}
	r.WarnCIScope()
	// Recover first, ALWAYS: release tasks a prior fixed-set run left parked;
	// then fence THIS run's subset before any worker can claim.
	r.Unfence()
	if opt.FixedSet {
		r.Fence()
	}
	// Record (or clear) this run's fixed-set so the status card scopes its queue
	// counts to the launched subset even after Unfence releases the fence on stop.
	r.WriteOnlySet()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sig := make(chan os.Signal, 2)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		cancel()
	}()
	defer r.cleanup()

	r.workers = make([]worker, opt.Workers)
	r.desired = opt.Workers
	// Seed the control file (overwriting any stale value a prior run left) so a
	// leftover count can't silently rescale this run at its first tick.
	os.WriteFile(opt.DesiredWorkersFile(), []byte(strconv.Itoa(opt.Workers)+"\n"), 0o644)
	// Advertise the backend so the dashboard scale API can reject tmux fleets
	// (resizeWorkers is headless-only) instead of accepting a no-op scale.
	os.WriteFile(opt.ModeFile(), []byte(mode+"\n"), 0o644)
	// Advertise the requested worker model (empty = CLI default) for the emitter
	// to surface in status.json / the dashboard. Written only when set, so old or
	// default runs leave no file and read back as the CLI default.
	if opt.Model != "" {
		os.WriteFile(opt.ModelFile(), []byte(opt.Model+"\n"), 0o644)
	} else {
		os.Remove(opt.ModelFile())
	}
	if mode == "tmux" {
		r.tmux = newTmuxBackend(r)
		for i := 0; i < opt.Workers; i++ {
			r.tmux.spawn(i)
		}
		r.logf("orch: workers booting; watch any with: tmux attach -t ns-w0")
	} else {
		for i := 0; i < opt.Workers; i++ {
			r.prepWorktree(i)
		}
	}

	start := time.Now()
	r.Reconcile()
	r.ReclaimStaleClaims(r.activeIDs())
	// Don't build on a red base. A fixed-set fleet can never fix it — fail fast.
	// BaseGate's first return is RED (true = a genuine test failure on the base).
	if red, res := r.BaseGate(); red {
		if opt.FixedSet {
			r.logf("orch: FATAL — base (origin/nightshift) is RED at boot: %s. A fixed-set run can never merge onto a red base — fix the base, then relaunch.", orDefault(res.Detail, "tests failed"))
			return 1
		}
		r.baseRed = true
		r.EnsureBaseFixTask(res.Detail)
	}
	if opt.Forever {
		r.logf("orch: running FOREVER — respawns dead/idle workers, replans every %ds; stop with nightshift stop/Ctrl-C", opt.Replan)
	}

	loops := 0
	for {
		if ctx.Err() != nil {
			break
		}
		now := time.Now()
		if opt.MaxWall > 0 && now.Sub(start) >= time.Duration(opt.MaxWall)*time.Second {
			r.logf("orch: wall-clock cap hit")
			break
		}
		if opt.MaxTurns > 0 && r.turns >= opt.MaxTurns {
			r.logf("orch: max-turns cap hit")
			break
		}

		// drain finished turns first (harvest on tick boundaries)
		drain := true
		for drain {
			select {
			case ev := <-r.done:
				r.harvest(ev)
			default:
				drain = false
			}
		}

		oc := r.openCount()
		unresolved := 0
		if opt.FixedSet {
			unresolved = r.Unresolved()
		}
		if opt.FixedSet && unresolved == 0 {
			missing, ok := r.Verify()
			if ok {
				r.logf("orch: 🌙 fixed-set complete — every selected task merged + verified present on nightshift")
				break
			}
			again := r.ReopenAbsent(missing, r.fsReopened)
			if len(again) > 0 {
				r.logf("orch: ♻ reopened absent done task(s) to regenerate: %s", strings.Join(again, " "))
			} else {
				r.logf("orch: ⚠ fixed-set INCOMPLETE — still absent after regeneration: %s — review + re-seed", strings.Join(missing, " "))
				break
			}
		}
		if r.stalled && oc > 0 {
			r.logf("orch: ▶ resuming — %d open task(s) available", oc)
			r.stalled = false
			r.noMerge = 0
		}
		loops++
		if loops%opt.ReconEvery == 0 {
			r.Reconcile()
			r.ReclaimStaleClaims(r.activeIDs())
			if red, res := r.BaseGate(); !red {
				if r.baseRed {
					r.logf("orch: ✅ nightshift green again — resuming full fleet")
				}
				r.baseRed = false
			} else if opt.FixedSet {
				r.logf("orch: 🩺 nightshift went RED mid-run (%s) — a fixed-set fleet can't fix the base; winding down", orDefault(res.Detail, "tests failed"))
				break
			} else {
				r.baseRed = true
				r.EnsureBaseFixTask(res.Detail)
			}
		}

		// apply any live worker-count change before assigning this tick
		r.resizeWorkers(oc, unresolved)

		// assignment round: one worker per open task; red base funnels to one fixer
		assigned := 0
		for i := range r.workers {
			if r.tmux != nil {
				r.tmux.step(i, &assigned, oc)
				continue
			}
			if i >= r.desired {
				continue // slot retired by a live downscale — don't relaunch it
			}
			if r.workers[i].wt == "" || !dirExists(r.workers[i].wt) {
				r.prepWorktree(i) // re-create a deleted worktree
			}
			if r.workers[i].running {
				continue
			}
			d := plan.PickTurn(plan.PolicyState{
				Stalled: r.stalled, NoMerge: r.noMerge, StallK: opt.StallK,
				BaseRed: r.baseRed, BRAssigned: assigned, Open: oc,
				FixedSet: opt.FixedSet,
				Now:      now.Unix(), PlannedLast: plannedEpoch(r.planned), Replan: int64(opt.Replan),
			})
			switch d.Pick {
			case plan.PickWork:
				assigned++
				r.logf("orch: worker %d → /work (open=%d)", i, oc)
				r.launchTurn(ctx, i, "/work")
			case plan.PickPlan:
				r.planned = now
				r.logf("orch: worker %d → planning (queue empty — replenish)", i)
				r.launchTurn(ctx, i, PlanRules(opt.Repo))
			}
		}

		// convergence: K turns with no new merge while open work remains
		if !r.stalled && r.noMerge >= opt.StallK && oc > 0 {
			held := 0
			out, _ := r.todo("list")
			for _, id := range listIDsLoose(out) {
				if opt.FixedSet && !plan.InOnly(opt.Only, id) {
					continue
				}
				if _, err := r.todo("hold", id, "stalled: no unattended progress — provision deps or release"); err == nil {
					held++
				}
			}
			r.stalled = true
			r.logf("orch: ⚠ STALLED — held %d undoable task(s); going quiet (release one to resume)", held)
		}

		// Fixed-set watchdog. A fixed-set run must terminate; if it can't reach the
		// clean Verify-exit (a selected task wedged in a state the fleet can't act
		// on, a lost harvest leaving nothing in flight, Verify oscillating) it
		// otherwise spins forever running only the periodic BaseGate — a silent
		// running:true zombie with flat-zero tokens and no live workers. Try one
		// escalated recovery first; if still wedged, break so cleanup() releases
		// claims + removes the pidfile and the dashboard's live indicator clears.
		wedged := false
		switch r.watchdogCheck(opt.FixedSet, r.anyRunning()) {
		case wdRecover:
			r.watchdogRecover()
		case wdExit:
			r.logf("orch: 🛑 WATCHDOG — still wedged after a recovery attempt (unresolved=%d); exiting so the run stops reporting itself live", unresolved)
			wedged = true
		}
		if wedged {
			break
		}

		// pacing: back off on a usage limit; otherwise the normal poll
		delay := time.Duration(opt.Poll) * time.Second
		backingOff := false
		if r.tmux == nil && r.limit {
			r.logf("orch: ⏳ usage limit hit — backing off %ds before the next turn", opt.LimitBackoff)
			r.limit = false
			delay = time.Duration(opt.LimitBackoff) * time.Second
			backingOff = true
		} else if r.tmux != nil && r.tmux.usageLimited() {
			r.logf("orch: ⏳ usage limit detected — backing off %ds (ping ~every 5 min until reset)", opt.LimitBackoff)
			delay = time.Duration(opt.LimitBackoff) * time.Second
			backingOff = true
		}
		r.writeBackoff(backingOff, opt.LimitBackoff)
		select {
		case <-ctx.Done():
		case ev := <-r.done: // a turn finishing wakes the loop early
			r.harvest(ev)
		case <-time.After(delay):
		}
	}

	r.cleanup()
	r.logf("orch: done. turns=%d open=%d tasks left.", r.turns, r.openCount())
	r.logf("orch: REVIEW WHAT LANDED →  git -C %s diff %s...nightshift   (then merge nightshift → %s)", r.Opt.StageWT(), r.Opt.BaseBranch, r.Opt.BaseBranch)
	if r.tmux != nil {
		r.logf("orch: worker sessions left alive: ns-w0 .. ns-w%d", opt.Workers-1)
	}
	return 0
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// fixedSetWatchdogTicks is how many consecutive poll ticks a fixed-set run may
// sit with NO worker in flight before the watchdog acts. During an idle stretch
// no turn finishes, so the loop only wakes on the poll timer — at the default
// 15s poll that's ~5 min. (It scales with --poll; that's fine, it's a coarse
// "clearly wedged" threshold, not a precise deadline.)
const fixedSetWatchdogTicks = 20

// anyRunning reports whether any worker slot has an in-flight turn. Mirrors
// activeIDs's liveness test: in tmux mode `worker.running` is never set, so
// aliveness comes from the live session — without this the watchdog would see a
// busy tmux fleet as idle and fire mid-work.
func (r *Runner) anyRunning() bool {
	for i := range r.workers {
		if r.tmux != nil {
			if r.tmux.hasSession(i) {
				return true
			}
		} else if r.workers[i].running {
			return true
		}
	}
	return false
}

// wdAction is what the watchdog wants the loop to do this tick.
type wdAction int

const (
	wdNone    wdAction = iota // healthy, or still counting down
	wdRecover                 // first trip: run one escalated self-heal, then retry
	wdExit                    // tripped again after a recovery attempt: give up cleanly
)

// watchdogCheck counts consecutive ticks with NO worker in flight — the
// signature of a wedged run that can't reach its clean exit (during a healthy
// drain a worker is always running or relaunched same-tick). Once the count
// reaches fixedSetWatchdogTicks it asks for ONE escalated recovery (wdRecover)
// and resets; if the run is still idle a full count later it gives up (wdExit).
// Any worker activity resets both the counter and the one-shot, so a recovered
// fleet gets a fresh attempt if it wedges again. Fixed-set only — forever mode
// idles on purpose when STALLED (going quiet until a human releases a task).
func (r *Runner) watchdogCheck(fixedSet, anyRunning bool) wdAction {
	if !fixedSet || anyRunning {
		r.idleTicks, r.wdRecov = 0, false
		return wdNone
	}
	r.idleTicks++
	if r.idleTicks < fixedSetWatchdogTicks {
		return wdNone
	}
	if !r.wdRecov {
		r.wdRecov, r.idleTicks = true, 0
		return wdRecover
	}
	return wdExit
}

// watchdogRecover is the one escalated self-heal the watchdog spends before it
// gives up: adopt stray pushed branches, reclaim stale claims, and — since no
// worker is in flight when the watchdog fires — return every still-`taken` task
// to `open` so a worker re-picks it next tick. If this frees real work the fleet
// resumes (which re-arms the one-shot); if it's still wedged a countdown later,
// the loop exits.
func (r *Runner) watchdogRecover() {
	r.logf("orch: 🔧 WATCHDOG — no worker in flight for %d ticks; one recovery attempt (reconcile + reclaim + release stranded claims), then retry", fixedSetWatchdogTicks)
	active := r.activeIDs()
	r.Reconcile()
	r.ReclaimStaleClaims(active)
	out, _ := r.todo("list", "taken")
	for _, id := range listIDsLoose(out) {
		if active[id] {
			continue // a live turn (e.g. a tmux session) owns it — don't yank it
		}
		if _, err := r.todo("release", id); err == nil {
			r.logf("orch: released stranded claim %s (taken → open, watchdog recovery)", id)
		}
	}
}

// plannedEpoch maps the zero time to 0 so the first planning turn always
// fires (the bash counter starts at 0).
func plannedEpoch(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}
