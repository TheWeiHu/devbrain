package nightshift

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMain builds the real devbrain binary once and exposes it via
// DEVBRAIN_BIN — the orchestrator's todo wrappers re-exec it, and inside `go
// test` os.Executable() is the TEST binary (re-exec'ing that would run the
// suite recursively; this is what hung the first end-to-end run).
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "nsbin")
	if err == nil {
		bin := filepath.Join(dir, "devbrain")
		if out, err := exec.Command("go", "build", "-o", bin, "github.com/TheWeiHu/devbrain/cmd/devbrain").CombinedOutput(); err == nil {
			os.Setenv("DEVBRAIN_BIN", bin)
		} else {
			fmt.Fprintf(os.Stderr, "TestMain: build failed, integration tests will misbehave: %s\n", out)
		}
	}
	code := m.Run()
	if dir != "" {
		os.RemoveAll(dir)
	}
	os.Exit(code)
}

func run(t *testing.T, dir string, argv ...string) string {
	t.Helper()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v in %s: %v\n%s", argv, dir, err, out)
	}
	return string(out)
}

// ClassifyPane fixtures — the pane-content regexes lifted from the script.
func TestClassifyPane(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		pane string
		want func(PaneFlags) bool
	}{
		{"idle footer", "some output\n? for shortcuts · bypass permissions on", func(f PaneFlags) bool { return f.IsIdle() && !f.MidTurn }},
		{"mid turn", "thinking…\nesc to interrupt · bypass permissions on", func(f PaneFlags) bool { return f.MidTurn && !f.IsIdle() }},
		{"trust prompt", "Is this a project you created or trust?\n1. Yes", func(f PaneFlags) bool { return f.TrustPrompt }},
		{"trust folder wording", "Do you trust this folder?", func(f PaneFlags) bool { return f.TrustPrompt }},
		{"menu", "Pick an option\nEnter to select · Tab/Arrow keys to navigate", func(f PaneFlags) bool { return f.Menu }},
		{"api error", "API Error: overloaded_error", func(f PaneFlags) bool { return f.StuckError }},
		{"529", "upstream connect error 529 retrying", func(f PaneFlags) bool { return f.StuckError }},
		{"usage limit", "You've hit your usage limit. Resets at 3am.", func(f PaneFlags) bool { return f.StuckError && f.UsageLimited }},
		{"quota", "monthly quota exceeded", func(f PaneFlags) bool { return f.UsageLimited && !f.StuckError }},
		{"plain output", "compiling…\nstill compiling…", func(f PaneFlags) bool {
			return !f.Footer && !f.MidTurn && !f.TrustPrompt && !f.Menu && !f.StuckError && !f.UsageLimited
		}},
	} {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if f := ClassifyPane(c.pane); !c.want(f) {
				t.Errorf("flags = %+v", f)
			}
		})
	}
}

// Prompt resolution: disk copy beside the repo wins over the embed.
func TestPromptPrecedence(t *testing.T) {
	t.Parallel()
	embedded := DrainRules(t.TempDir())
	if !strings.Contains(embedded, "NIGHTSHIFT") {
		t.Fatalf("embedded drain rules missing: %q", embedded[:min(len(embedded), 80)])
	}
	repo := t.TempDir()
	os.MkdirAll(filepath.Join(repo, "prompts"), 0o755)
	os.WriteFile(filepath.Join(repo, "prompts", "nightshift-drain.txt"), []byte("CUSTOM RULES"), 0o644)
	if got := DrainRules(repo); got != "CUSTOM RULES" {
		t.Errorf("disk copy must win: %q", got)
	}
	if PlanRules(t.TempDir()) == "" {
		t.Error("plan prompt must resolve from the embed")
	}
}

// One full headless turn end-to-end: a stub `claude` claims the task, commits
// on a todo/ branch, pushes it, and exits — the orchestrator must gate-free
// merge it into nightshift and mark the task done, then hit --max-turns.
func TestHeadlessTurnEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	base := filepath.Join(root, "base")
	data := filepath.Join(root, "data")
	run(t, root, "git", "init", "-q", "--bare", origin)
	run(t, root, "git", "clone", "-q", origin, base)
	run(t, base, "git", "checkout", "-q", "-B", "main")
	run(t, base, "git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "root")
	run(t, base, "git", "push", "-q", "origin", "main")

	// a queue with one open task, keyed to this repo's (absent) remote → set DEVBRAIN_PROJECT
	t.Setenv("DEVBRAIN_DATA", data)
	t.Setenv("DEVBRAIN_PROJECT", "ns__e2e")
	todoDir := filepath.Join(data, "projects", "ns__e2e", "todo")
	os.MkdirAll(todoDir, 0o755)
	os.WriteFile(filepath.Join(todoDir, "0001-do-it.md"),
		[]byte("---\nid: 0001-do-it\nstatus: open\npriority: 50\ncreated: 2026-07-01T00:00:00Z\nclaimed_by:\nclaimed_at:\npr:\n---\n\n# Do it\n"), 0o644)

	// stub claude: from its worktree cwd, branch, commit a file, push
	binDir := filepath.Join(root, "bin")
	os.MkdirAll(binDir, 0o755)
	stub := `#!/bin/sh
git checkout -q -b todo/0001-do-it
echo done > work.txt
git add work.txt
git -c user.name=w -c user.email=w@w commit -qm "do it"
git push -q origin todo/0001-do-it
exit 0
`
	os.WriteFile(filepath.Join(binDir, "claude"), []byte(stub), 0o755)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	opt, err := ParseArgs([]string{"--repo", base, "--workers", "1", "--poll", "1",
		"--max-turns", "1", "--no-gate", "--turn-timeout", "60"})
	if err != nil {
		t.Fatal(err)
	}
	var log strings.Builder
	r := NewRunner(NewOrch(opt, &log))
	doneCh := make(chan int, 1)
	go func() { doneCh <- r.Run() }()
	select {
	case rc := <-doneCh:
		if rc != 0 {
			t.Fatalf("run rc=%d\n%s", rc, log.String())
		}
	case <-time.After(90 * time.Second):
		t.Fatalf("orchestrator did not finish\n%s", log.String())
	}

	// the work landed on origin/nightshift with the frozen merge subject
	subjects := run(t, base, "git", "log", "--format=%s", "origin/nightshift")
	if !strings.Contains(subjects, "nightshift: merge todo/0001-do-it into nightshift") {
		t.Errorf("merge subject missing:\n%s\n--- log:\n%s", subjects, log.String())
	}
	// the task is done with a done_at stamp
	taskB, _ := os.ReadFile(filepath.Join(todoDir, "0001-do-it.md"))
	task := string(taskB)
	if !strings.Contains(task, "status: done") || !strings.Contains(task, "done_at: 2") {
		t.Errorf("task not closed:\n%s\n--- log:\n%s", task, log.String())
	}
	// pidfile removed on clean exit
	if _, err := os.Stat(filepath.Join(base, ".nightshift", "orchestrator.pid")); !os.IsNotExist(err) {
		t.Error("orchestrator.pid must be removed on exit")
	}
	for _, want := range []string{"finished a turn rc=0", "✓ merged todo/0001-do-it → nightshift"} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("log missing %q:\n%s", want, log.String())
		}
	}
}

// Fixed-set watchdog: the guard that stops a wedged fixed-set run from spinning
// forever as a silent running:true zombie. It counts consecutive ticks with NO
// worker in flight; on the first trip it asks for ONE escalated recovery, and
// only exits if it's still wedged a full count later. Worker activity (and a
// non-fixed-set run) resets both the counter and the one-shot.
func TestFixedSetWatchdog(t *testing.T) {
	t.Parallel()
	r := &Runner{Orch: &Orch{}}

	// idle runs n idle ticks and returns the action from the last one.
	idle := func(n int) wdAction {
		var a wdAction
		for i := 0; i < n; i++ {
			a = r.watchdogCheck(true, false)
		}
		return a
	}

	// Forever (non-fixed-set): never trips, counter stays parked.
	for i := 0; i < fixedSetWatchdogTicks*2; i++ {
		if r.watchdogCheck(false, false) != wdNone {
			t.Fatalf("non-fixed-set run must not trip (tick %d)", i)
		}
	}
	if r.idleTicks != 0 {
		t.Fatalf("non-fixed-set must leave the counter at 0, got %d", r.idleTicks)
	}

	// Fixed-set with a worker always in flight: never trips.
	for i := 0; i < fixedSetWatchdogTicks*2; i++ {
		if r.watchdogCheck(true, true) != wdNone {
			t.Fatalf("a running worker must reset the watchdog (tick %d)", i)
		}
	}

	// First idle episode: nothing until the Nth tick, which asks for RECOVERY.
	if a := idle(fixedSetWatchdogTicks - 1); a != wdNone {
		t.Fatalf("watchdog acted early: %v", a)
	}
	if a := idle(1); a != wdRecover {
		t.Fatalf("first trip must be wdRecover, got %v", a)
	}
	if r.idleTicks != 0 || !r.wdRecov {
		t.Fatalf("recovery must reset the counter and spend the one-shot: idle=%d recov=%v", r.idleTicks, r.wdRecov)
	}

	// Still wedged a full count later → EXIT (the one-shot is already spent).
	if a := idle(fixedSetWatchdogTicks - 1); a != wdNone {
		t.Fatalf("must count down again before exiting, got %v", a)
	}
	if a := idle(1); a != wdExit {
		t.Fatalf("second trip must be wdExit, got %v", a)
	}

	// Worker activity re-arms the one-shot: a later wedge gets a fresh recovery.
	if r.watchdogCheck(true, true) != wdNone || r.idleTicks != 0 || r.wdRecov {
		t.Fatalf("activity must reset counter + re-arm recovery: idle=%d recov=%v", r.idleTicks, r.wdRecov)
	}
	if a := idle(fixedSetWatchdogTicks); a != wdRecover {
		t.Fatalf("a fresh wedge after recovery must get RECOVER again, got %v", a)
	}
}

// Live rescale: writing .nightshift/desired-workers grows then shrinks
// r.workers across resize passes, and a retired slot's worktree loses its run
// stamp so the dashboard hides it.
func TestResizeWorkers(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	base := filepath.Join(root, "base")
	data := filepath.Join(root, "data")
	run(t, root, "git", "init", "-q", "--bare", origin)
	run(t, root, "git", "clone", "-q", origin, base)
	run(t, base, "git", "checkout", "-q", "-B", "main")
	run(t, base, "git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "root")
	run(t, base, "git", "push", "-q", "origin", "main:main")
	run(t, base, "git", "push", "-q", "origin", "main:nightshift") // prepWorktree checks out origin/nightshift

	// enough open tasks that the work cap doesn't clamp a grow to 3
	t.Setenv("DEVBRAIN_DATA", data)
	t.Setenv("DEVBRAIN_PROJECT", "ns__resize")
	todoDir := filepath.Join(data, "projects", "ns__resize", "todo")
	os.MkdirAll(todoDir, 0o755)
	for _, n := range []string{"0001", "0002", "0003", "0004", "0005"} {
		os.WriteFile(filepath.Join(todoDir, n+"-t.md"),
			[]byte("---\nid: "+n+"-t\nstatus: open\npriority: 50\ncreated: 2026-07-01T00:00:00Z\nclaimed_by:\n---\n\n# t\n"), 0o644)
	}

	opt, err := ParseArgs([]string{"--repo", base, "--workers", "1"})
	if err != nil {
		t.Fatal(err)
	}
	var log strings.Builder
	r := NewRunner(NewOrch(opt, &log))
	r.runID = "testrun"
	os.MkdirAll(filepath.Join(base, ".nightshift"), 0o755)
	r.workers = make([]worker, 1)
	r.desired = 1
	r.prepWorktree(0)

	stamp := func(i int) string { return filepath.Join(r.Opt.WorkerWT(i), ".nightshift", "run") }

	// GROW 1 → 3
	os.WriteFile(opt.DesiredWorkersFile(), []byte("3\n"), 0o644)
	r.resizeWorkers(r.openCount(), 0)
	if len(r.workers) != 3 {
		t.Fatalf("grow: want 3 slots, got %d\n%s", len(r.workers), log.String())
	}
	for i := 0; i < 3; i++ {
		if b, err := os.ReadFile(stamp(i)); err != nil || strings.TrimSpace(string(b)) != "testrun" {
			t.Errorf("slot %d run stamp = %q err=%v", i, b, err)
		}
	}

	// SHRINK 3 → 1: trailing idle slots dropped, their run stamps cleared
	os.WriteFile(opt.DesiredWorkersFile(), []byte("1\n"), 0o644)
	r.resizeWorkers(r.openCount(), 0)
	if len(r.workers) != 1 {
		t.Fatalf("shrink: want 1 slot, got %d\n%s", len(r.workers), log.String())
	}
	for i := 1; i < 3; i++ {
		if _, err := os.Stat(stamp(i)); !os.IsNotExist(err) {
			t.Errorf("retired slot %d must lose its run stamp (err=%v)", i, err)
		}
	}
	if _, err := os.Stat(stamp(0)); err != nil {
		t.Errorf("surviving slot 0 must keep its run stamp: %v", err)
	}
}

// Forever mode must not collapse the fleet on a momentary queue drain: with the
// desired-workers file unchanged, an empty queue (oc=0, nothing running) keeps the
// current target — the pending planning turn will refill. A user downscale (a lower
// file value) still shrinks.
func TestResizeWorkersForeverDrainKeepsFleet(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	os.MkdirAll(filepath.Join(repo, ".nightshift"), 0o755)
	opt := DefaultOptions() // Forever: true
	opt.Repo = repo
	var log strings.Builder
	r := NewRunner(NewOrch(opt, &log))
	r.desired = 4
	r.workers = make([]worker, 4)
	for i := range r.workers { // scope shrink's run-stamp removal to a temp dir
		r.workers[i].wt = t.TempDir()
	}

	// Momentary drain: empty queue, no rescale requested → hold at 4.
	r.resizeWorkers(0, 0)
	if r.desired != 4 || len(r.workers) != 4 {
		t.Fatalf("drain must keep fleet: desired=%d slots=%d\n%s", r.desired, len(r.workers), log.String())
	}

	// User downscale still shrinks even with an empty queue.
	os.WriteFile(opt.DesiredWorkersFile(), []byte("2\n"), 0o644)
	r.resizeWorkers(0, 0)
	if r.desired != 2 || len(r.workers) != 2 {
		t.Fatalf("user downscale must shrink: desired=%d slots=%d\n%s", r.desired, len(r.workers), log.String())
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// The singleton pidfile refuses a second orchestrator on the same repo.
func TestRunSingleton(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	os.MkdirAll(filepath.Join(repo, ".nightshift"), 0o755)
	os.WriteFile(filepath.Join(repo, ".nightshift", "orchestrator.pid"),
		[]byte(fmt.Sprintf("%d\n", os.Getpid())), 0o644) // a live pid (ours)
	opt := DefaultOptions()
	opt.Repo = repo
	var log strings.Builder
	if rc := NewRunner(NewOrch(opt, &log)).Run(); rc != 1 {
		t.Errorf("second orchestrator must refuse: rc=%d\n%s", rc, log.String())
	}
	if !strings.Contains(log.String(), "another orchestrator owns") {
		t.Errorf("log: %s", log.String())
	}
}
