package nightshift

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// fakeGH puts a `gh` shim on PATH whose body is `script`.
func fakeGH(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestBaseCIGreen: the boot gate is skipped ONLY on a positive green verdict;
// every other answer (pending/failed, no checks, gh error) falls through.
func TestBaseCIGreen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("gh shim is a POSIX shell script")
	}
	cases := []struct {
		name, script string
		want         bool
	}{
		{"green", `echo green`, true},
		{"not green (pending/failed)", `echo notgreen`, false},
		{"no checks ran", `echo none`, false},
		{"gh errors", `exit 1`, false},
		{"gh prints nothing", `exit 0`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fakeGH(t, c.script)
			if got := baseCIGreen(t.TempDir(), "abc1234"); got != c.want {
				t.Errorf("baseCIGreen = %v, want %v", got, c.want)
			}
		})
	}
}

// TestBaseCIGreenEmptySHA: no SHA → never probes, never skips.
func TestBaseCIGreenEmptySHA(t *testing.T) {
	fakeGH(t, `echo green`) // would say green if asked; must not be asked
	if baseCIGreen(t.TempDir(), "") {
		t.Error("baseCIGreen(\"\") = true, want false (must not skip without a SHA)")
	}
}

// TestBaseCIGreenTimesOut: a hanging gh must not stall the boot — the probe
// gives up and falls through to the gate (returns false) within the bound.
func TestBaseCIGreenTimesOut(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("gh shim is a POSIX shell script")
	}
	fakeGH(t, `exec sleep 30`)
	old := ciTrustTimeout
	ciTrustTimeout = 200 * time.Millisecond
	defer func() { ciTrustTimeout = old }()

	start := time.Now()
	if baseCIGreen(t.TempDir(), "abc1234") {
		t.Error("baseCIGreen = true on a hanging gh, want false")
	}
	if el := time.Since(start); el > 5*time.Second {
		t.Errorf("baseCIGreen took %s — did not honor the timeout", el)
	}
}
