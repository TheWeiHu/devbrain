package nightshift

import (
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/TheWeiHu/devbrain/internal/gitx"
)

// Orch is the orchestrator's state for the ported core: options plus the two
// git handles every helper pokes (the base clone and its staging worktree).
// Policy counters stay out — PickTurn takes them explicitly.
type Orch struct {
	Opt   Options
	Base  gitx.Repo
	Stage gitx.Repo
	Out   io.Writer
	RunID string
}

// NewOrch wires an Orch for a parsed option set.
func NewOrch(opt Options, out io.Writer) *Orch {
	return &Orch{
		Opt:   opt,
		Base:  gitx.Repo{Dir: opt.Repo},
		Stage: gitx.Repo{Dir: opt.StageWT()},
		Out:   out,
	}
}

// ── queue access ──────────────────────────────────────────────────────────────
// ALL of the orchestrator's queue reads/writes go through these wrappers, so
// the queue env — git-derived status + the fixed-set scope — is applied at
// exactly the calls that need it and NOWHERE ELSE (the #164/#169 leak class).
// Each call is a child `devbrain todo` process with cwd=$BASE and the scoped
// env set explicitly, mirroring the bash subshell wrappers:
//   todo        — the fleet's view: fixed-set scoped, status derived from git
//   todoAll     — the WHOLE queue (fence management must see out-of-set tasks)
//   todoStored  — stored status only (reconcile compares stored vs git truth)

// selfBin resolves the devbrain binary to re-exec for queue verbs.
// DEVBRAIN_BIN overrides (the shim convention) — REQUIRED under `go test`,
// where os.Executable() is the test binary and re-exec'ing it would run the
// suite recursively.
func selfBin() string {
	if b := os.Getenv("DEVBRAIN_BIN"); b != "" {
		return b
	}
	if exe, err := os.Executable(); err == nil {
		return exe
	}
	return "devbrain"
}

func (o *Orch) runTodo(derive, only string, args []string) (string, error) {
	cmd := exec.Command(selfBin(), append([]string{"todo"}, args...)...)
	cmd.Dir = o.Opt.Repo
	env := scrubbedEnv() // drop any inherited copies, then pin exactly ours
	env = append(env,
		"DEVBRAIN_TODO_DERIVE_GIT="+derive,
		"DEVBRAIN_TODO_ONLY="+only,
		"DEVBRAIN_TODO_TASK_POLICY="+o.Opt.TaskPolicy,
	)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return strings.TrimRight(string(out), "\n"), err
}

func (o *Orch) todo(args ...string) (string, error) {
	return o.runTodo("1", o.Opt.Only, args)
}
func (o *Orch) todoAll(args ...string) (string, error) {
	return o.runTodo("1", "", args)
}
func (o *Orch) todoStored(args ...string) (string, error) {
	return o.runTodo("0", o.Opt.Only, args)
}

// taskField extracts a frontmatter field from `todo show` output
// (`sed -n 's/^field:[[:space:]]*//p' | head -1`).
func taskField(show, field string) string {
	for _, line := range strings.Split(show, "\n") {
		if strings.HasPrefix(line, field+":") {
			return strings.TrimLeft(strings.TrimPrefix(line, field+":"), " \t")
		}
	}
	return ""
}

// taskStatus is the orchestrator's task_status (scoped todo view).
func (o *Orch) taskStatus(id string) string {
	out, err := o.todo("show", id)
	if err != nil {
		return ""
	}
	return taskField(out, "status")
}
