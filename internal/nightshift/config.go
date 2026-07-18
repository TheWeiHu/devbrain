// Package nightshift is the autonomous overnight orchestrator: option parsing,
// the fixed-set fence, the daemon loop and its worker backends (headless +
// tmux), the green-gate run, and the merge/reconcile plumbing. The pure
// decision logic it drives — the assignment policy, the CI-scope check, and the
// gate's interpreter selection + verdict classification — lives in the plan
// subpackage; status emission lives in status. The hidden
// `devbrain nightshift internal …` entrypoints expose the primitives the
// black-box CLI tests drive.
package nightshift

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/TheWeiHu/devbrain/internal/config"
	"github.com/TheWeiHu/devbrain/internal/projectkey"
)

// Options carries every orchestrator flag plus the tuning constants the
// script sets as globals. Field comments name the bash variable.
type Options struct {
	Repo           string      // BASE (required; absolute)
	Workers        int         // N=3
	Mode           string      // MODE=headless (or tmux)
	TurnMax        int         // TURN_MAX=1800 — per-turn wall cap, seconds (headless)
	Hang           int         // HANG=600 — frozen-pane threshold, seconds (tmux)
	Low            int         // LOW=2 — accepted for back-compat, no-op
	MaxTurns       int         // MAXTURNS=0 (0 = unlimited)
	MaxWall        int         // MAXWALL=0 (0 = unlimited)
	Poll           int         // POLL=15
	Replan         int         // REPLAN=300 — min gap between planning turns
	Only           string      // ONLY — normalized comma list once parsed
	OnlyGiven      bool        // ONLY_GIVEN
	Agents         []agentKind // slot-expanded --agents (empty = all claude)
	AgentsGiven    bool
	WorkersGiven   bool
	FixedSet       bool   // FIXED_SET
	Forever        bool   // FOREVER=1
	BaseBranch     string // BASE_BRANCH=main
	KeepNightshift bool   // KEEP_NIGHTSHIFT
	TestCmd        string // TEST_CMD
	NoGate         bool   // NO_GATE
	Strict         bool   // STRICT
	Retries        int    // RETRIES=2
	Notify         bool   // NOTIFY (off by default)
	GatePy         string // GATE_PY=python3 (set for real in setup)
	Model          string // --model <id|alias> forwarded to claude -p (empty = CLI default)

	ClaimTTL     int // CLAIM_TTL=5400
	StallK       int // STALL_K=8
	ReconEvery   int // RECON_EVERY=8
	LimitBackoff int // LIMIT_BACKOFF=300
	ResendGrace  int // RESEND_GRACE=60
}

// DefaultOptions mirrors the top-of-script defaults exactly.
func DefaultOptions() Options {
	return Options{
		Workers: 3, Mode: "headless", TurnMax: 1800, Hang: 600, Low: 2,
		Poll: 15, Replan: 300, Forever: true, BaseBranch: "main",
		Retries: 2, GatePy: "python3",
		ClaimTTL: 5400, StallK: 8, ReconEvery: 8,
		LimitBackoff: 300, ResendGrace: 60,
	}
}

// ParseArgs consumes the orchestrator's flag surface into Options.
// Unknown flags are an error, like the script's `unknown arg` exit.
func ParseArgs(args []string) (Options, error) {
	o := DefaultOptions()
	i := 0
	next := func(flag string) (string, error) {
		i++
		if i >= len(args) {
			return "", fmt.Errorf("orch: %s needs a value", flag)
		}
		return args[i], nil
	}
	num := func(flag string) (int, error) {
		v, err := next(flag)
		if err != nil {
			return 0, err
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			return 0, fmt.Errorf("orch: %s: not a number: %s", flag, v)
		}
		return n, nil
	}
	for ; i < len(args); i++ {
		var err error
		switch args[i] {
		case "--repo":
			o.Repo, err = next("--repo")
		case "--workers":
			o.Workers, err = num("--workers")
			o.WorkersGiven = true
		case "--agents":
			var spec string
			if spec, err = next("--agents"); err == nil {
				o.Agents, err = parseAgents(spec, o.Workers)
				o.AgentsGiven = true
			}
		case "--tmux":
			o.Mode = "tmux"
		case "--headless":
			o.Mode = "headless"
		case "--turn-timeout":
			o.TurnMax, err = num("--turn-timeout")
		case "--hang":
			o.Hang, err = num("--hang")
		case "--low":
			o.Low, err = num("--low")
		case "--max-turns":
			o.MaxTurns, err = num("--max-turns")
			o.Forever = false
		case "--max-wall":
			o.MaxWall, err = num("--max-wall")
			o.Forever = false
		case "--replan":
			o.Replan, err = num("--replan")
		case "--only":
			o.Only, err = next("--only")
			o.OnlyGiven = true
		case "--poll":
			o.Poll, err = num("--poll")
		case "--base-branch":
			o.BaseBranch, err = next("--base-branch")
		case "--keep-nightshift":
			o.KeepNightshift = true
		case "--test-cmd":
			o.TestCmd, err = next("--test-cmd")
		case "--no-gate":
			o.NoGate = true
		case "--strict-gate":
			o.Strict = true
		case "--retries":
			o.Retries, err = num("--retries")
		case "--notify":
			o.Notify = true
		case "--model":
			o.Model, err = next("--model")
			if err == nil {
				if o.Model = strings.TrimSpace(o.Model); o.Model == "" {
					return o, fmt.Errorf("orch: --model needs a non-empty value (omit the flag for each CLI's default)")
				}
			}
		default:
			return o, fmt.Errorf("orch: unknown arg %s", args[i])
		}
		if err != nil {
			return o, err
		}
	}
	if o.AgentsGiven {
		if o.WorkersGiven {
			return o, fmt.Errorf("orch: use --agents OR --workers, not both")
		}
		o.Workers = len(o.Agents)
		var hasClaude, hasCodex bool
		for _, k := range o.Agents {
			if k == agentClaude {
				hasClaude = true
			}
			if k == agentCodex {
				hasCodex = true
			}
		}
		if o.Mode == "tmux" && hasCodex {
			return o, fmt.Errorf("orch: codex workers require the headless backend — the tmux backend is claude-only (drop --tmux or codex from --agents)")
		}
		// One --model string can't name both a claude alias and a codex model id,
		// so a mixed fleet would silently break whichever kind doesn't recognize
		// it. Fail loud; per-worker models are the --worker-model growth path.
		if o.Model != "" && hasClaude && hasCodex {
			return o, fmt.Errorf("orch: --model is one value for the whole run, but this is a mixed claude+codex fleet — a single id can't name both; use a single-agent fleet (--agents claude=… or --agents codex)")
		}
	}
	return o, nil
}

// Derived paths (the script's STAGE_WT / VENV / RETRYDIR / RULES_FILE / LANDED).

func (o Options) StageWT() string    { return o.Repo + "-stage" }
func (o Options) Venv() string       { return filepath.Join(o.Repo, ".nightshift", "venv") }
func (o Options) RetryDir() string   { return filepath.Join(o.Repo, ".nightshift", "retries") }
func (o Options) RulesFile() string  { return filepath.Join(o.Repo, ".nightshift", "drain-rules.txt") }
func (o Options) LandedFile() string { return filepath.Join(o.Repo, ".nightshift", "landed.tsv") }

// BackoffFile records an in-progress usage-limit backoff so the status emitter
// can tell the dashboard the fleet is paused, not dead. Absent = not backing off.
func (o Options) BackoffFile() string { return filepath.Join(o.Repo, ".nightshift", "backoff.json") }

// OnlyFile records THIS run's fixed-set (the normalized --only list) so the
// standalone status emitter can scope its queue counts to the launched subset.
func (o Options) OnlyFile() string { return filepath.Join(o.Repo, ".nightshift", "only.txt") }

// DesiredWorkersFile holds the live worker-count target the coordinator re-reads
// each tick (same re-read-per-pass pattern as OnlyFile) so the fleet can be
// scaled up or down without a restart.
func (o Options) DesiredWorkersFile() string {
	return filepath.Join(o.Repo, ".nightshift", "desired-workers")
}

// ModeFile records this run's backend ("headless" or "tmux") so the dashboard
// and its scale API can tell them apart — tmux fleets can't be live-rescaled
// (resizeWorkers is headless-only), so the API rejects it and the UI hides the
// stepper. Written once at boot by the orchestrator (the single writer, so no
// race with cliWatch, which owns nightshift-run.json).
func (o Options) ModeFile() string {
	return filepath.Join(o.Repo, ".nightshift", "mode")
}

// ModelFile records the --model this run requested (empty = CLI default) so the
// emitter can surface it in status.json and the dashboard. Written once at boot
// (single writer, like ModeFile); absent → the account's Claude CLI default.
func (o Options) ModelFile() string {
	return filepath.Join(o.Repo, ".nightshift", "model")
}

// WorkerWT is the per-worker worktree path ($BASE-w<i>).
func (o Options) WorkerWT(i int) string { return fmt.Sprintf("%s-w%d", o.Repo, i) }

// ── ~/.config/nightshift/repo — the remembered repo (scripts/nightshift) ─────

func confDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "nightshift")
}

// SaveRepo remembers the repo path so later verbs need no argument.
func SaveRepo(repo string) error {
	d := confDir()
	if d == "" {
		return os.ErrNotExist
	}
	if err := os.MkdirAll(d, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(d, "repo"), []byte(repo+"\n"), 0o644)
}

// ResolveRepo returns the explicit repo if it is a directory, else the
// remembered one, else "".
func ResolveRepo(explicit string) string {
	if explicit != "" {
		if fi, err := os.Stat(explicit); err == nil && fi.IsDir() {
			abs, err := filepath.Abs(explicit)
			if err == nil {
				return abs
			}
			return explicit
		}
	}
	d := confDir()
	if d == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(d, "repo"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// ── nightshift-run.json — dashboard linkage (scripts/nightshift) ─────────────

// DataProjectDir maps a repo checkout to its projects/<owner>__<repo> dir in
// the data repo, or "" when the project dir does not exist.
func DataProjectDir(repo string) string {
	key := projectkey.ProjectKey(repo)
	if key == "" {
		return ""
	}
	data, err := config.ResolveDataDir()
	if err != nil {
		return ""
	}
	d := filepath.Join(data, "projects", key)
	if fi, err := os.Stat(d); err == nil && fi.IsDir() {
		return d
	}
	return ""
}

// RegisterRun advertises a live run's dashboard port via nightshift-run.json.
func RegisterRun(repo string, port int) error {
	d := DataProjectDir(repo)
	if d == "" {
		return nil // unresolvable project — same silent no-op as the script
	}
	b, err := json.Marshal(map[string]any{"port": port, "repo": repo})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(d, "nightshift-run.json"), append(b, '\n'), 0o644)
}

// UnregisterRun erases the run marker (best-effort).
func UnregisterRun(repo string) {
	if d := DataProjectDir(repo); d != "" {
		os.Remove(filepath.Join(d, "nightshift-run.json"))
	}
}
