package nightshift

// PolicyState is the explicit form of the globals pick_turn() reads in the
// bash orchestrator (STALLED / NOMERGE / STALL_K / BASE_RED / BR_ASSIGNED /
// oc / FIXED_SET / now / PLANNED_LAST / REPLAN). Both backends decide a
// worker's next turn through this one function so they can't drift apart.
type PolicyState struct {
	Stalled     bool  `json:"stalled"`      // STALLED — the fleet has gone quiet
	NoMerge     int   `json:"nomerge"`      // NOMERGE — consecutive turns with no new merge
	StallK      int   `json:"stall_k"`      // STALL_K — stall threshold
	BaseRed     bool  `json:"base_red"`     // BASE_RED — origin/nightshift fails its own suite
	BRAssigned  int   `json:"br_assigned"`  // BR_ASSIGNED — workers fed this poll
	Open        int   `json:"open"`         // oc — open task count
	FixedSet    bool  `json:"fixed_set"`    // FIXED_SET — --only run, never plans
	Now         int64 `json:"now"`          // now — current epoch
	PlannedLast int64 `json:"planned_last"` // PLANNED_LAST — epoch of the last planning turn
	Replan      int64 `json:"replan"`       // REPLAN — min gap between planning turns
}

// Pick values a turn decision can take. The orchestrator maps PickWork to the
// /work prompt and PickPlan to the planning rules; PickNone parks the worker.
const (
	PickWork = "work"
	PickPlan = "plan"
	PickNone = ""
)

// Decision is pick_turn's outcome: the turn to launch plus the mutated
// throttles (the bash function updates BR_ASSIGNED / PLANNED_LAST in place;
// here they come back explicitly so the function stays pure).
type Decision struct {
	Pick        string `json:"pick"`
	BRAssigned  int    `json:"br_assigned"`
	PlannedLast int64  `json:"planned_last"`
}

// PickTurn ports pick_turn()'s decision tree. Each branch carries the
// script's own comment.
func PickTurn(s PolicyState) Decision {
	d := Decision{Pick: PickNone, BRAssigned: s.BRAssigned, PlannedLast: s.PlannedLast}
	// gone quiet → no new work
	if s.Stalled || s.NoMerge >= s.StallK {
		return d
	}
	// red base → one fixer/cycle
	if s.BaseRed && s.BRAssigned >= 1 {
		return d
	}
	if s.BRAssigned < s.Open { // one worker per open task
		d.Pick = PickWork
		d.BRAssigned++
		return d
	}
	if s.Open == 0 && !s.FixedSet && s.Now-s.PlannedLast > s.Replan {
		// queue empty — replenish (one plan per REPLAN window, fleet-wide)
		d.Pick = PickPlan
		d.PlannedLast = s.Now
		return d
	}
	// else: capped, fixed-set, or planned recently → park
	// (fixed-set wind-down is the main loop's job, not the policy's)
	return d
}

// AssignRound runs one poll's assignment pass over `workers` idle workers —
// the hl_step idle path with the launch stubbed out. It returns the indices
// that would get a turn this round (at most one worker per open task; a
// planning turn also occupies a slot). Pins the Bug 1a fix: open=1 with 8
// idle workers assigns exactly one.
func AssignRound(s PolicyState, workers int) []int {
	assigned := []int{}
	for i := 0; i < workers; i++ {
		d := PickTurn(s)
		s.BRAssigned, s.PlannedLast = d.BRAssigned, d.PlannedLast
		if d.Pick != PickNone {
			assigned = append(assigned, i)
		}
	}
	return assigned
}
