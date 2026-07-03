package nightshift

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/TheWeiHu/devbrain/internal/nightshift/plan"
)

// gate.go — the stateful green-gate: the pytest/test-cmd run with its retry +
// env scrub, and the Orch methods that drive it. Interpreter selection and
// verdict classification are pure and live in the plan subpackage.

// gateTimeout bounds one suite run (the script's `timeout 600`).
const gateTimeout = 600 * time.Second

// scrubbedEnv returns the process env with the queue vars removed. The
// orchestrator no longer exports DEVBRAIN_TODO_ONLY / DEVBRAIN_TODO_DERIVE_GIT,
// but the shell that LAUNCHED nightshift may still carry them, and either one
// deterministically breaks tests that build their own throwaway queues,
// false-REDing the gate (the #164/#169 leak). Defense in depth.
func scrubbedEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "DEVBRAIN_TODO_ONLY=") ||
			strings.HasPrefix(kv, "DEVBRAIN_TODO_DERIVE_GIT=") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

// runTimed runs argv in dir with the gate timeout and scrubbed env,
// returning combined output + exit code (124 on timeout, like coreutils).
func runTimed(dir string, argv ...string) (string, int) {
	ctx, cancel := context.WithTimeout(context.Background(), gateTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = scrubbedEnv()
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return string(out), 124
	}
	if err == nil {
		return string(out), 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return string(out), ee.ExitCode()
	}
	return string(out), 127
}

// RunGate ports run_gate: rc 0 pass · 1 fail · 2 inconclusive, with Detail on
// fail and ImportError on collection/import-only failures. Messages go to w
// (the orchestrator log).
func (o *Orch) RunGate(dir string) plan.GateResult {
	w := o.Out
	if o.Opt.TestCmd != "" {
		// Retry once on failure: a single flaky test shouldn't RED the base and
		// deadlock every merge. A real regression fails both attempts; a flake
		// almost never does.
		var out string
		var rc int
		for attempt := 1; attempt <= 2; attempt++ {
			out, rc = runTimed(dir, "bash", "-c", o.Opt.TestCmd)
			if rc == 0 {
				retry := ""
				if attempt == 2 {
					retry = " (retry)"
				}
				fmt.Fprintf(w, "  gate PASS: %s%s\n", o.Opt.TestCmd, retry)
				return plan.GateResult{RC: plan.GatePass}
			}
			if attempt == 1 {
				fmt.Fprintln(w, "  gate retry: suite failed once — re-running to rule out a flake")
			}
		}
		detail := plan.LastLinesDetail(out)
		fmt.Fprintf(w, "  gate FAIL (%s, 2 attempts): %s\n", o.Opt.TestCmd, detail)
		return plan.GateResult{RC: plan.GateFail, Detail: detail}
	}
	venvPy := filepath.Join(o.Opt.Venv(), "bin", "python")
	if fi, err := os.Stat(venvPy); err != nil || fi.Mode()&0o111 == 0 {
		fmt.Fprintln(w, "  gate inconclusive (no venv)")
		return plan.GateResult{RC: plan.GateInconclusive}
	}
	// Install the package + its declared deps (dev extras if present) so pytest
	// can actually import it. If this fails the suite won't collect → rc=2 →
	// FAIL, which is correct for MERGE admission; base_gate reads ImportError
	// to NOT treat it as a red base.
	pip := filepath.Join(o.Opt.Venv(), "bin", "pip")
	if _, rc := runTimed(dir, pip, "install", "-q", "-e", ".[dev]"); rc != 0 {
		runTimed(dir, pip, "install", "-q", "-e", ".")
	}
	out, rc := runTimed(dir, venvPy, "-m", "pytest", "-q")
	res := plan.ClassifyPytest(rc, out)
	switch {
	case rc == 0:
		fmt.Fprintln(w, "  gate PASS (pytest)")
	case rc == 5:
		fmt.Fprintln(w, "  gate inconclusive (no tests collected)")
	case rc == 1:
		fmt.Fprintf(w, "  gate FAIL (pytest): %s\n", res.Detail)
	case rc == 2:
		fmt.Fprintf(w, "  gate FAIL (collection/import error): %s\n", res.Detail)
	default:
		fmt.Fprintf(w, "  gate inconclusive (pytest rc=%d)\n", rc)
	}
	return res
}

// BaseGate checks whether origin/nightshift is green ON ITS OWN.
// Returns (red, result).
func (o *Orch) BaseGate() (bool, plan.GateResult) {
	if o.Opt.NoGate {
		return false, plan.GateResult{RC: plan.GatePass}
	}
	o.Base.Fetch()
	o.Stage.Checkout("nightshift")
	o.Stage.ResetHard("origin/nightshift")
	res := o.RunGate(o.Opt.StageWT())
	if res.RC == plan.GateFail && res.ImportError {
		fmt.Fprintf(o.Out, "orch: ⚠ base gate could not build/import origin/nightshift (environment, not code) — NOT flagging RED. Detail: %s\n", orDefault(res.Detail, "?"))
		return false, res
	}
	return plan.ClassifyBase(res, false), res
}

// EnsureBaseFixTask files ONE high-priority fix task for a red base (deduped
// against the WHOLE queue). Never files in fixed-set mode: the fence/$ONLY
// scoping makes the new task INVISIBLE to every worker (nothing could ever
// fix the base → deadlock), and each red gate would drop another orphan task.
func (o *Orch) EnsureBaseFixTask(detail string) {
	if o.Opt.FixedSet {
		return
	}
	const title = "NIGHTSHIFT IS RED — fix the failing test(s) to unblock all merges"
	// Dedup on the EXACT title in a still-actionable state (anything but
	// done/held). Whole-queue view (todoAll): an ONLY-scoped list can hide the
	// existing fix task and file a duplicate every time the gate re-runs red.
	out, _ := o.todoAll("list", "all")
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, title) &&
			!strings.Contains(line, "done") && !strings.Contains(line, "held") {
			return
		}
	}
	// Pin the gate's own interpreter in the repro hint — a bare `python3` may
	// be older than requires-python, so a worker following the hint would
	// reproduce the env bug rather than the real failure.
	py := orDefault(o.Opt.GatePy, "python3")
	body := fmt.Sprintf("origin/nightshift fails its OWN test suite, so EVERY task merge fails the gate — the whole fleet is blocked until this is green. Fix the failing test(s) and push nightshift green. Failing: %s. Reproduce: checkout nightshift, %s -m pip install -e '.[dev]', %s -m pytest -q.",
		orDefault(detail, "?"), py, py)
	o.todo("add", title, "-p", "99", "-b", body)
	fmt.Fprintf(o.Out, "orch: 🩺 nightshift RED → filed priority-99 fix task — %s\n", orDefault(detail, "?"))
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
