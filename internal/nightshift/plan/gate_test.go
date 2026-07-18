package plan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseRequiresPython(t *testing.T) {
	cases := []struct {
		req    string
		lo, hi int
	}{
		{`requires-python = ">=3.99"`, 99, 99},
		{`requires-python = ">=3.0"`, 0, 99},
		{`requires-python = ">=3.0,<3.1"`, 0, 1},
		{`requires-python = ">=3.0,<=3.0"`, 0, 1}, // inclusive cap → exclusive ceiling +1
		{`requires-python = ">=3.0,<4.0"`, 0, 99}, // <4.0 is no real 3.x ceiling
		{`requires-python = "==3.99"`, 99, 100},   // exact pin → one minor
		{`requires-python = "~=3.0"`, 0, 99},      // compatible-release ≈ floor only
		{`requires-python = ">=3.11,<3.13"`, 11, 13},
		{``, 0, 99}, // no constraint
	}
	for _, c := range cases {
		lo, hi := ParseRequiresPython(c.req)
		if lo != c.lo || hi != c.hi {
			t.Errorf("%q: got [%d,%d) want [%d,%d)", c.req, lo, hi, c.lo, c.hi)
		}
	}
}

func TestFindInterpreter(t *testing.T) {
	// Fake PATH with python3.9 / python3.12 stubs; the version probe is
	// injected so no real interpreter runs.
	dir := t.TempDir()
	for _, n := range []string{"python3.9", "python3.12", "python3"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	orig := pyMinorSatisfies
	defer func() { pyMinorSatisfies = orig }()
	pyMinorSatisfies = func(interp string, lo, hi int) bool {
		minor := map[string]int{"python3.9": 9, "python3.12": 12, "python3": 9}[interp]
		return minor >= lo && minor < hi
	}

	cases := []struct {
		name   string
		lo, hi int
		want   string
	}{
		{"no constraint picks lowest minor first", 0, 99, "python3.9"},
		{"floor skips too-old minors", 10, 99, "python3.12"},
		{"cap below floor → none", 20, 99, ""},
		{"window selects the eligible minor", 10, 13, "python3.12"},
		{"cap excludes everything → none", 0, 9, ""},
	}
	for _, c := range cases {
		if got := FindInterpreter(c.lo, c.hi); got != c.want {
			t.Errorf("%s: FindInterpreter(%d,%d) = %q want %q", c.name, c.lo, c.hi, got, c.want)
		}
	}
}

func TestClassifyPytest(t *testing.T) {
	failedOut := "x\nFAILED tests/test_a.py::t1 - boom\nFAILED tests/test_a.py::t2\n1 failed"
	errorOut := "x\nERROR tests/test_b.py - ImportError: no module\n1 error"
	mixedOut := "FAILED tests/t.py::a\nERROR tests/t.py::b\ndone"
	cases := []struct {
		name      string
		rc        int
		out       string
		wantRC    int
		importErr bool
	}{
		{"rc 0 → pass", 0, "5 passed", GatePass, false},
		{"rc 5 → inconclusive (no tests)", 5, "no tests ran", GateInconclusive, false},
		{"rc 1 + FAILED → fail", 1, failedOut, GateFail, false},
		{"rc 1 ERROR-without-FAILED → import error", 1, errorOut, GateFail, true},
		{"rc 1 ERROR+FAILED → real fail, not import", 1, mixedOut, GateFail, false},
		{"rc 2 → fail + import error", 2, "usage error", GateFail, true},
		{"rc 124 (timeout) → inconclusive", 124, "", GateInconclusive, false},
		{"rc 3 → inconclusive", 3, "internal error", GateInconclusive, false},
	}
	for _, c := range cases {
		got := ClassifyPytest(c.rc, c.out)
		if got.RC != c.wantRC || got.ImportError != c.importErr {
			t.Errorf("%s: got rc=%d import=%v want rc=%d import=%v",
				c.name, got.RC, got.ImportError, c.wantRC, c.importErr)
		}
	}

	// Detail: FAILED/ERROR heads (max 4) win; fallback is the last 3 lines.
	if d := ClassifyPytest(1, failedOut).Detail; !strings.Contains(d, "FAILED tests/test_a.py::t1") {
		t.Errorf("detail should carry FAILED heads, got %q", d)
	}
	if d := ClassifyPytest(1, "a\nb\nc\nd\ne").Detail; d != "c d e" {
		t.Errorf("fallback detail = %q want last 3 lines", d)
	}
	long := strings.Repeat("y", 500)
	if d := ClassifyPytest(1, long).Detail; len(d) != 240 {
		t.Errorf("fallback detail should cut at 240, got %d", len(d))
	}
}

func TestClassifyBase(t *testing.T) {
	cases := []struct {
		name   string
		res    GateResult
		noGate bool
		red    bool
	}{
		{"import/collection error is NOT red", GateResult{RC: GateFail, ImportError: true}, false, false},
		{"real test FAILED IS red", GateResult{RC: GateFail}, false, true},
		{"passing gate is green", GateResult{RC: GatePass}, false, false},
		{"inconclusive gate is green", GateResult{RC: GateInconclusive}, false, false},
		{"no-gate short-circuits green", GateResult{RC: GateFail}, true, false},
	}
	for _, c := range cases {
		if got := ClassifyBase(c.res, c.noGate); got != c.red {
			t.Errorf("%s: got red=%v want %v", c.name, got, c.red)
		}
	}
}

func TestCauseFingerprint(t *testing.T) {
	base := "FAILED /tmp/pytest-123/tests/test_a.py::test_x - AssertionError at 0x7f01"
	// Run-to-run noise (tmp paths, addresses, counters) must not change the key.
	same := "FAILED /tmp/pytest-987/tests/test_a.py::test_x - AssertionError at 0x9bcd"
	if CauseFingerprint(base) != CauseFingerprint(same) {
		t.Errorf("same failure fingerprints differently: %s vs %s", CauseFingerprint(base), CauseFingerprint(same))
	}
	if CauseFingerprint(base) == CauseFingerprint("FAILED tests/test_b.py::test_y - TypeError") {
		t.Error("different failures must fingerprint differently")
	}
	if CauseFingerprint("") == "" {
		t.Error("empty detail must still fingerprint")
	}
}
