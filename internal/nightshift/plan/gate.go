package plan

import (
	"encoding/json"
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

// GateDetection is a repository-owned test contract discovered without
// executing project code. Kind is "command", "pytest", or empty.
type GateDetection struct {
	Kind    string
	Command string
	Source  string
	Tool    string
}

var makeTestTargetRe = regexp.MustCompile(`(?m)^[ \t]*test[ \t]*:`)

// DetectGate chooses a conventional test command from files on the checked-out
// base. A project-owned Make target wins; language defaults follow. Empty means
// Nightshift cannot prove that a merge is safe without an operator override.
func DetectGate(repo string) GateDetection {
	if b, err := os.ReadFile(filepath.Join(repo, "Makefile")); err == nil && makeTestTargetRe.Match(b) {
		return GateDetection{Kind: "command", Command: "make test", Source: "Makefile test target", Tool: "make"}
	}
	if fileExists(filepath.Join(repo, "go.mod")) || fileExists(filepath.Join(repo, "go.work")) {
		return GateDetection{Kind: "command", Command: "go test ./...", Source: "Go module", Tool: "go"}
	}
	if fileExists(filepath.Join(repo, "Cargo.toml")) {
		return GateDetection{Kind: "command", Command: "cargo test --all-targets", Source: "Cargo.toml", Tool: "cargo"}
	}
	if d := detectNodeGate(repo); d.Kind != "" {
		return d
	}
	for _, name := range []string{"pyproject.toml", "pytest.ini", "tox.ini", "setup.cfg"} {
		if fileExists(filepath.Join(repo, name)) {
			return GateDetection{Kind: "pytest", Source: name, Tool: "python3"}
		}
	}
	if fi, err := os.Stat(filepath.Join(repo, "tests")); err == nil && fi.IsDir() {
		return GateDetection{Kind: "pytest", Source: "tests/ directory", Tool: "python3"}
	}
	return GateDetection{}
}

func detectNodeGate(repo string) GateDetection {
	b, err := os.ReadFile(filepath.Join(repo, "package.json"))
	if err != nil {
		return GateDetection{}
	}
	var pkg struct {
		PackageManager string            `json:"packageManager"`
		Scripts        map[string]string `json:"scripts"`
	}
	if json.Unmarshal(b, &pkg) != nil {
		return GateDetection{}
	}
	test := strings.TrimSpace(pkg.Scripts["test"])
	if test == "" || strings.Contains(strings.ToLower(test), "no test specified") {
		return GateDetection{}
	}
	manager := ""
	if pkg.PackageManager != "" {
		manager = strings.SplitN(pkg.PackageManager, "@", 2)[0]
	}
	if manager == "" {
		switch {
		case fileExists(filepath.Join(repo, "pnpm-lock.yaml")):
			manager = "pnpm"
		case fileExists(filepath.Join(repo, "yarn.lock")):
			manager = "yarn"
		case fileExists(filepath.Join(repo, "bun.lock")) || fileExists(filepath.Join(repo, "bun.lockb")):
			manager = "bun"
		default:
			manager = "npm"
		}
	}
	commands := map[string]string{
		"npm": "npm test", "pnpm": "pnpm test", "yarn": "yarn test", "bun": "bun run test",
	}
	command, ok := commands[manager]
	if !ok {
		return GateDetection{}
	}
	return GateDetection{Kind: "command", Command: command, Source: "package.json test script", Tool: manager}
}

func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
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
// operator/gate problem rather than a code-red verdict. The strict admission
// policy handles that separately and stops the run; this function only answers
// whether the tested code is known red.
func ClassifyBase(res GateResult, noGate bool) bool {
	if noGate {
		return false
	}
	if res.RC == GateFail && res.ImportError {
		return false // environment, not code
	}
	return res.RC != GatePass && res.RC != GateInconclusive
}
