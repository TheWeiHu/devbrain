package nightshift_test

// Port of scripts/test-nightshift-policy.sh

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TheWeiHu/devbrain/internal/clitest"
)

func TestNightshiftPolicy(t *testing.T) {
	h := clitest.New(t)

	binDir := filepath.Join(h.Data, "bin")
	clitest.WriteExec(t, filepath.Join(binDir, "claude"), "#!/usr/bin/env bash\nexit 0\n")
	h.Env["PATH"] = binDir + string(os.PathListSeparator) + os.Getenv("PATH")

	base := filepath.Join(h.Data, "repo")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	clitest.Git(t, "", "init", "-q", base)
	clitest.Git(t, base, "remote", "add", "origin", "git@github.com:test/repo.git")

	// now=1000; REPLAN=300; STALL_K=8 throughout (matches the script).
	type policyOut struct {
		Pick        string `json:"pick"`
		BrAssigned  int    `json:"br_assigned"`
		PlannedLast int64  `json:"planned_last"`
	}

	// pt: stalled, nomerge, base_red, br_assigned, open, fixed_set, planned_last (default 0)
	pt := func(stalled bool, nomerge int, baseRed bool, brAssigned, open int, fixedSet bool, plannedLast int64) policyOut {
		t.Helper()
		state, _ := json.Marshal(map[string]interface{}{
			"stalled":      stalled,
			"nomerge":      nomerge,
			"stall_k":      8,
			"base_red":     baseRed,
			"br_assigned":  brAssigned,
			"open":         open,
			"fixed_set":    fixedSet,
			"now":          1000,
			"planned_last": plannedLast,
			"replan":       300,
		})
		r := h.RunWith(clitest.RunOpts{}, "nightshift", "internal", "pick-turn",
			"--state", string(state), "--repo", base)
		var out policyOut
		if err := json.Unmarshal([]byte(strings.TrimSpace(r.Stdout)), &out); err != nil {
			t.Fatalf("pick-turn output not JSON: %v; stdout=%q", err, r.Stdout)
		}
		return out
	}

	// open work → assign /work, and bump the per-open-task assignment counter
	out := pt(false, 0, false, 0, 3, false, 0)
	if out.Pick != "work" {
		t.Errorf("open work: pick = %q, want work", out.Pick)
	}
	if out.BrAssigned != 1 {
		t.Errorf("/work must bump br_assigned to 1, got %d", out.BrAssigned)
	}

	// one worker per open task: once this poll's assignments reach oc, the rest park
	out = pt(false, 0, false, 3, 3, false, 0)
	if out.Pick != "" {
		t.Errorf("assignments capped at open count: pick = %q, want empty (park)", out.Pick)
	}

	// gone quiet: STALLED → park
	out = pt(true, 0, false, 0, 3, false, 0)
	if out.Pick != "" {
		t.Errorf("STALLED: pick = %q, want empty (park)", out.Pick)
	}

	// gone quiet: NOMERGE >= STALL_K → park
	out = pt(false, 8, false, 0, 3, false, 0)
	if out.Pick != "" {
		t.Errorf("NOMERGE>=STALL_K: pick = %q, want empty (park)", out.Pick)
	}

	// red base: feed exactly ONE fixer per cycle
	out = pt(false, 0, true, 0, 3, false, 0)
	if out.Pick != "work" {
		t.Errorf("red base first worker: pick = %q, want work", out.Pick)
	}
	out = pt(false, 0, true, 1, 3, false, 0)
	if out.Pick != "" {
		t.Errorf("red base already fed one: pick = %q, want empty (park)", out.Pick)
	}

	// empty queue + forever mode: plan to replenish, and stamp the cooldown
	out = pt(false, 0, false, 0, 0, false, 0)
	if out.Pick != "plan" {
		t.Errorf("empty queue: pick = %q, want plan", out.Pick)
	}
	if out.PlannedLast != 1000 {
		t.Errorf("planning must stamp planned_last=1000, got %d", out.PlannedLast)
	}

	// planned within REPLAN seconds → park
	out = pt(false, 0, false, 0, 0, false, 900)
	if out.Pick != "" {
		t.Errorf("replan cooldown: pick = %q, want empty (park)", out.Pick)
	}

	// fixed-set + empty queue → NEVER plan; just park
	out = pt(false, 0, false, 0, 0, true, 0)
	if out.Pick != "" {
		t.Errorf("fixed-set + empty: pick = %q, want empty (never plans)", out.Pick)
	}
}
