package clitest

import (
	"os"
	"testing"
)

// A DEVBRAIN_* var in the runner's environment must not reach the child binary:
// a shell-exported DEVBRAIN_PROJECT would reroute the queue the black-box tests
// build and false-red them.
func TestRunScrubsInheritedDevbrainEnv(t *testing.T) {
	t.Setenv("DEVBRAIN_PROJECT", "not__a-real-project")
	h := New(t)
	id := h.Run("todo", "add", "hermetic check").Out()
	if id == "" {
		t.Fatal("add returned empty id")
	}
	if _, err := os.Stat(h.TaskFile(id)); err != nil {
		t.Errorf("task not under %s — inherited DEVBRAIN_PROJECT leaked into the child: %v", h.Project, err)
	}
}

// RequiredToRun is the classifier behind DEVBRAIN_TEST_REQUIRE: a matching keyword
// means a would-be skip is upgraded to a failure, so a CI runner meant to have a
// dependency can't go green while silently skipping the test that needs it.
func TestRequiredToRun(t *testing.T) {
	cases := []struct {
		name    string
		require string // DEVBRAIN_TEST_REQUIRE value ("" = unset)
		keyword string
		want    bool
	}{
		{"unset requires nothing", "", "docker", false},
		{"exact match", "docker", "docker", true},
		{"substring regex match", "docker", "cross-platform-docker", true},
		{"non-match", "docker", "redact", false},
		{"alternation matches", "docker|dogfood", "dogfood", true},
		{"alternation non-match", "docker|dogfood", "todo", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("DEVBRAIN_TEST_REQUIRE", c.require)
			if got := RequiredToRun(c.keyword); got != c.want {
				t.Fatalf("RequiredToRun(%q) with REQUIRE=%q = %v, want %v", c.keyword, c.require, got, c.want)
			}
		})
	}
}
