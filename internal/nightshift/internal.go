package nightshift

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// internal.go — the hidden `devbrain nightshift internal <fn>` plumbing
// entrypoints. The acceptance tests (internal/nightshift/*_test.go, package
// nightshift_test) drive the SAME functions the orchestrator uses via these verbs, so the
// tested code IS the shipped code. Contract per verb (stdout / exit):
//
//   pick-gate-python                     interpreter name (or nothing) / 0
//   run-gate DIR [--test-cmd C]          gate log lines / 0 pass · 1 fail · 2 inconclusive
//   classify-base --rc N [--import-error] [--no-gate]   / 0 green · 1 RED
//   ensure-base-fix-task --detail D [--fixed-set --only S]  orch lines / 0
//   ci-scope-unsafe FILE                 / 0 unsafe · 1 safe
//   pick-turn --state JSON               decision JSON / 0
//   assign-round --open N --workers N [--fixed-set]  assigned indices / 0
//   parse-only --only S [--workers N]    orch lines / 0 ok · 1 FATAL
//   in-only ID --only S                  / 0 in set · 1 not
//   fence --only S | unfence             orch lines / 0
//   unresolved --only S                  count / 0
//   record-landed ID | landed-sha ID     (sha) / 0
//   verify --only S                      orch lines / 0 present · 1 absent
//   turn-made-commits WT SHA             / 0 made commits · 1 empty turn
//   cleanup [--workers N ...]            orch lines / 0
//   reconcile | reconcile-task ID        orch lines / 0
//   reclaim                              orch lines / 0
//   merge BRANCH ID                      orch lines / 0 new · 2 already · 1 fail
//   setup-nightshift                     orch lines / 0 · 1 FATAL
//   backfill-tokens                      orch line on success / 0 (best-effort)
//   todo|todo-all|todo-stored ARGS…      todo passthrough (wrapper-scoped env)

var looseIDRe = regexp.MustCompile(`[0-9]{4}-[a-z0-9-]+`)

// errW lets ParseOnly route FATALs to stderr while Out stays the log stream.
var errWriter io.Writer

// Err returns the error stream (stderr in the CLI; Out in library use).
func (o *Orch) Err() io.Writer {
	if errWriter != nil {
		return errWriter
	}
	return o.Out
}

// RunInternal dispatches one internal verb. Flags may appear anywhere in the
// argument list (the test harness appends --repo after the verb's own args).
func RunInternal(args []string, stdout, stderr io.Writer) int {
	errWriter = stderr
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: devbrain nightshift internal <fn> [args] --repo BASE")
		return 2
	}
	fn, rest := args[0], args[1:]

	// Split rest into positionals and orchestrator/verb flags.
	var pos []string
	var state, detail string
	rc, open := 0, 0
	importErr := false
	opt := DefaultOptions()
	takesValue := map[string]*string{
		"--state":  &state,
		"--detail": &detail,
	}
	var flagArgs []string
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		if p, ok := takesValue[a]; ok {
			if i+1 < len(rest) {
				*p = rest[i+1]
				i++
			}
			continue
		}
		switch a {
		case "--rc", "--open":
			if i+1 < len(rest) {
				n, _ := strconv.Atoi(rest[i+1])
				if a == "--rc" {
					rc = n
				} else {
					open = n
				}
				i++
			}
		case "--import-error":
			importErr = true
		case "--fixed-set":
			opt.FixedSet = true
		default:
			if strings.HasPrefix(a, "--") {
				flagArgs = append(flagArgs, a)
				// orchestrator flags that take a value
				switch a {
				case "--repo", "--workers", "--turn-timeout", "--hang", "--low",
					"--max-turns", "--max-wall", "--replan", "--only", "--poll",
					"--base-branch", "--test-cmd", "--retries":
					if i+1 < len(rest) {
						flagArgs = append(flagArgs, rest[i+1])
						i++
					}
				}
			} else {
				pos = append(pos, a)
			}
		}
	}
	fixedSet := opt.FixedSet
	parsed, err := ParseArgs(flagArgs)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	parsed.FixedSet = fixedSet || parsed.FixedSet
	o := NewOrch(parsed, stdout)

	need := func(n int, usage string) bool {
		if len(pos) < n {
			fmt.Fprintf(stderr, "nightshift internal %s: usage: %s\n", fn, usage)
			return false
		}
		return true
	}

	switch fn {
	case "pick-gate-python":
		if py := PickGatePython(o.Opt.Repo); py != "" {
			fmt.Fprintln(stdout, py)
		}
		return 0

	case "run-gate":
		if !need(1, "run-gate DIR [--test-cmd C]") {
			return 2
		}
		return o.RunGate(pos[0]).RC

	case "classify-base":
		res := GateResult{RC: rc, ImportError: importErr}
		if o.Opt.NoGate {
			return 0
		}
		if res.RC == GateFail && res.ImportError {
			fmt.Fprintf(stdout, "orch: ⚠ base gate could not build/import origin/nightshift (environment, not code) — NOT flagging RED. Detail: %s\n", orDefault(res.Detail, "?"))
			return 0
		}
		if ClassifyBase(res, false) {
			return 1
		}
		return 0

	case "ensure-base-fix-task":
		o.EnsureBaseFixTask(detail)
		return 0

	case "ci-scope-unsafe":
		if !need(1, "ci-scope-unsafe FILE") {
			return 2
		}
		if CIScopeUnsafe(pos[0]) {
			return 0
		}
		return 1

	case "pick-turn":
		s := PolicyState{StallK: o.Opt.StallK, Replan: int64(o.Opt.Replan)}
		if state != "" {
			if err := json.Unmarshal([]byte(state), &s); err != nil {
				fmt.Fprintf(stderr, "pick-turn: bad --state: %v\n", err)
				return 2
			}
		}
		b, _ := json.Marshal(PickTurn(s))
		fmt.Fprintln(stdout, string(b))
		return 0

	case "assign-round":
		s := PolicyState{
			Open: open, StallK: o.Opt.StallK, Replan: int64(o.Opt.Replan),
			FixedSet: o.Opt.FixedSet,
			// PLANNED_LAST=now in the statelock fixture: a plan turn never
			// fires inside an assignment round.
			Now: 1000, PlannedLast: 1000,
		}
		if state != "" {
			if err := json.Unmarshal([]byte(state), &s); err != nil {
				fmt.Fprintf(stderr, "assign-round: bad --state: %v\n", err)
				return 2
			}
		}
		idx := AssignRound(s, o.Opt.Workers)
		var out []string
		for _, i := range idx {
			out = append(out, strconv.Itoa(i))
		}
		fmt.Fprintln(stdout, strings.Join(out, " "))
		return 0

	case "parse-only":
		if err := o.ParseOnly(o.Opt.Only); err != nil {
			return 1
		}
		return 0

	case "in-only":
		if !need(1, "in-only ID --only SET") {
			return 2
		}
		if InOnly(o.Opt.Only, pos[0]) {
			return 0
		}
		return 1

	case "fence":
		o.Opt.FixedSet = true
		o.Fence()
		return 0

	case "unfence":
		o.Unfence()
		return 0

	case "unresolved":
		fmt.Fprintln(stdout, o.Unresolved())
		return 0

	case "record-landed":
		if !need(1, "record-landed ID") {
			return 2
		}
		o.RecordLanded(pos[0])
		return 0

	case "landed-sha":
		if !need(1, "landed-sha ID") {
			return 2
		}
		if sha := o.LandedSHA(pos[0]); sha != "" {
			fmt.Fprintln(stdout, sha)
		}
		return 0

	case "verify":
		if _, ok := o.Verify(); !ok {
			return 1
		}
		return 0

	case "turn-made-commits":
		if !need(1, "turn-made-commits WORKTREE [FORK_BASE_SHA]") {
			return 2
		}
		base := ""
		if len(pos) > 1 {
			base = pos[1]
		}
		if TurnMadeCommits(pos[0], base) {
			return 0
		}
		return 1

	case "cleanup":
		o.Cleanup()
		return 0

	case "reconcile":
		o.Reconcile()
		return 0

	case "reconcile-task":
		if !need(1, "reconcile-task ID") {
			return 2
		}
		o.ReconcileTask(pos[0])
		return 0

	case "reclaim":
		o.ReclaimStaleClaims(map[string]bool{})
		return 0

	case "merge":
		if !need(2, "merge BRANCH ID") {
			return 2
		}
		return o.MergeToNightshift(pos[0], pos[1])

	case "setup-nightshift":
		if err := o.SetupNightshift(); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0

	case "backfill-tokens":
		o.BackfillTokenCost()
		return 0

	case "todo", "todo-all", "todo-stored":
		var out string
		var terr error
		switch fn {
		case "todo":
			out, terr = o.todo(pos...)
		case "todo-all":
			out, terr = o.todoAll(pos...)
		default:
			out, terr = o.todoStored(pos...)
		}
		if out != "" {
			fmt.Fprintln(stdout, out)
		}
		if terr != nil {
			return 1
		}
		return 0
	}
	fmt.Fprintf(stderr, "nightshift internal: unknown fn: %s\n", fn)
	return 2
}
