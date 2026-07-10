package todo

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/TheWeiHu/devbrain/internal/task"
	"github.com/TheWeiHu/devbrain/internal/taskcontract"
)

const (
	policyLegacy   = "legacy"
	policyShadow   = "shadow"
	policyContract = "contract"
)

type queueTask struct {
	task   *task.Task
	status string
	report taskcontract.Report
}

type readiness struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Priority      int      `json:"priority"`
	Status        string   `json:"status"`
	ContractState string   `json:"contract_state"`
	Eligible      bool     `json:"eligible"`
	WouldBlock    bool     `json:"would_block"`
	BlockedBy     []string `json:"blocked_by"`
	Conflicts     []string `json:"conflicts"`
	Errors        []string `json:"errors"`
	Warnings      []string `json:"warnings"`
}

type readyDoc struct {
	Policy string      `json:"policy"`
	Tasks  []readiness `json:"tasks"`
}

func parsePolicy(args []string) (string, []string, string) {
	policy := strings.ToLower(strings.TrimSpace(os.Getenv("DEVBRAIN_TODO_TASK_POLICY")))
	if policy == "" {
		policy = policyShadow
	}
	rest := []string{}
	for i := 0; i < len(args); i++ {
		if args[i] == "--policy" {
			if i+1 >= len(args) {
				return "", nil, "--policy needs legacy, shadow, or contract"
			}
			policy = strings.ToLower(args[i+1])
			i++
			continue
		}
		rest = append(rest, args[i])
	}
	if policy != policyLegacy && policy != policyShadow && policy != policyContract {
		return "", nil, "bad policy: " + policy + " (legacy|shadow|contract)"
	}
	return policy, rest, ""
}

func (c *cli) queueTasks() []*queueTask {
	ents, err := os.ReadDir(c.dir)
	if err != nil {
		return nil
	}
	out := []*queueTask{}
	for _, e := range ents {
		name := e.Name()
		if !strings.HasSuffix(name, ".md") || strings.HasPrefix(name, ".") {
			continue
		}
		id := strings.TrimSuffix(name, ".md")
		t, err := task.Load(c.taskPath(id), c.project)
		if err != nil {
			continue
		}
		out = append(out, &queueTask{task: t, status: c.effectiveStatus(t, id), report: taskcontract.Inspect(t)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].task.Priority != out[j].task.Priority {
			return out[i].task.Priority > out[j].task.Priority
		}
		if out[i].task.Created != out[j].task.Created {
			return out[i].task.Created < out[j].task.Created
		}
		return out[i].task.ID < out[j].task.ID
	})
	return out
}

func dependencyCycles(tasks map[string]*queueTask) map[string]bool {
	state := map[string]uint8{}
	stack := []string{}
	cycles := map[string]bool{}
	var visit func(string)
	visit = func(id string) {
		if state[id] == 2 {
			return
		}
		if state[id] == 1 {
			for i := len(stack) - 1; i >= 0; i-- {
				cycles[stack[i]] = true
				if stack[i] == id {
					break
				}
			}
			return
		}
		qt := tasks[id]
		if qt == nil || qt.report.State != "valid" {
			return
		}
		state[id] = 1
		stack = append(stack, id)
		for _, dep := range qt.report.Contract.DependsOn {
			visit(dep)
		}
		stack = stack[:len(stack)-1]
		state[id] = 2
	}
	for id := range tasks {
		visit(id)
	}
	return cycles
}

func (c *cli) readiness(policy string) []readiness {
	all := c.queueTasks()
	byID := map[string]*queueTask{}
	for _, qt := range all {
		byID[qt.task.ID] = qt
	}
	cycles := dependencyCycles(byID)
	out := []readiness{}
	for _, qt := range all {
		if qt.status != "open" || !onlyMatch(qt.task.ID) {
			continue
		}
		r := readiness{
			ID: qt.task.ID, Title: qt.task.Title, Priority: qt.task.Priority, Status: qt.status,
			ContractState: qt.report.State, Eligible: true, BlockedBy: []string{}, Conflicts: []string{},
			Errors: append([]string{}, qt.report.Errors...), Warnings: append([]string{}, qt.report.Warnings...),
		}
		if qt.report.State == "valid" {
			for _, dep := range qt.report.Contract.DependsOn {
				other := byID[dep]
				switch {
				case other == nil:
					r.BlockedBy = append(r.BlockedBy, dep+" (missing)")
				case other.status != "done":
					r.BlockedBy = append(r.BlockedBy, dep+" ("+other.status+")")
				}
			}
			if cycles[qt.task.ID] {
				r.BlockedBy = append(r.BlockedBy, "dependency cycle")
			}
			for _, active := range all {
				if active.task.ID == qt.task.ID || (active.status != "taken" && active.status != "review") {
					continue
				}
				if active.report.State != "valid" {
					if policy == policyContract {
						r.Conflicts = append(r.Conflicts, active.task.ID+" (legacy scope unknown)")
					}
					continue
				}
				if key := taskcontract.Conflict(qt.report.Contract, active.report.Contract); key != "" {
					r.Conflicts = append(r.Conflicts, active.task.ID+" ("+key+")")
				}
			}
		}

		wouldBlock := qt.report.State != "valid" || len(r.BlockedBy) > 0 || len(r.Conflicts) > 0
		r.WouldBlock = wouldBlock
		if policy == policyContract && wouldBlock {
			r.Eligible = false
		}
		out = append(out, r)
	}
	return out
}

func (c *cli) ready(args []string) int {
	policy, rest, why := parsePolicy(args)
	if why != "" {
		return c.die("ready: " + why)
	}
	count, asJSON := false, false
	for _, arg := range rest {
		switch arg {
		case "--count":
			count = true
		case "--json":
			asJSON = true
		default:
			return c.die("ready: bad flag: " + arg)
		}
	}
	reports := c.readiness(policy)
	if count {
		n := 0
		for _, r := range reports {
			if r.Eligible {
				n++
			}
		}
		fmt.Fprintln(c.stdout, n)
		return 0
	}
	if asJSON {
		enc := json.NewEncoder(c.stdout)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(readyDoc{Policy: policy, Tasks: reports}); err != nil {
			return c.die(err.Error())
		}
		return 0
	}
	fmt.Fprintf(c.stdout, "ready: %s (policy=%s)\n", c.project, policy)
	shown := 0
	for _, r := range reports {
		if !r.Eligible {
			continue
		}
		state := r.ContractState
		if r.WouldBlock && policy == policyShadow {
			state = "shadow-blocked"
		}
		fmt.Fprintf(c.stdout, "  [%3d] %-14s %-32s %s\n", r.Priority, state, r.ID, r.Title)
		shown++
	}
	if shown == 0 {
		fmt.Fprintln(c.stdout, "  (empty)")
	}
	return 0
}

func (c *cli) validate(args []string) int {
	_, rest, why := parsePolicy(args)
	if why != "" {
		return c.die("validate: " + why)
	}
	asJSON, openOnly, id := false, false, ""
	for _, arg := range rest {
		switch arg {
		case "--json":
			asJSON = true
		case "--open":
			openOnly = true
		default:
			if strings.HasPrefix(arg, "-") || id != "" {
				return c.die("validate: bad argument: " + arg)
			}
			id = argID([]string{arg})
		}
	}
	all := c.queueTasks()
	reports := []taskcontract.Report{}
	bad := false
	for _, qt := range all {
		if id != "" && qt.task.ID != id {
			continue
		}
		if openOnly && qt.status != "open" {
			continue
		}
		reports = append(reports, qt.report)
		bad = bad || qt.report.State == "invalid"
	}
	if id != "" && len(reports) == 0 {
		return c.die("no such todo: " + id)
	}
	if asJSON {
		enc := json.NewEncoder(c.stdout)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(reports); err != nil {
			return c.die(err.Error())
		}
	} else {
		for _, report := range reports {
			fmt.Fprintf(c.stdout, "%s\t%s", report.ID, report.State)
			detail := append(append([]string{}, report.Errors...), report.Warnings...)
			if len(detail) > 0 {
				fmt.Fprint(c.stdout, "\t"+strings.Join(detail, "; "))
			}
			fmt.Fprintln(c.stdout)
		}
	}
	if bad {
		return 1
	}
	return 0
}

func (c *cli) claimNext(args []string) int {
	policy, rest, why := parsePolicy(args)
	if why != "" {
		return c.die("claim-next: " + why)
	}
	if len(rest) != 0 {
		return c.die("claim-next: bad argument: " + rest[0])
	}
	return c.withClaimLock(func() int {
		for _, candidate := range c.readiness(policy) {
			if !candidate.Eligible {
				continue
			}
			if rc := c.claimID(candidate.ID, false); rc != 0 {
				return rc
			}
			fmt.Fprintln(c.stdout, candidate.ID)
			return 0
		}
		return 0
	})
}

func (c *cli) withClaimLock(fn func() int) int {
	sum := sha256.Sum256([]byte(c.dir))
	lock := filepath.Join(os.TempDir(), fmt.Sprintf("devbrain-todo-claim-%x.lock", sum[:8]))
	deadline := time.Now().Add(10 * time.Second)
	for {
		f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			fmt.Fprintf(f, "%d\n", os.Getpid())
			f.Close()
			defer os.Remove(lock)
			return fn()
		}
		if !os.IsExist(err) {
			return c.die("claim-next lock: " + err.Error())
		}
		if fi, statErr := os.Stat(lock); statErr == nil && time.Since(fi.ModTime()) > 60*time.Second {
			os.Remove(lock)
			continue
		}
		if time.Now().After(deadline) {
			return c.die("claim-next: queue is busy")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
