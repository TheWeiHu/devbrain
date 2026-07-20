package nightshift

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A limit-hit turn must not count as no-progress: the stall counter it feeds
// holds every open task, and the base-fix dedup skips held tasks, so each
// backoff loop would file a fresh priority-99 blocker.
func TestHarvestLimitHitDoesNotCountAsNoProgress(t *testing.T) {
	for _, tc := range []struct {
		name    string
		log     string
		wantNo  int
		wantCap int
		timeout bool
	}{
		{"limit hit", "Claude usage limit reached — resets at 3pm\n", 0, 1, false},
		{"normal empty turn", "nothing to do\n", 1, 0, false},
		{"limit hit on a timed-out turn", "out of credit\n", 0, 1, true},
		{"timed-out turn", "still working\n", 1, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wt := t.TempDir()
			logPath := filepath.Join(wt, "turn.log")
			if err := os.WriteFile(logPath, []byte(tc.log), 0o644); err != nil {
				t.Fatal(err)
			}
			r := &Runner{
				Orch:    &Orch{Opt: Options{Repo: t.TempDir()}, Out: io.Discard},
				workers: []worker{{wt: wt, logPath: logPath, running: true}},
			}
			r.harvest(turnDone{i: 0, rc: 1, timedOut: tc.timeout})
			if r.noMerge != tc.wantNo {
				t.Fatalf("noMerge = %d, want %d", r.noMerge, tc.wantNo)
			}
			if r.capacityFailures != tc.wantCap {
				t.Fatalf("capacityFailures = %d, want %d", r.capacityFailures, tc.wantCap)
			}
		})
	}
}

// The backoff file is what tells the dashboard the fleet is paused, not dead.
func TestWriteBackoff(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".nightshift"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := &Runner{Orch: &Orch{Opt: Options{Repo: repo}, Out: io.Discard}}
	r.writeBackoff(true, 300)
	b, err := os.ReadFile(r.Opt.BackoffFile())
	if err != nil {
		t.Fatalf("backoff file not written: %v", err)
	}
	for _, want := range []string{`"reason":"usage limit"`, `"seconds":300`, `"until"`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("backoff %s missing %s", b, want)
		}
	}
	r.writeBackoff(false, 300)
	if _, err := os.Stat(r.Opt.BackoffFile()); !os.IsNotExist(err) {
		t.Fatal("backoff file survived a clear")
	}
}

func TestRunBudgetCircuitPredicates(t *testing.T) {
	r := &Runner{
		Orch:    &Orch{Opt: Options{MaxCapacityFailures: 3, MaxWorkerTurns: 2}},
		desired: 2,
		workers: []worker{{turns: 2}, {turns: 1}},
	}
	r.capacityFailures = 2
	if r.capacityCircuitOpen() {
		t.Error("capacity circuit opened before its threshold")
	}
	r.capacityFailures = 3
	if !r.capacityCircuitOpen() {
		t.Error("capacity circuit did not open at its threshold")
	}
	if r.workerBudgetsExhausted() {
		t.Error("one worker still has a turn remaining")
	}
	r.workers[1].turns = 2
	if !r.workerBudgetsExhausted() {
		t.Error("all worker budgets are exhausted")
	}
	r.workers[0].running = true
	if r.workerBudgetsExhausted() {
		t.Error("an in-flight worker must finish before the budget stops the run")
	}
}
