package nightshift

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// gate.go — the green-gate: interpreter selection, the pytest/test-cmd run
// with its retry + env scrub, verdict classification, and the base-health
// rule that an import/collection error is NOT a red base.

// Gate verdicts (run_gate's return codes).
const (
	GatePass         = 0
	GateFail         = 1
	GateInconclusive = 2
)

// GateResult carries run_gate's rc plus the globals it set
// (GATE_DETAIL, GATE_IMPORT_ERROR).
type GateResult struct {
	RC          int
	Detail      string
	ImportError bool
}

// gateTimeout bounds one suite run (the script's `timeout 600`).
const gateTimeout = 600 * time.Second

// ── interpreter selection ─────────────────────────────────────────────────────

var (
	requiresPyRe = regexp.MustCompile(`(?m)^[ \t]*requires-python.*$`)
	floorRe      = regexp.MustCompile(`(>=?|~=|==)[ \t]*3\.([0-9]+)`)
	capRe        = regexp.MustCompile(`(<=?)[ \t]*3\.([0-9]+)`)
)

// ParseRequiresPython extracts the [lo, hi) minor-version window from a
// pyproject requires-python line. Only 3.x is in play; a `<4.0`-style cap
// matches nothing here and correctly imposes no ceiling. Defaults: lo=0 hi=99.
func ParseRequiresPython(req string) (lo, hi int) {
	lo, hi = 0, 99
	// Floor markers: `>=`/`>` (range), `~=` (compatible-release, ≈ `>=`),
	// and `==` (exact pin).
	if m := floorRe.FindStringSubmatch(req); m != nil {
		lo, _ = strconv.Atoi(m[2])
	}
	// exclusive `<3.x` or inclusive `<=3.x`
	if m := capRe.FindStringSubmatch(req); m != nil {
		hi, _ = strconv.Atoi(m[2])
		if m[1] == "<=" { // inclusive cap → exclusive ceiling is one higher
			hi++
		}
	}
	// `==3.x` pins a single minor (no `<` clause), so its ceiling is the
	// floor + 1. `~=3.x` is `>=3.x` with no real 3.x ceiling — leave hi alone.
	if strings.Contains(req, "==") && floorRe.MatchString(req) {
		hi = lo + 1
	}
	return lo, hi
}

// requiresPythonLine returns the first requires-python line of
// repo/pyproject.toml ("" when absent — no constraint).
func requiresPythonLine(repo string) string {
	b, err := os.ReadFile(filepath.Join(repo, "pyproject.toml"))
	if err != nil {
		return ""
	}
	return requiresPyRe.FindString(string(b))
}

// pyMinorSatisfies is the version probe (`$c -c "import sys; …"`); swapped
// out in unit tests so they don't depend on installed interpreters.
var pyMinorSatisfies = func(interp string, lo, hi int) bool {
	cmd := exec.Command(interp, "-c",
		fmt.Sprintf("import sys; m=sys.version_info[1]; sys.exit(0 if m>=%d and m<%d else 1)", lo, hi))
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	return cmd.Run() == nil
}

// FindInterpreter scans PATH for every python3.N (lowest minor first, so we
// pick the interpreter nearest the declared floor, not the newest), then
// generic python3 as the last resort. Returns "" when the window is
// unsatisfiable by any installed interpreter.
func FindInterpreter(lo, hi int) string {
	minors := map[int]bool{}
	re := regexp.MustCompile(`^python3\.([0-9]+)$`)
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		ents, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range ents {
			if m := re.FindStringSubmatch(e.Name()); m != nil {
				n, _ := strconv.Atoi(m[1])
				minors[n] = true
			}
		}
	}
	var sorted []int
	for n := range minors {
		sorted = append(sorted, n)
	}
	sort.Ints(sorted)
	var cands []string
	for _, n := range sorted {
		cands = append(cands, fmt.Sprintf("python3.%d", n))
	}
	cands = append(cands, "python3")
	for _, c := range cands {
		if _, err := exec.LookPath(c); err != nil {
			continue
		}
		if pyMinorSatisfies(c, lo, hi) {
			return c
		}
	}
	return "" // requires-python set but unsatisfiable by any installed interpreter
}

// PickGatePython ports pick_gate_python: choose an interpreter satisfying the
// project's requires-python (both bounds). "" = declared but unsatisfiable.
func PickGatePython(repo string) string {
	lo, hi := ParseRequiresPython(requiresPythonLine(repo))
	return FindInterpreter(lo, hi)
}

// ── suite run + classification ────────────────────────────────────────────────

var pytestHeadRe = regexp.MustCompile(`(?m)^(FAILED|ERROR).*$`)

// ClassifyPytest maps a pytest exit code + output to the gate verdict.
// pytest prints FAILED for a real assertion failure, ERROR for a collection/
// import failure. "ERROR but no FAILED" means the suite never ran — an
// environment problem, not broken code — flagged so base_gate can tell the
// two apart.
func ClassifyPytest(rc int, out string) GateResult {
	res := GateResult{}
	heads := pytestHeadRe.FindAllString(out, 4)
	res.Detail = strings.Join(heads, " ")
	if res.Detail == "" {
		res.Detail = lastLinesDetail(out)
	}
	hasError, hasFailed := false, false
	for _, h := range pytestHeadRe.FindAllString(out, -1) {
		if strings.HasPrefix(h, "ERROR") {
			hasError = true
		} else {
			hasFailed = true
		}
	}
	if hasError && !hasFailed {
		res.ImportError = true
	}
	switch rc {
	case 0:
		res.RC = GatePass
	case 5:
		res.RC = GateInconclusive // no tests collected
	case 1:
		res.RC = GateFail
	case 2:
		res.RC = GateFail // collection/import error
		res.ImportError = true
	default:
		res.RC = GateInconclusive
	}
	return res
}

// lastLinesDetail is the fallback detail: last 3 lines joined, cut to 240
// (`tail -3 | tr '\n' ' ' | cut -c1-240`).
func lastLinesDetail(out string) string {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) > 3 {
		lines = lines[len(lines)-3:]
	}
	s := strings.Join(lines, " ")
	if len(s) > 240 {
		s = s[:240]
	}
	return s
}

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
func (o *Orch) RunGate(dir string) GateResult {
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
				return GateResult{RC: GatePass}
			}
			if attempt == 1 {
				fmt.Fprintln(w, "  gate retry: suite failed once — re-running to rule out a flake")
			}
		}
		detail := lastLinesDetail(out)
		fmt.Fprintf(w, "  gate FAIL (%s, 2 attempts): %s\n", o.Opt.TestCmd, detail)
		return GateResult{RC: GateFail, Detail: detail}
	}
	venvPy := filepath.Join(o.Opt.Venv(), "bin", "python")
	if fi, err := os.Stat(venvPy); err != nil || fi.Mode()&0o111 == 0 {
		fmt.Fprintln(w, "  gate inconclusive (no venv)")
		return GateResult{RC: GateInconclusive}
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
	res := ClassifyPytest(rc, out)
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

// ── base health ───────────────────────────────────────────────────────────────

// ClassifyBase applies base_gate's verdict rule to a gate result:
// RED only on a genuine test FAILED. "Couldn't build/import the base" is an
// OPERATOR problem on a CI-green base — not broken code — so it stays green
// (inconclusive) rather than stopping the world. Returns true when RED.
func ClassifyBase(res GateResult, noGate bool) bool {
	if noGate {
		return false
	}
	if res.RC == GateFail && res.ImportError {
		return false // environment, not code
	}
	return res.RC != GatePass && res.RC != GateInconclusive
}

// BaseGate checks whether origin/nightshift is green ON ITS OWN.
// Returns (red, result).
func (o *Orch) BaseGate() (bool, GateResult) {
	if o.Opt.NoGate {
		return false, GateResult{RC: GatePass}
	}
	o.Base.Fetch()
	o.Stage.Checkout("nightshift")
	o.Stage.ResetHard("origin/nightshift")
	res := o.RunGate(o.Opt.StageWT())
	if res.RC == GateFail && res.ImportError {
		fmt.Fprintf(o.Out, "orch: ⚠ base gate could not build/import origin/nightshift (environment, not code) — NOT flagging RED. Detail: %s\n", orDefault(res.Detail, "?"))
		return false, res
	}
	return ClassifyBase(res, false), res
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
