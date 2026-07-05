package nightshift

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/TheWeiHu/devbrain/internal/config"
	"github.com/TheWeiHu/devbrain/internal/gitx"
	"github.com/TheWeiHu/devbrain/internal/nightshift/plan"
)

// merge.go — nightshift setup, the serialized automerge, requeue/retry
// accounting, reconcile (git-truth self-heal), stale-claim reclaim, and the
// shutdown cleanup. Faithful port of the bash call sites; each function
// carries the script's own rationale.

// Merge return codes (merge_to_nightshift): 0 NEW merge · 2 already-in-
// nightshift (no-op) · 1 conflict/fail/not-pushed.
const (
	MergeNew     = 0
	MergeFailed  = 1
	MergeAlready = 2
)

func (o *Orch) taskHasRemoteBranch(id string) bool {
	return o.Base.RemoteBranchExists("todo/" + id)
}

// taskInNightshift: branch ancestry or the surviving merge subject says the
// task's work landed (the subject survives the branch being deleted).
func (o *Orch) taskInNightshift(id string) bool {
	return o.Base.IsAncestor("origin/todo/"+id, "origin/nightshift") ||
		o.Base.LogGrepHit("merge todo/"+id+" ", "origin/nightshift")
}

// DropSpentBranch deletes a merged todo/<id> branch (origin copy + any local
// ref) so todo/* branches don't accumulate on every turn. Best-effort.
func (o *Orch) DropSpentBranch(branch string) {
	o.Base.PushDelete(branch)   // the pushed copy ls-remote sees + what piles up
	o.Base.DeleteBranch(branch) // local copy, if not checked out anywhere
}

// retryCount reads retries/<id> (0 when absent, like `cat … || echo 0`).
func (o *Orch) retryCount(id string) int {
	b, err := os.ReadFile(filepath.Join(o.Opt.RetryDir(), id))
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return n
}

// Requeue releases a failed task back to open, or PARKS it for the human
// after Retries attempts. The note is feedback the next worker reads.
func (o *Orch) Requeue(id, why string) {
	if why == "" {
		why = "could not merge"
	}
	n := o.retryCount(id) + 1
	os.MkdirAll(o.Opt.RetryDir(), 0o755)
	os.WriteFile(filepath.Join(o.Opt.RetryDir(), id), []byte(fmt.Sprintf("%d\n", n)), 0o644)
	o.todo("note", id, fmt.Sprintf("attempt %d — %s", n, why))
	// Release only while attempts REMAIN (n < Retries); hold on the final attempt
	// (n == Retries). `n <= Retries` released even on the last try, so a
	// retry-exhausted task went back to open and a fixed-set run never wound down.
	if n < o.Opt.Retries {
		o.todo("release", id)
		fmt.Fprintf(o.Out, "  requeued %s (attempt %d/%d): %s\n", id, n, o.Opt.Retries, why)
	} else {
		o.todo("hold", id, fmt.Sprintf("%s (after %d attempts)", why, n))
		fmt.Fprintf(o.Out, "  ⚠ %s held after %d attempts — %s (needs you)\n", id, n, why)
	}
}

// MergeToNightshift lands branch (todo/<id>) onto nightshift through the
// staging worktree: gate, merge --no-ff with the EXACT subject
// "nightshift: merge todo/<id> into nightshift", push with DEVBRAIN_GATE_SKIP=1
// (run_gate already gated; skip the pre-push hook's re-run). Serialized by
// construction — only the single orchestrator loop calls it.
func (o *Orch) MergeToNightshift(branch, id string) int {
	// Test seam: reconcile-path tests record which branches WOULD merge
	// without needing a full stage/gate fixture (mirrors the bash tests'
	// merge_to_nightshift stub).
	if log := os.Getenv("NIGHTSHIFT_TEST_MERGE_LOG"); log != "" {
		if f, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintln(f, strings.TrimPrefix(branch, "todo/"))
			f.Close()
		}
		return MergeNew
	}
	o.Base.Fetch()
	// A worker can LAND a failed-merge fix itself. Detect that FIRST — before
	// the not-pushed requeue — then confirm the close and never re-merge.
	// branch-is-ancestor is the verified truth; a worker-set `done` is the
	// explicit signal. Also covers a stale branch already in nightshift from a
	// no-op turn.
	if o.Base.IsAncestor("origin/"+branch, "origin/nightshift") || o.taskStatus(id) == "done" {
		o.RecordLanded(id) // work is on origin/nightshift now → stamp the landing SHA
		o.todo("done", id, "--force") // direct-merge: no PR by design
		o.DropSpentBranch(branch)
		fmt.Fprintf(o.Out, "orch: ✓ %s landed (worker-direct or prior merge) — confirmed, not re-merging\n", id)
		return MergeAlready
	}
	if !o.Base.RemoteBranchExists(branch) {
		fmt.Fprintf(o.Out, "orch:   %s not pushed — requeue\n", branch)
		o.Requeue(id, "worker turn produced no pushed branch")
		return MergeFailed
	}
	o.Stage.Checkout("nightshift")
	o.Stage.ResetHard("origin/nightshift")
	if err := o.Stage.MergeNoFF("nightshift: merge "+branch+" into nightshift", "origin/"+branch); err != nil {
		files := ""
		if ce, ok := err.(*gitx.ConflictError); ok {
			files = strings.Join(ce.Files, " ")
		}
		fmt.Fprintf(o.Out, "orch: ✗ %s CONFLICTS with nightshift (%s)\n", branch, files)
		o.Requeue(id, fmt.Sprintf("merge conflict with nightshift in: %s — rebuild on current origin/nightshift and resolve", orDefault(files, "?")))
		return MergeFailed
	}
	verdict := plan.GateResult{RC: plan.GatePass}
	if !o.Opt.NoGate {
		verdict = o.RunGate(o.Opt.StageWT())
	}
	if verdict.RC == plan.GatePass || (verdict.RC == plan.GateInconclusive && !o.Opt.Strict) {
		if err := o.Stage.Push([]string{"DEVBRAIN_GATE_SKIP=1"}, "nightshift"); err == nil {
			o.RecordLanded(id) // nightshift now contains this branch → stamp its landing SHA
			o.todo("done", id, "--force") // direct-merge: no PR by design
			o.DropSpentBranch(branch)
			fmt.Fprintf(o.Out, "orch: ✓ merged %s → nightshift; task %s done\n", branch, id)
			return MergeNew
		}
		o.Stage.ResetHard("origin/nightshift")
		fmt.Fprintf(o.Out, "orch: ✗ push of nightshift failed for %s — requeue\n", branch)
		o.Requeue(id, "git push to nightshift failed")
		return MergeFailed
	}
	o.Stage.ResetHard("origin/nightshift")
	fmt.Fprintf(o.Out, "orch: ✗ %s failed gate — not merged\n", branch)
	o.Requeue(id, fmt.Sprintf("gate failed: %s — reproduce by merging your branch onto origin/nightshift and running the test suite", orDefault(verdict.Detail, "tests failed")))
	return MergeFailed
}

// ReconcileTask forces one task's stored status toward the git truth.
func (o *Orch) ReconcileTask(id string) {
	branch := "todo/" + id
	st := o.taskStatus(id)
	if st == "" {
		return
	}
	if o.taskInNightshift(id) {
		if st != "done" && st != "held" {
			if _, err := o.todo("done", id, "--force"); err == nil {
				fmt.Fprintf(o.Out, "orch: ✓ %s already in nightshift — marked %s done (was %s)\n", branch, id, orDefault(st, "?"))
			}
		}
		if o.taskHasRemoteBranch(id) {
			o.DropSpentBranch(branch)
		}
		return
	}
	if st == "held" || st == "done" {
		return
	}
	if !o.taskHasRemoteBranch(id) {
		return
	}
	if o.retryCount(id) >= o.Opt.Retries {
		return
	}
	fmt.Fprintf(o.Out, "orch: ♻ reconcile — %s is pushed but not in nightshift; merging\n", branch)
	o.MergeToNightshift(branch, id)
}

// Reconcile self-heals task state against git: landed tasks become done;
// pushed todo/* branches are merged; branchless review orphans close from the
// surviving nightshift merge subject.
func (o *Orch) Reconcile() {
	o.Base.Fetch()
	seen := map[string]bool{}
	for _, branch := range o.Base.LsRemoteHeads("todo/*") {
		id := strings.TrimPrefix(branch, "todo/")
		if id == "" {
			continue
		}
		seen[id] = true
		// A fixed-set run must not adopt out-of-set residue: the fence parks
		// only OPEN tasks, so a taken/review leftover from a prior run would
		// otherwise get its stale branch merged into this contained run.
		if o.Opt.FixedSet && !plan.InOnly(o.Opt.Only, id) {
			continue
		}
		o.ReconcileTask(id)
	}
	out, _ := o.todoStored("list", "review")
	for _, id := range listIDsLoose(out) {
		if seen[id] {
			continue
		}
		if o.Opt.FixedSet && !plan.InOnly(o.Opt.Only, id) {
			continue
		}
		o.ReconcileTask(id)
	}
}

// listIDsLoose is the `grep -oE '[0-9]{4}-[a-z0-9-]+'` id sweep used where
// the bash pulled ids out of a whole listing.
func listIDsLoose(out string) []string {
	var ids []string
	seen := map[string]bool{}
	for _, m := range looseIDRe.FindAllString(out, -1) {
		if !seen[m] {
			seen[m] = true
			ids = append(ids, m)
		}
	}
	return ids
}

// ReclaimStaleClaims frees tasks stranded `taken` by a dead worker: not held
// by a live worker turn (activeIDs) and claimed longer than ClaimTTL ago.
func (o *Orch) ReclaimStaleClaims(activeIDs map[string]bool) {
	nowS := time.Now().Unix()
	out, _ := o.todo("list", "taken")
	for _, id := range listIDsLoose(out) {
		if activeIDs[id] {
			continue // a live turn owns it — leave it alone
		}
		// Fail closed like the fence: releasing an out-of-set stale claim back
		// to `open` would expose it to `next` if a stale installed todo ignores
		// DEVBRAIN_TODO_ONLY. Leave it `taken` — invisible either way.
		if o.Opt.FixedSet && !plan.InOnly(o.Opt.Only, id) {
			continue
		}
		show, _ := o.todo("show", id)
		ca := taskField(show, "claimed_at")
		age := nowS - epochOf(ca) // no/garbage claimed_at → epoch 0 → huge age → reclaim
		if age >= int64(o.Opt.ClaimTTL) {
			if _, err := o.todo("release", id); err == nil {
				fmt.Fprintf(o.Out, "orch: ♻ reclaimed stale claim %s (taken, no live worker, lease > %ds)\n", id, o.Opt.ClaimTTL)
			}
		}
	}
}

// epochOf parses ISO-8601 UTC (2026-06-19T14:05:44Z) → epoch seconds, or 0.
func epochOf(s string) int64 {
	t, err := time.Parse("2006-01-02T15:04:05Z", s)
	if err != nil {
		return 0
	}
	return t.Unix()
}

// TurnMadeCommits: true only if the worktree branch is AHEAD of the fork-base
// SHA it started from — i.e. the turn committed something. An empty turn
// equals its fork base, which the merge path would mis-read as
// already-landed (the 0085 bug).
func TurnMadeCommits(worktree, forkBase string) bool {
	if forkBase == "" {
		return true // no recorded base → can't prove it's empty; let the merge path decide
	}
	return gitx.Repo{Dir: worktree}.RevListCount(forkBase+"..HEAD") > 0
}

// ReleaseBranchTask restores as if a worker's turn never ran: wipe the
// half-done branch FIRST (local + the pushed copy on origin), reset the
// worktree to a pristine origin/nightshift, and ONLY THEN release the task
// back to `open`. If the remote branch can't be deleted, HOLD the task
// instead — reconcile skips held tasks, so partial work can never ship.
func (o *Orch) ReleaseBranchTask(worktree string) {
	wt := gitx.Repo{Dir: worktree}
	b := wt.CurrentBranch()
	if !strings.HasPrefix(b, "todo/") {
		return
	}
	id := strings.TrimPrefix(b, "todo/")
	wt.CheckoutDetach("origin/nightshift") // leave the branch so it can be deleted
	wt.ResetHard("origin/nightshift")
	wt.CleanFD()
	wt.DeleteBranch(b)   // local ref
	o.Base.PushDelete(b) // pushed copy, if the turn got that far
	// Confirm origin/<b> is actually gone before reopening.
	if o.Base.RemoteBranchExists(b) {
		o.todo("hold", id, fmt.Sprintf("dead turn: could not delete origin/%s — partial work may remain; release after deleting the branch", b))
		fmt.Fprintf(o.Out, "orch: ⚠ origin/%s survived deletion — HELD %s so reconcile won't merge the partial branch\n", b, id)
		return
	}
	if _, err := o.todo("release", id); err == nil {
		fmt.Fprintf(o.Out, "orch: released %s\n", id)
	}
	fmt.Fprintf(o.Out, "orch: discarded partial branch %s (local+remote); worktree restored to origin/nightshift\n", b)
}

// HarvestBranch lands a finished turn from a worker worktree. An EMPTY turn
// (branch == its fork base, no new commit) is released back to `open` rather
// than mis-merged as done; a real turn is gated + merged. Returns true when
// the turn made progress (merge rc 0/2) — the caller's NOMERGE counter.
func (o *Orch) HarvestBranch(worktree, forkBase string) (progress bool) {
	br := gitx.Repo{Dir: worktree}.CurrentBranch()
	if !strings.HasPrefix(br, "todo/") {
		return false // planning / no-branch turn → no merge
	}
	id := strings.TrimPrefix(br, "todo/")
	if !TurnMadeCommits(worktree, forkBase) {
		fmt.Fprintf(o.Out, "orch: worker produced no commit for %s — releasing (empty turn, not marking done)\n", id)
		o.ReleaseBranchTask(worktree)
		return false
	}
	switch o.MergeToNightshift(br, id) {
	case MergeNew, MergeAlready:
		return true
	}
	return false
}

// Cleanup is the shutdown reaper (headless): reap every in-flight turn (via
// the on-disk turn.pid the launch recorded), release its task, then backstop-
// release every still-`taken` task in scope. A HELD task survives — the
// per-worker release is gated on an in-flight turn and the sweep only lists
// `taken`, so neither reopens it and defeats the hold.
func (o *Orch) Cleanup() {
	if o.Opt.FixedSet {
		o.Unfence() // un-park the out-of-set tasks we fenced at boot
	}
	if o.Opt.Mode == "headless" {
		fmt.Fprintln(o.Out, "orch: shutting down — reaping in-flight turns + releasing their claimed tasks")
		for i := 0; i < o.Opt.Workers; i++ {
			// Only workers with an UNHARVESTED in-flight turn — the on-disk
			// turn.pid the launch recorded (the WTPID analog; it survives a hard
			// orchestrator kill). A harvested worktree can sit on a HELD task's
			// todo/ branch; wiping it would release the task and defeat the hold.
			wt := o.Opt.WorkerWT(i)
			pidFile := filepath.Join(wt, ".nightshift", "turn.pid")
			b, err := os.ReadFile(pidFile)
			if err != nil {
				continue
			}
			if pid, perr := strconv.Atoi(strings.TrimSpace(string(b))); perr == nil && pid > 0 {
				killTurn(pid)
			}
			// Release even if the child already died: a separate stop may have
			// reaped it first — the task must not stay stranded `taken`.
			o.ReleaseBranchTask(wt)
			os.Remove(pidFile)
		}
		// Backstop: return every still-`taken` task in scope to `open` (covers
		// a claim made before the worktree was on its todo/ branch). The todo
		// wrapper scopes it; `release` skips `done` tasks.
		out, _ := o.todo("list", "taken")
		for _, id := range listIDsLoose(out) {
			if _, err := o.todo("release", id); err == nil {
				fmt.Fprintf(o.Out, "orch: released stranded claim %s (taken → open on shutdown)\n", id)
			}
		}
	}
	// Both backends: recover killed-turn cost. The test seam skips the
	// transcript scan the same way the bash tests stub backfill_token_cost.
	if os.Getenv("NIGHTSHIFT_TEST_NO_LAUNCH") != "1" {
		o.BackfillTokenCost()
	}
}

// killTurn is the `pkill -P $p; kill $p; wait` sweep for one detached turn.
func killTurn(pid int) {
	exec.Command("pkill", "-P", strconv.Itoa(pid)).Run()
	exec.Command("kill", strconv.Itoa(pid)).Run()
}

// BackfillTokenCost re-derives killed/un-stopped worker turns' token spend
// from the transcripts: a SIGKILLed worker never runs its Stop hook, so its
// tokens never reach the sidecar. The importer is idempotent. Best-effort —
// never aborts teardown. DEVBRAIN_IMPORT_CMD overrides the importer
// invocation (tests pin it to a stub); the default is `devbrain import`.
func (o *Orch) BackfillTokenCost() {
	data := config.DataDir() // same resolution as the capture hooks
	var argv []string
	if imp := os.Getenv("DEVBRAIN_IMPORT_CMD"); imp != "" {
		argv = strings.Fields(imp)
	} else {
		argv = []string{selfBin(), "import"}
	}
	argv = append(argv, "--data", data, "--apply", "--tokens-only")
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout, cmd.Stderr = nil, nil
	if cmd.Run() == nil {
		fmt.Fprintln(o.Out, "orch: backfilled token cost for killed/un-stopped worker turns")
	}
}

// SetupNightshift ports setup_nightshift's branch phase: reset (or keep) the
// integration branch, detaching any worktree that holds it FIRST (a REUSED
// clone keeps the stage / a worker on `nightshift`, which blocks `branch -f`),
// prepare the staging worktree, the retries dir, the shared info/exclude, and
// the Makefile gate auto-detect. The venv/preflight phase (SetupVenv) only
// runs when a gate will actually be used, exactly like the script.
func (o *Orch) SetupNightshift() error {
	o.Base.Fetch()
	if o.Opt.KeepNightshift && o.Base.RemoteBranchExists("nightshift") {
		fmt.Fprintln(o.Out, "orch: keeping existing origin/nightshift")
	} else {
		// Detach worktrees sitting on `nightshift` from a prior run so the
		// reset can move the branch (the legitimate, expected case; the FATALs
		// below are for the rest).
		o.Base.WorktreePrune()
		for _, wt := range o.Base.WorktreesOn("nightshift") {
			gitx.Repo{Dir: wt}.CheckoutDetach("")
		}
		// Reset the integration branch to a fresh base. FAIL LOUDLY if we STILL
		// can't: silently continuing would build every task on a STALE base.
		if err := o.Base.ForceBranch("nightshift", "origin/"+o.Opt.BaseBranch); err != nil {
			return fmt.Errorf("orch: FATAL — can't reset 'nightshift' to origin/%s (checked out in another worktree we couldn't detach). Refusing to run on a stale base.", o.Opt.BaseBranch)
		}
		if err := o.Base.PushForce("nightshift"); err != nil {
			return fmt.Errorf("orch: FATAL — couldn't force-push the reset nightshift to origin.")
		}
		fmt.Fprintf(o.Out, "orch: nightshift reset to origin/%s\n", o.Opt.BaseBranch)
	}
	o.Base.WorktreePrune()
	if _, err := os.Stat(o.Opt.StageWT()); err != nil {
		o.Base.WorktreeAdd(o.Opt.StageWT(), "nightshift", false)
	}
	o.Stage.Checkout("nightshift")
	o.Stage.ResetHard("origin/nightshift")
	os.MkdirAll(o.Opt.RetryDir(), 0o755)
	// Exclude the state dir + common ephemeral build/venv dirs in ALL worktrees
	// (shared info/exclude) so /work's `git add -A` never commits them AND the
	// per-turn `git clean -fd` PRESERVES a worker's venv/build cache.
	excl := filepath.Join(o.Opt.Repo, ".git", "info", "exclude")
	if b, err := os.ReadFile(excl); err == nil {
		have := map[string]bool{}
		for _, l := range strings.Split(string(b), "\n") {
			have[l] = true
		}
		var add []string
		for _, p := range []string{".nightshift/", ".venv/", "venv/", "node_modules/", "__pycache__/"} {
			if !have[p] {
				add = append(add, p)
			}
		}
		if len(add) > 0 {
			f, err := os.OpenFile(excl, os.O_APPEND|os.O_WRONLY, 0o644)
			if err == nil {
				fmt.Fprintln(f, strings.Join(add, "\n"))
				f.Close()
			}
		}
	}
	// Default the gate to `make test` for a Makefile-driven (non-pytest)
	// project: without this the pytest gate collects nothing → "inconclusive" →
	// a RED bash suite slips past base-health AND every merge gate.
	if !o.Opt.NoGate && o.Opt.TestCmd == "" && !fileExists(filepath.Join(o.Opt.StageWT(), "pyproject.toml")) &&
		makefileHasTest(filepath.Join(o.Opt.StageWT(), "Makefile")) {
		// Skip the slow docker clean-room test in the PER-TURN gate; GitHub CI
		// runs the FULL suite on every PR.
		o.Opt.TestCmd = "DEVBRAIN_TEST_SKIP='docker' make test"
		fmt.Fprintln(o.Out, "orch: gate = 'make test' (fast: skips the docker clean-room; CI runs the full set) — at base-health and before every merge")
	}
	if !o.Opt.NoGate && o.Opt.TestCmd == "" {
		if err := o.SetupVenv(); err != nil {
			return err
		}
	}
	return nil
}

// SetupVenv is setup_nightshift's gate-env phase: pick an eligible
// interpreter, build the pytest venv, and preflight that the BASE installs —
// failing fast on a structurally-impossible gate beats discovering it at
// hour 8.
func (o *Orch) SetupVenv() error {
	o.Opt.GatePy = plan.PickGatePython(o.Opt.Repo)
	if o.Opt.GatePy == "" {
		return fmt.Errorf("orch: FATAL — no installed python satisfies %s for the green-gate.\norch:   install an interpreter matching that requirement, or pass --test-cmd to pin your own gate, or --no-gate to skip it.",
			strings.TrimSpace(plan.RequiresPythonLine(o.Opt.Repo)))
	}
	ver, _ := exec.Command(o.Opt.GatePy, "--version").CombinedOutput()
	fmt.Fprintf(o.Out, "orch: green-gate interpreter: %s (%s)\n", o.Opt.GatePy, strings.TrimSpace(string(ver)))
	venv := o.Opt.Venv()
	pip := filepath.Join(venv, "bin", "pip")
	// Upgrade pip/setuptools/wheel FIRST — the venv default pip can be too old
	// for PEP 660 editable installs from a pyproject-only project.
	ok := exec.Command(o.Opt.GatePy, "-m", "venv", venv).Run() == nil &&
		exec.Command(pip, "install", "-q", "--upgrade", "pip", "setuptools", "wheel").Run() == nil &&
		exec.Command(pip, "install", "-q", "pytest").Run() == nil
	if ok {
		fmt.Fprintln(o.Out, "orch: green-gate venv ready (pytest)")
	} else {
		fmt.Fprintln(o.Out, "orch: WARN gate venv unavailable — gate may be inconclusive")
	}
	// Fail fast on a structurally-impossible gate: if the gate venv can't even
	// install the BASE (origin/nightshift), it can never pass, so EVERY merge
	// would be rejected. Only meaningful for a packaged project.
	if fileExists(filepath.Join(o.Opt.Repo, "pyproject.toml")) && fileExists(filepath.Join(venv, "bin", "python")) {
		o.Stage.ResetHard("origin/nightshift")
		dev := exec.Command(pip, "install", "-q", "-e", ".[dev]")
		dev.Dir = o.Opt.StageWT()
		if dev.Run() != nil {
			plain := exec.Command(pip, "install", "-q", "-e", ".")
			plain.Dir = o.Opt.StageWT()
			if plain.Run() != nil {
				return fmt.Errorf("orch: FATAL — green-gate (%s) cannot install origin/nightshift ('pip install -e .' failed).\norch:   the gate would reject every merge. Fix the env (interpreter/deps), or pass --test-cmd / --no-gate.", o.Opt.GatePy)
			}
		}
		fmt.Fprintf(o.Out, "orch: green-gate preflight OK — origin/nightshift installs under %s\n", o.Opt.GatePy)
	}
	return nil
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func makefileHasTest(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "test:") {
			return true
		}
	}
	return false
}
