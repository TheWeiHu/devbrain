package nightshift_test

// Port of scripts/test-nightshift-statelock.sh

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TheWeiHu/devbrain/internal/clitest"
)

func TestNightshiftStatelock(t *testing.T) {
	h := clitest.New(t)
	h.Project = "test__repo"

	binDir := filepath.Join(h.Data, "bin")
	clitest.WriteExec(t, filepath.Join(binDir, "claude"), "#!/usr/bin/env bash\nexit 0\n")
	h.Env["PATH"] = binDir + string(os.PathListSeparator) + os.Getenv("PATH")

	rem := filepath.Join(h.Data, "rem.git")
	seed := filepath.Join(h.Data, "seed")
	base := filepath.Join(h.Data, "clone")

	clitest.Git(t, "", "init", "-q", "--bare", rem)
	clitest.Git(t, "", "clone", "-q", rem, seed)
	clitest.WriteFile(t, filepath.Join(seed, "f"), "base\n")
	clitest.Git(t, seed, "add", "f")
	clitest.Git(t, seed, "commit", "-qm", "init")
	clitest.Git(t, seed, "push", "-q", "origin", "HEAD:main")
	clitest.Git(t, seed, "checkout", "-q", "-b", "nightshift")
	clitest.Git(t, seed, "push", "-q", "origin", "nightshift")
	clitest.Git(t, "", "clone", "-q", rem, base)

	td := filepath.Join(h.Data, "projects", "test__repo", "todo")
	if err := os.MkdirAll(td, 0o755); err != nil {
		t.Fatal(err)
	}

	// NIGHTSHIFT_TEST_NO_LAUNCH skips backfill (scans real ~/.claude transcripts).
	ns := func(args ...string) clitest.Result {
		full := append([]string{"nightshift", "internal"}, args...)
		full = append(full, "--repo", base, "--no-gate")
		return h.RunWith(clitest.RunOpts{
			Dir: base,
			Env: map[string]string{"NIGHTSHIFT_TEST_NO_LAUNCH": "1"},
		}, full...)
	}

	nsShow := func(id string) string {
		t.Helper()
		r := h.RunWith(clitest.RunOpts{Dir: base}, "todo", "show", id)
		return r.Stdout
	}
	nsStatus := func(id string) string {
		t.Helper()
		return clitest.Field(nsShow(id), "status")
	}

	// ── Bug 1b — an empty turn (no commit) is not counted as landed ──
	t.Run("empty_turn", func(t *testing.T) {
		wt0 := filepath.Join(h.Data, "wt-empty")
		clitest.Git(t, "", "clone", "-q", rem, wt0)
		clitest.Git(t, wt0, "checkout", "-q", "-B", "todo/0099-eee", "origin/nightshift")
		b0 := nsRevParse(t, wt0, "HEAD")

		// No commits since fork base → empty turn.
		r := ns("turn-made-commits", wt0, b0)
		if r.Code == 0 {
			t.Error("no commits since fork base: want non-zero (empty turn), got 0")
		}

		// One commit since fork base → real turn.
		clitest.WriteFile(t, filepath.Join(wt0, "essay-0099"), "work\n")
		clitest.Git(t, wt0, "add", "essay-0099")
		clitest.Git(t, wt0, "commit", "-qm", "essay 0099")
		r = ns("turn-made-commits", wt0, b0)
		if r.Code != 0 {
			t.Errorf("one commit since fork base: want 0 (real turn), got %d", r.Code)
		}

		// Missing fork base → cannot prove empty.
		r = ns("turn-made-commits", wt0, "")
		if r.Code != 0 {
			t.Errorf("missing fork base: want 0 (cannot prove empty), got %d", r.Code)
		}
	})

	// ── Bug 2 — shutdown releases EVERY taken task in scope ──
	t.Run("shutdown_release", func(t *testing.T) {
		fresh := time.Now().UTC().Format("2006-01-02T15:04:05Z")
		for _, id := range []string{"0101-fff", "0102-ggg", "0103-hhh"} {
			clitest.WriteFile(t, filepath.Join(td, id+".md"),
				nsTaskFrontmatter(id, "taken", fmt.Sprintf("in-flight %s", id), "w@h", fresh, ""))
		}

		// Count taken before shutdown.
		r := h.RunWith(clitest.RunOpts{Dir: base}, "todo", "list", "taken")
		taken := 0
		for _, ln := range strings.Split(r.Stdout, "\n") {
			if strings.Contains(ln, "010") &&
				(strings.Contains(ln, "0101") || strings.Contains(ln, "0102") || strings.Contains(ln, "0103")) {
				taken++
			}
		}
		if taken != 3 {
			t.Errorf("three tasks taken before shutdown, found %d in list taken", taken)
		}

		// Drive the orchestrator's shutdown reaper.
		ns("cleanup", "--workers", "3")

		for _, id := range []string{"0101-fff", "0102-ggg", "0103-hhh"} {
			if got := nsStatus(id); got != "open" {
				t.Errorf("%s released to open on shutdown: status = %q, want open", id, got)
			}
		}

		// No task left taken.
		r = h.RunWith(clitest.RunOpts{Dir: base}, "todo", "list", "taken")
		for _, ln := range strings.Split(r.Stdout, "\n") {
			if strings.Contains(ln, "0101") || strings.Contains(ln, "0102") || strings.Contains(ln, "0103") {
				t.Errorf("task still taken after shutdown: %q", ln)
			}
		}

		// A HELD task (merge hit retry cap) must SURVIVE shutdown.
		clitest.WriteFile(t, filepath.Join(td, "0104-iii.md"),
			nsTaskFrontmatter("0104-iii", "held", "held", "", "",
				"reason: gate failed (after 2 attempts)\n"))
		ns("cleanup", "--workers", "3")
		if got := nsStatus("0104-iii"); got != "held" {
			t.Errorf("held task survives shutdown: status = %q, want held", got)
		}
	})

	// ── Bug 1a — at most ONE worker is assigned per open task in a poll ──
	t.Run("assign_round", func(t *testing.T) {
		countWords := func(s string) int {
			fields := strings.Fields(strings.TrimSpace(s))
			return len(fields)
		}

		r := ns("assign-round", "--open", "1", "--workers", "8", "--fixed-set")
		if n := countWords(r.Stdout); n != 1 {
			t.Errorf("open=1, 8 idle: expected exactly 1 assigned, got %d; stdout=%q", n, r.Stdout)
		}

		r = ns("assign-round", "--open", "3", "--workers", "8", "--fixed-set")
		if n := countWords(r.Stdout); n != 3 {
			t.Errorf("open=3, 8 idle: expected exactly 3 assigned, got %d; stdout=%q", n, r.Stdout)
		}

		r = ns("assign-round", "--open", "0", "--workers", "8", "--fixed-set")
		if strings.TrimSpace(r.Stdout) != "" {
			t.Errorf("open=0 (fixed-set): expected none assigned, got %q", r.Stdout)
		}
	})
}

// nsTaskFrontmatter builds a minimal task markdown file.
func nsTaskFrontmatter(id, status, title, claimedBy, claimedAt, extra string) string {
	return "---\nid: " + id + "\nstatus: " + status + "\npriority: 50\n" +
		"created: 2026-06-20T00:00:00Z\nclaimed_by: " + claimedBy + "\nclaimed_at: " + claimedAt +
		"\npr:\n" + extra + "---\n# " + title + "\n"
}
