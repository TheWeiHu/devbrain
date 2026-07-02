package nightshift

import (
	"reflect"
	"testing"
)

// base returns the healthy-fleet state the branches deviate from.
func base() PolicyState {
	return PolicyState{StallK: 8, Open: 3, Now: 1000, PlannedLast: 0, Replan: 300}
}

func TestPickTurn(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*PolicyState)
		want Decision
	}{
		{"open work → work", func(s *PolicyState) {},
			Decision{Pick: PickWork, BRAssigned: 1, PlannedLast: 0}},
		{"work bumps br_assigned from prior", func(s *PolicyState) { s.BRAssigned = 1 },
			Decision{Pick: PickWork, BRAssigned: 2, PlannedLast: 0}},
		{"assignments capped at open count", func(s *PolicyState) { s.BRAssigned = 3 },
			Decision{Pick: PickNone, BRAssigned: 3, PlannedLast: 0}},
		{"stalled → park", func(s *PolicyState) { s.Stalled = true },
			Decision{Pick: PickNone, BRAssigned: 0, PlannedLast: 0}},
		{"nomerge at stall_k → park", func(s *PolicyState) { s.NoMerge = 8 },
			Decision{Pick: PickNone, BRAssigned: 0, PlannedLast: 0}},
		{"nomerge below stall_k still works", func(s *PolicyState) { s.NoMerge = 7 },
			Decision{Pick: PickWork, BRAssigned: 1, PlannedLast: 0}},
		{"red base, first worker → work", func(s *PolicyState) { s.BaseRed = true },
			Decision{Pick: PickWork, BRAssigned: 1, PlannedLast: 0}},
		{"red base, already fed one → park", func(s *PolicyState) { s.BaseRed = true; s.BRAssigned = 1 },
			Decision{Pick: PickNone, BRAssigned: 1, PlannedLast: 0}},
		{"empty queue → plan + stamp cooldown", func(s *PolicyState) { s.Open = 0 },
			Decision{Pick: PickPlan, BRAssigned: 0, PlannedLast: 1000}},
		{"replan cooldown → park", func(s *PolicyState) { s.Open = 0; s.PlannedLast = 900 },
			Decision{Pick: PickNone, BRAssigned: 0, PlannedLast: 900}},
		{"cooldown boundary (now-last == replan) → park", func(s *PolicyState) { s.Open = 0; s.PlannedLast = 700 },
			Decision{Pick: PickNone, BRAssigned: 0, PlannedLast: 700}},
		{"fixed-set + empty → park, never plans", func(s *PolicyState) { s.Open = 0; s.FixedSet = true },
			Decision{Pick: PickNone, BRAssigned: 0, PlannedLast: 0}},
	}
	for _, c := range cases {
		s := base()
		c.mut(&s)
		if got := PickTurn(s); got != c.want {
			t.Errorf("%s: got %+v want %+v", c.name, got, c.want)
		}
	}
}

func TestAssignRound(t *testing.T) {
	cases := []struct {
		name    string
		open    int
		workers int
		want    []int
	}{
		{"open=1, 8 idle → exactly 1", 1, 8, []int{0}},
		{"open=3, 8 idle → exactly 3", 3, 8, []int{0, 1, 2}},
		{"open=0 fixed-set → none", 0, 8, []int{}},
	}
	for _, c := range cases {
		s := PolicyState{StallK: 8, Open: c.open, FixedSet: true, Now: 1000, PlannedLast: 1000, Replan: 300}
		if got := AssignRound(s, c.workers); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}
