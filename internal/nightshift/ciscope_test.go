package nightshift

import (
	"os"
	"path/filepath"
	"testing"
)

// The fixture cases are lifted verbatim from test-nightshift-gate.sh — that
// bash test is the spec for the awk state machine this ports.
func TestCIScopeUnsafe(t *testing.T) {
	cases := []struct {
		name   string
		yaml   string
		unsafe bool
	}{
		{"bare pull_request → unsafe",
			"name: t\non:\n  pull_request:\n  push:\n    branches: [main]", true},
		{"pull_request scoped to main → safe",
			"name: t\non:\n  pull_request:\n    branches: [main]\n  push:\n    branches: [main]", false},
		{"inline on: pull_request → unsafe",
			"on: pull_request", true},
		{"inline flow-list pull_request → unsafe",
			"on: [push, pull_request]", true},
		{"block-list pull_request → unsafe",
			"on:\n  - push\n  - pull_request", true},
		{"block-list without pull_request → safe",
			"on:\n  - push", false},
		{"branches include nightshift → unsafe",
			"on:\n  pull_request:\n    branches:\n      - main\n      - nightshift", true},
		{"no pull_request trigger → safe",
			"on:\n  push:\n    branches: [main]", false},
		{"comments and blanks are ignored",
			"on:\n  # a comment\n\n  pull_request:  # trailing comment\n    branches: [main]", false},
		{"flow branches with nightshift → unsafe",
			"on:\n  pull_request:\n    branches: [main, nightshift]", true},
		{"pull_request at EOF without branches → unsafe",
			"on:\n  push:\n    branches: [main]\n  pull_request:", true},
	}
	for _, c := range cases {
		if got := ciScopeUnsafeYAML(c.yaml); got != c.unsafe {
			t.Errorf("%s: got unsafe=%v want %v", c.name, got, c.unsafe)
		}
	}
}

func TestCIScopeUnsafeFiles(t *testing.T) {
	if CIScopeUnsafe(filepath.Join(t.TempDir(), "nope.yml")) {
		t.Error("missing workflow file must be safe")
	}
	p := filepath.Join(t.TempDir(), "wf.yml")
	os.WriteFile(p, []byte("on: pull_request\n"), 0o644)
	if !CIScopeUnsafe(p) {
		t.Error("inline pull_request file must be unsafe")
	}
	// The repo's own workflow must be scoped (regression guard for the shipped fix).
	shipped := filepath.Join("..", "..", ".github", "workflows", "test.yml")
	if _, err := os.Stat(shipped); err == nil && CIScopeUnsafe(shipped) {
		t.Error("shipped test.yml must be scoped to main")
	}
}
