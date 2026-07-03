package nightshift_test

// Port of scripts/test-nightshift-gate.sh

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TheWeiHu/devbrain/internal/clitest"
)

func TestNightshiftGate(t *testing.T) {
	h := clitest.New(t)

	binDir := filepath.Join(h.Data, "bin")
	clitest.WriteExec(t, filepath.Join(binDir, "claude"), "#!/usr/bin/env bash\nexit 0\n")
	h.Env["PATH"] = binDir + string(os.PathListSeparator) + os.Getenv("PATH")

	base := filepath.Join(h.Data, "repo")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}

	ns := func(extraEnv map[string]string, args ...string) clitest.Result {
		full := append([]string{"nightshift", "internal"}, args...)
		full = append(full, "--repo", base)
		return h.RunWith(clitest.RunOpts{Env: extraEnv}, full...)
	}
	nsClean := func(args ...string) clitest.Result { return ns(nil, args...) }

	// ── pick_gate_python honors requires-python ──
	t.Run("pick_gate_python", func(t *testing.T) {
		pyp := filepath.Join(base, "pyproject.toml")
		writePyp := func(content string) {
			clitest.WriteFile(t, pyp, fmt.Sprintf("[project]\n%s\n", content))
		}
		cases := []struct {
			content string
			name    string
			wantAny bool // true = want non-empty output, false = want empty
		}{
			{`requires-python = ">=3.99"`, "unsatisfiable floor → none", false},
			{`requires-python = ">=3.0"`, "satisfiable floor → picks one", true},
			{`requires-python = ">=3.0,<3.1"`, "exclusive cap <3.1 → none", false},
			{`requires-python = ">=3.0,<=3.0"`, "inclusive cap <=3.0 → none", false},
			{`requires-python = ">=3.0,<4.0"`, "<4.0 is no real ceiling → picks", true},
			{`requires-python = "==3.99"`, "exact pin ==3.99 → none", false},
			{`requires-python = "~=3.0"`, "compatible-release ~=3.0 → picks", true},
			{`name = "x"`, "no floor declared → picks one", true},
		}
		for _, c := range cases {
			writePyp(c.content)
			r := nsClean("pick-gate-python")
			got := strings.TrimSpace(r.Stdout)
			if c.wantAny && got == "" {
				t.Errorf("pick-gate-python (%s): want a result, got empty", c.name)
			}
			if !c.wantAny && got != "" {
				t.Errorf("pick-gate-python (%s): want empty, got %q", c.name, got)
			}
		}
		_ = os.Remove(pyp)
		r := nsClean("pick-gate-python")
		if strings.TrimSpace(r.Stdout) == "" {
			t.Error("pick-gate-python (no pyproject): want a result, got empty")
		}
	})

	// ── run_gate strips DEVBRAIN_TODO_ONLY so the fixed-set fence can't poison the suite ──
	t.Run("run_gate_strips_env", func(t *testing.T) {
		// test-cmd passes only if both are cleared inside the child.
		testCmd := `[ -z "$DEVBRAIN_TODO_ONLY" ] && [ -z "$DEVBRAIN_TODO_DERIVE_GIT" ]`
		r := ns(
			map[string]string{
				"DEVBRAIN_TODO_ONLY":        "9999-nonexistent",
				"DEVBRAIN_TODO_DERIVE_GIT":  "1",
			},
			"run-gate", h.Data, "--test-cmd", testCmd,
		)
		if r.Code != 0 {
			t.Errorf("gate strips DEVBRAIN_TODO_ONLY + DERIVE_GIT: rc=%d; stdout=%q stderr=%q",
				r.Code, r.Stdout, r.Stderr)
		}
	})

	// ── run_gate retries once so a single flaky test can't RED the base ──
	t.Run("run_gate_retry", func(t *testing.T) {
		gcnt := filepath.Join(h.Data, "gate_attempts")
		clitest.WriteFile(t, gcnt, "")
		// fail 1st attempt (c==0), pass 2nd (c>=1)
		testCmd := fmt.Sprintf(`c=$(wc -c < %q | tr -d ' '); printf x >> %q; [ "$c" -ge 1 ]`, gcnt, gcnt)
		r := nsClean("run-gate", h.Data, "--test-cmd", testCmd)
		if r.Code != 0 {
			t.Errorf("gate retries a one-off flake → pass: rc=%d", r.Code)
		}
		b, _ := os.ReadFile(gcnt)
		if len(b) != 2 {
			t.Errorf("gate ran exactly 2 times (one retry): count = %d, want 2", len(b))
		}

		r = nsClean("run-gate", h.Data, "--test-cmd", "false")
		if r.Code != 1 {
			t.Errorf("persistent failure still FAILs: rc=%d, want 1", r.Code)
		}
	})

	// ── base_gate goes RED only on a real test FAILED, not a collection/import error ──
	t.Run("classify_base", func(t *testing.T) {
		bg := func(args ...string) int {
			full := append([]string{"classify-base"}, args...)
			return nsClean(full...).Code
		}
		if rc := bg("--rc", "1", "--import-error"); rc != 0 {
			t.Errorf("import/collection error is NOT red: rc=%d, want 0", rc)
		}
		if rc := bg("--rc", "1"); rc != 1 {
			t.Errorf("real test FAILED IS red: rc=%d, want 1", rc)
		}
		if rc := bg("--rc", "0"); rc != 0 {
			t.Errorf("passing gate is green: rc=%d, want 0", rc)
		}
		if rc := bg("--rc", "2"); rc != 0 {
			t.Errorf("inconclusive gate is green: rc=%d, want 0", rc)
		}
		if rc := bg("--rc", "1", "--no-gate"); rc != 0 {
			t.Errorf("--no-gate short-circuits green: rc=%d, want 0", rc)
		}
	})

	// ── ci_scope_unsafe: flags a pull_request trigger that fires on per-task PRs ──
	t.Run("ci_scope_unsafe", func(t *testing.T) {
		wf := filepath.Join(h.Data, "wf.yml")
		unsafe := func() bool { return nsClean("ci-scope-unsafe", wf).Code == 0 }

		clitest.WriteFile(t, wf, "name: t\non:\n  pull_request:\n  push:\n    branches: [main]\n")
		if !unsafe() {
			t.Error("bare pull_request → unsafe: expected unsafe (exit 0), got safe")
		}

		clitest.WriteFile(t, wf, "name: t\non:\n  pull_request:\n    branches: [main]\n  push:\n    branches: [main]\n")
		if unsafe() {
			t.Error("pull_request scoped to main → safe: expected safe, got unsafe")
		}

		clitest.WriteFile(t, wf, "on: pull_request\n")
		if !unsafe() {
			t.Error("inline on: pull_request → unsafe: expected unsafe, got safe")
		}

		clitest.WriteFile(t, wf, "on: [push, pull_request]\n")
		if !unsafe() {
			t.Error("inline flow-list pull_request → unsafe: expected unsafe, got safe")
		}

		clitest.WriteFile(t, wf, "on:\n  - push\n  - pull_request\n")
		if !unsafe() {
			t.Error("block-list pull_request → unsafe: expected unsafe, got safe")
		}

		clitest.WriteFile(t, wf, "on:\n  - push\n")
		if unsafe() {
			t.Error("block-list without pull_request → safe: expected safe, got unsafe")
		}

		clitest.WriteFile(t, wf, "on:\n  pull_request:\n    branches:\n      - main\n      - nightshift\n")
		if !unsafe() {
			t.Error("branches include nightshift → unsafe: expected unsafe, got safe")
		}

		clitest.WriteFile(t, wf, "on:\n  push:\n    branches: [main]\n")
		if unsafe() {
			t.Error("no pull_request trigger → safe: expected safe, got unsafe")
		}

		if unsafe() {
			// missing file: nsClean("ci-scope-unsafe", h.Data+"/nope.yml") should be safe (exit 1)
			t.Error("missing workflow file → safe: expected safe (exit 1), got unsafe (exit 0)")
		}
		// Actually test with the missing file directly
		r := nsClean("ci-scope-unsafe", h.Data+"/nope.yml")
		if r.Code == 0 {
			t.Error("missing workflow file → safe: expected safe (exit != 0), got exit 0")
		}

		// The repo's own workflow must be scoped to main.
		root := clitest.Root(t)
		testYML := filepath.Join(root, ".github", "workflows", "test.yml")
		r = nsClean("ci-scope-unsafe", testYML)
		if r.Code == 0 {
			t.Errorf("shipped test.yml must be scoped to main (safe), but ci-scope-unsafe said unsafe")
		}
	})

	// ── fixed-set: a red base must NOT file a fix task ──
	t.Run("fixed_set_no_fix_task", func(t *testing.T) {
		h2 := clitest.New(t)
		h2.Project = "test__repo"
		h2.Env["PATH"] = binDir + string(os.PathListSeparator) + os.Getenv("PATH")
		if err := os.MkdirAll(filepath.Join(h2.Data, "projects", "test__repo", "todo"), 0o755); err != nil {
			t.Fatal(err)
		}

		redCount := func() int {
			r := h2.RunWith(clitest.RunOpts{}, "nightshift", "internal", "todo-all", "list", "all", "--repo", base)
			count := 0
			for _, ln := range strings.Split(r.Stdout, "\n") {
				if strings.Contains(ln, "NIGHTSHIFT IS RED") {
					count++
				}
			}
			return count
		}

		// fixed-set: red base files NO fix task
		h2.RunWith(clitest.RunOpts{}, "nightshift", "internal",
			"ensure-base-fix-task", "--detail", "detail", "--fixed-set", "--repo", base)
		if n := redCount(); n != 0 {
			t.Errorf("fixed-set: red base files NO fix task: count = %d, want 0", n)
		}

		// unbounded: red base files the fix task
		h2.RunWith(clitest.RunOpts{}, "nightshift", "internal",
			"ensure-base-fix-task", "--detail", "detail", "--repo", base)
		if n := redCount(); n != 1 {
			t.Errorf("unbounded: red base files the fix task: count = %d, want 1", n)
		}

		// Dedup must read the WHOLE queue — an ONLY-scoped view hides the existing task.
		h2.RunWith(clitest.RunOpts{}, "nightshift", "internal",
			"ensure-base-fix-task", "--detail", "detail", "--only", "9999-nonexistent", "--repo", base)
		if n := redCount(); n != 1 {
			t.Errorf("dedup sees the whole queue (no duplicate): count = %d, want 1", n)
		}
	})
}
