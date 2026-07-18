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
		timeout bool
	}{
		{"limit hit", "Claude usage limit reached — resets at 3pm\n", 0, false},
		{"normal empty turn", "nothing to do\n", 1, false},
		{"limit hit on a timed-out turn", "out of credit\n", 0, true},
		{"timed-out turn", "still working\n", 1, true},
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
