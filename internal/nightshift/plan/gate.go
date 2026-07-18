package plan

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// gate.go — the pure green-gate decisions: interpreter selection, pytest
// verdict classification, and the base-health rule that an import/collection
// error is NOT a red base. The stateful suite run (RunGate/BaseGate) lives in
// the nightshift root package and calls into these.

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

// RequiresPythonLine returns the first requires-python line of
// repo/pyproject.toml ("" when absent — no constraint).
func RequiresPythonLine(repo string) string {
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
	lo, hi := ParseRequiresPython(RequiresPythonLine(repo))
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
		res.Detail = LastLinesDetail(out)
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

// LastLinesDetail is the fallback detail: last 3 lines joined, cut to 240
// (`tail -3 | tr '\n' ' ' | cut -c1-240`).
func LastLinesDetail(out string) string {
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

// causeNoiseRe strips the run-to-run varying parts of a failure detail (digits,
// hex blobs, tmp paths) so the same failure fingerprints identically each run.
var causeNoiseRe = regexp.MustCompile(`(?i)(/[^ ]*/)|(0x[0-9a-f]+)|([0-9]+)`)

// CauseFingerprint keys a generated blocker task by its stable cause, so a
// failure recurring across runs updates ONE task instead of filing a duplicate
// every time the gate re-runs red.
func CauseFingerprint(detail string) string {
	s := strings.ToLower(causeNoiseRe.ReplaceAllString(detail, " "))
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		s = "unknown"
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:8]
}
