package taskcontract

import (
	"strings"
	"testing"

	"github.com/TheWeiHu/devbrain/internal/task"
)

func taskWith(fields, body string) *task.Task {
	return task.Parse("---\nid: 0042-test\nstatus: open\n"+fields+"---\n\n# Test\n\n"+body+"\n", "proj")
}

const completeBody = `Outcome: command reports contract readiness
Evidence: internal/taskcontract owns the parser
Scope: internal/taskcontract and focused tests
Non-goals: no scheduler mutation
Acceptance: valid contracts pass and invalid contracts explain why
Verify: go test ./internal/taskcontract`

func TestInspectLegacyIsAdvisory(t *testing.T) {
	r := Inspect(taskWith("", "free-form legacy task"))
	if r.State != "legacy" || len(r.Errors) != 0 || len(r.Warnings) != 1 {
		t.Fatalf("legacy report = %+v", r)
	}
}

func TestInspectValidContract(t *testing.T) {
	fields := "contract_version: 1\ntask_type: feature\ndepends_on: none\nconflict_keys: path:internal/taskcontract/, resource:todo-schema\nbudget_turns: 1\n"
	r := Inspect(taskWith(fields, completeBody))
	if r.State != "valid" || len(r.Errors) != 0 {
		t.Fatalf("valid report = %+v", r)
	}
	if len(r.Contract.DependsOn) != 0 || len(r.Contract.ConflictKeys) != 2 || r.Contract.BudgetTurns != 1 {
		t.Fatalf("contract = %+v", r.Contract)
	}
}

func TestInspectRejectsIncompleteContract(t *testing.T) {
	fields := "contract_version: 1\ntask_type: dream\ndepends_on:\nconflict_keys: /tmp/nope\nbudget_turns: 0\n"
	r := Inspect(taskWith(fields, "Outcome: <single observable result>"))
	if r.State != "invalid" || len(r.Errors) < 8 {
		t.Fatalf("invalid report = %+v", r)
	}
}

func TestInspectStopsAtSynthesizedContext(t *testing.T) {
	fields := "contract_version: 1\ntask_type: feature\ndepends_on: none\nconflict_keys: path:internal/taskcontract/\nbudget_turns: 1\n"
	body := "Outcome: real outcome\n## Context (synthesized now)\nAcceptance: context must not satisfy the contract"
	r := Inspect(taskWith(fields, body))
	if r.State != "invalid" || !strings.Contains(strings.Join(r.Errors, " "), "Acceptance") {
		t.Fatalf("context labels should not satisfy contract: %+v", r)
	}
}

func TestInspectAcceptsCompactWorkPacketLabels(t *testing.T) {
	fields := "contract_version: 1\ntask_type: bug\ndepends_on: 0041-first\nconflict_keys: path:internal/todo/todo.go\nbudget_turns: 1\n"
	body := `- **Outcome:** fix atomic claim
- **Evidence:** claim race test
- **Scope:** internal/todo
- **Non-goals:** dashboard redesign
- **Acceptance:** one conflicting claim succeeds
- **Verify:** go test ./internal/todo`
	r := Inspect(taskWith(fields, body))
	if r.State != "valid" || len(r.Contract.DependsOn) != 1 {
		t.Fatalf("compact labels = %+v", r)
	}
}

func TestConflictKeys(t *testing.T) {
	a := Contract{ConflictKeys: []string{"path:internal/nightshift/", "resource:queue"}}
	if got := Conflict(a, Contract{ConflictKeys: []string{"path:internal/nightshift/gate.go"}}); got == "" {
		t.Fatal("directory path key should conflict with descendant")
	}
	if got := Conflict(a, Contract{ConflictKeys: []string{"resource:queue"}}); got != "resource:queue" {
		t.Fatalf("resource conflict = %q", got)
	}
	if got := Conflict(a, Contract{ConflictKeys: []string{"path:internal/todo/"}}); got != "" {
		t.Fatalf("unrelated paths conflict = %q", got)
	}
}
