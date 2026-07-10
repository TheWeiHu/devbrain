package todo_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TheWeiHu/devbrain/internal/clitest"
)

const contractBody = `Outcome: scheduler selects only runnable tasks
Evidence: contract CLI integration test
Scope: task contract selection and claiming
Non-goals: no dashboard redesign
Acceptance: dependencies and conflicts control eligibility
Verify: go test ./internal/todo`

func addContract(t *testing.T, h *clitest.Harness, title, priority, depends, conflict string) string {
	t.Helper()
	r := h.Run("todo", "add", title,
		"-p", priority,
		"-b", contractBody,
		"--contract",
		"--task-type", "feature",
		"--depends-on", depends,
		"--conflict-key", conflict,
		"--budget-turns", "1")
	if r.Code != 0 {
		t.Fatalf("add contract: exit %d\n%s", r.Code, r.Stderr)
	}
	return r.Out()
}

func TestTodoContractCLI(t *testing.T) {
	h := clitest.New(t)
	legacy := h.Run("todo", "add", "legacy task", "-p", "100").Out()
	first := addContract(t, h, "first contracted task", "90", "none", "path:internal/shared.go")
	dependent := addContract(t, h, "dependent contracted task", "80", first, "path:internal/dependent.go")
	conflicting := addContract(t, h, "conflicting contracted task", "70", "none", "path:internal/shared.go")

	t.Run("contract fields round trip and validate", func(t *testing.T) {
		show := h.Run("todo", "show", first).Stdout
		for _, want := range []string{
			"contract_version: 1",
			"task_type: feature",
			"depends_on: none",
			"conflict_keys: path:internal/shared.go",
			"budget_turns: 1",
		} {
			if !strings.Contains(show, want) {
				t.Errorf("task missing %q:\n%s", want, show)
			}
		}
		var reports []map[string]any
		r := h.Run("todo", "validate", first, "--json")
		if r.Code != 0 || json.Unmarshal([]byte(r.Stdout), &reports) != nil || len(reports) != 1 || reports[0]["state"] != "valid" {
			t.Fatalf("validate valid: code=%d stdout=%s stderr=%s", r.Code, r.Stdout, r.Stderr)
		}
	})

	t.Run("invalid explicit contract is rejected without a task file", func(t *testing.T) {
		dir := filepath.Dir(h.TaskFile(first))
		before, _ := os.ReadDir(dir)
		r := h.Run("todo", "add", "bad contract", "--contract", "--task-type", "feature", "-b", "Outcome: only")
		after, _ := os.ReadDir(dir)
		if r.Code == 0 || !strings.Contains(r.Stderr, "invalid task contract") || len(after) != len(before) {
			t.Fatalf("invalid add: code=%d before=%d after=%d stderr=%s", r.Code, len(before), len(after), r.Stderr)
		}
	})

	t.Run("shadow preserves legacy behavior", func(t *testing.T) {
		if got := h.Run("todo", "claim-next", "--policy", "shadow").Out(); got != legacy {
			t.Fatalf("shadow claim = %q want legacy top task %q", got, legacy)
		}
		if r := h.Run("todo", "release", legacy); r.Code != 0 {
			t.Fatalf("release legacy: %s", r.Stderr)
		}
	})

	t.Run("contract policy enforces dependencies and active conflicts", func(t *testing.T) {
		if got := h.Run("todo", "ready", "--policy", "contract", "--count").Out(); got != "2" {
			t.Fatalf("initial ready count = %q want first + same-key candidate before either is active", got)
		}
		if got := h.Run("todo", "claim-next", "--policy", "contract").Out(); got != first {
			t.Fatalf("first contract claim = %q want %q", got, first)
		}
		if got := h.Run("todo", "ready", "--policy", "contract", "--count").Out(); got != "0" {
			t.Fatalf("ready while first active = %q want 0", got)
		}
		if r := h.Run("todo", "done", first, "--force"); r.Code != 0 {
			t.Fatalf("close dependency: %s", r.Stderr)
		}
		if got := h.Run("todo", "claim-next", "--policy", "contract").Out(); got != dependent {
			t.Fatalf("claim after dependency done = %q want %q", got, dependent)
		}
		if got := h.Run("todo", "show", conflicting).Stdout; !strings.Contains(got, "status: open") {
			t.Fatalf("unclaimed conflicting task changed state:\n%s", got)
		}
	})
}

func TestClaimNextIsAtomicAcrossProcesses(t *testing.T) {
	h := clitest.New(t)
	first := addContract(t, h, "atomic first", "90", "none", "path:shared/file.go")
	second := addContract(t, h, "atomic second", "80", "none", "path:shared/file.go")

	env := []string{}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "DEVBRAIN_") {
			env = append(env, kv)
		}
	}
	env = append(env, "DEVBRAIN_DATA="+h.Data, "DEVBRAIN_PROJECT="+h.Project)
	cmds := []*exec.Cmd{
		exec.Command(h.Bin, "todo", "claim-next", "--policy", "contract"),
		exec.Command(h.Bin, "todo", "claim-next", "--policy", "contract"),
	}
	bufs := make([]*bytes.Buffer, len(cmds))
	for i, cmd := range cmds {
		bufs[i] = &bytes.Buffer{}
		cmd.Env = env
		cmd.Stdout, cmd.Stderr = bufs[i], bufs[i]
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
	}
	outputs := []string{}
	for i, cmd := range cmds {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("claim-next process: %v\n%s", err, bufs[i].String())
		}
		if value := strings.TrimSpace(bufs[i].String()); value != "" {
			outputs = append(outputs, value)
		}
	}
	if len(outputs) != 1 || outputs[0] != first {
		t.Fatalf("atomic outputs = %v want only %q", outputs, first)
	}
	if show := h.Run("todo", "show", second).Stdout; !strings.Contains(show, "status: open") {
		t.Fatalf("second conflicting task should remain open:\n%s", show)
	}
}
