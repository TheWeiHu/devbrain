package todo_test

// Go-native port of scripts/test-todo.sh: the todo CLI's black-box contract
// (args in → stdout / exit-code / on-disk files out), driven through the built
// binary via the shared clitest harness. Reference for the rest of the suite.

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/TheWeiHu/devbrain/internal/clitest"
)

// listIDs pulls the ids, in order, from an open-list body ("  [ 90] <id>  <title>").
func listIDs(out string) []string {
	var ids []string
	for _, ln := range strings.Split(out, "\n") {
		if strings.HasPrefix(ln, "  [") {
			if f := strings.Fields(ln[strings.Index(ln, "]")+1:]); len(f) > 0 {
				ids = append(ids, f[0])
			}
		}
	}
	return ids
}

func TestTodoCLI(t *testing.T) {
	h := clitest.New(t)
	run := func(a ...string) clitest.Result { return h.Run(append([]string{"todo"}, a...)...) }
	runWith := func(o clitest.RunOpts, a ...string) clitest.Result {
		return h.RunWith(o, append([]string{"todo"}, a...)...)
	}
	add := func(title string, args ...string) string {
		return run(append([]string{"add", title}, args...)...).Out()
	}
	field := func(id, key string) string { return clitest.Field(run("show", id).Stdout, key) }

	a := add("high priority task", "-p", "90")
	b := add("low chore", "-p", "10")
	c := add("mid task", "-p", "50")

	t.Run("add+ordering", func(t *testing.T) {
		if a == "" || b == "" || c == "" {
			t.Fatalf("add returned empty id(s): %q %q %q", a, b, c)
		}
		if got := run("next").Out(); got != a {
			t.Errorf("next = %q, want highest-priority %q", got, a)
		}
		if got := listIDs(run("list").Stdout); strings.Join(got, " ") != strings.Join([]string{a, c, b}, " ") {
			t.Errorf("list order = %v, want p90,p50,p10 %v", got, []string{a, c, b})
		}
	})

	t.Run("claim/release/done lifecycle", func(t *testing.T) {
		run("claim", a)
		if got := field(a, "status"); got != "taken" {
			t.Errorf("claim -> status %q, want taken", got)
		}
		if field(a, "claimed_at") == "" {
			t.Error("claim did not stamp claimed_at")
		}
		if got := run("next").Out(); got != c {
			t.Errorf("next after claim = %q, want %q (skips taken)", got, c)
		}
		if code := run("claim", a).Code; code != 2 {
			t.Errorf("re-claim taken exit = %d, want 2", code)
		}
		run("release", a)
		if got := field(a, "status"); got != "open" {
			t.Errorf("release -> status %q, want open", got)
		}
		if field(a, "claimed_at") != "" {
			t.Error("release did not clear claimed_at")
		}
		if code := run("done", a).Code; code == 0 {
			t.Error("done without a recorded pr exited 0")
		}
		if got := field(a, "status"); got != "open" {
			t.Errorf("refused done still changed status to %q", got)
		}
		run("done", a, "--force")
		if got := field(a, "status"); got != "done" {
			t.Errorf("done --force -> status %q, want done", got)
		}
		iso := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`)
		if got := field(a, "done_at"); !iso.MatchString(got) {
			t.Errorf("done_at = %q, want UTC ISO-8601", got)
		}
		if field(c, "done_at") != "" {
			t.Error("open task carries a done_at")
		}
		if got := run("next").Out(); got != c {
			t.Errorf("next after done = %q, want %q", got, c)
		}
		if strings.Contains(run("list").Stdout, a) {
			t.Error("list shows a done task")
		}
		// `done` is terminal: release must NOT reopen it (watchdog-requeue race).
		run("release", a)
		if got := field(a, "status"); got != "done" {
			t.Errorf("release reopened a done task: status %q", got)
		}
	})

	t.Run("review status", func(t *testing.T) {
		run("claim", c)
		if code := run("review", c).Code; code == 0 {
			t.Error("review without a pr ref exited 0")
		}
		run("review", c, "42")
		if got := field(c, "status"); got != "review" {
			t.Errorf("review -> status %q, want review", got)
		}
		if got := field(c, "pr"); got != "42" {
			t.Errorf("review pr = %q, want 42", got)
		}
		if got := run("next").Out(); got != b {
			t.Errorf("next skips review = %q, want %q", got, b)
		}
		if strings.Contains(run("list").Stdout, c) {
			t.Error("list shows a review task")
		}
		run("release", c)
		if got := field(c, "status"); got != "open" {
			t.Errorf("release review -> status %q, want open", got)
		}
	})

	t.Run("review inserts missing pr field", func(t *testing.T) {
		old := add("legacy task", "-p", "5")
		stripLine(t, h.TaskFile(old), "pr:") // simulate a task created before pr: existed
		run("review", old, "7")
		if got := field(old, "pr"); got != "7" {
			t.Errorf("review on legacy task pr = %q, want 7", got)
		}
		if code := run("done", old).Code; code != 0 {
			t.Errorf("done with a recorded pr exited %d, want 0", code)
		}
	})

	t.Run("list status filter", func(t *testing.T) {
		// State: a=done, b=open, c=open, old=done. Put b into review.
		run("review", b, "99")
		open := run("list").Stdout
		if !strings.Contains(open, c) || strings.Contains(open, b) || strings.Contains(open, a) {
			t.Errorf("default list is not open-only:\n%s", open)
		}
		rev := run("list", "review").Stdout
		if !strings.Contains(rev, b) || strings.Contains(rev, c) {
			t.Errorf("list review wrong set:\n%s", rev)
		}
		if !clitest.HasLineWith(rev, b, "review") {
			t.Errorf("list review row omits status:\n%s", rev)
		}
		if done := run("list", "done").Stdout; !strings.Contains(done, a) || strings.Contains(done, c) {
			t.Errorf("list done wrong set:\n%s", done)
		}
		all := run("list", "all").Stdout
		if !strings.Contains(all, a) || !strings.Contains(all, b) || !strings.Contains(all, c) {
			t.Errorf("list all missing rows:\n%s", all)
		}
		if code := run("list", "bogus").Code; code == 0 {
			t.Error("list with a bad status exited 0")
		}
		if got := run("next").Out(); got != c {
			t.Errorf("next still open-only = %q, want %q", got, c)
		}
	})

	t.Run("context body", func(t *testing.T) {
		runWith(clitest.RunOpts{Stdin: "line one\nline two\n"}, "context", b)
		show := run("show", b).Stdout
		if !strings.Contains(show, "## Context (synthesized ") {
			t.Error("context did not add a ## Context section")
		}
		if !containsLine(show, "line two") {
			t.Error("context dropped body lines")
		}
		runWith(clitest.RunOpts{Stdin: "fresh only\n"}, "context", b)
		show = run("show", b).Stdout
		if strings.Count(show, "## Context (synthesized ") != 1 {
			t.Error("context did not replace the prior block")
		}
		if !containsLine(show, "fresh only") || containsLine(show, "line two") {
			t.Error("context kept stale body lines")
		}
		if code := runWith(clitest.RunOpts{Stdin: ""}, "context", b).Code; code == 0 {
			t.Error("context with empty stdin exited 0")
		}
	})

	t.Run("self-heal zombie sweep", func(t *testing.T) {
		// Offline PR-state oracle: any ref containing MERGED reports merged.
		fake := filepath.Join(h.Data, "fake-pr-state")
		clitest.WriteExec(t, fake, "#!/usr/bin/env bash\ncase \"$1\" in *MERGED*) echo MERGED;; *) echo OPEN;; esac\n")
		h.Env["DEVBRAIN_PR_STATE_CMD"] = fake

		setpr := func(id, pr string) { setField(t, h.TaskFile(id), "pr", pr) }
		z1 := add("merged open zombie")
		setpr(z1, "PR-MERGED-1")
		z2 := add("open with live PR")
		setpr(z2, "PR-OPEN-2")
		z3 := add("open no PR")
		z4 := add("merged taken zombie")
		setpr(z4, "PR-MERGED-4")
		run("claim", z4)

		run("self-heal")
		if got := field(z1, "status"); got != "done" {
			t.Errorf("self-heal merged-open -> %q, want done", got)
		}
		if field(z1, "done_at") == "" {
			t.Error("self-heal did not stamp done_at")
		}
		if got := field(z4, "status"); got != "done" {
			t.Errorf("self-heal merged-taken -> %q, want done", got)
		}
		if got := field(z2, "status"); got != "open" {
			t.Errorf("self-heal closed a live-PR task -> %q", got)
		}
		if got := field(z3, "status"); got != "open" {
			t.Errorf("self-heal touched a no-PR task -> %q", got)
		}

		// release/hold clear pr + reason so a reopened task can't be re-zombied.
		zr := add("reopen clears pr")
		run("review", zr, "PR-MERGED-R")
		run("release", zr)
		if field(zr, "pr") != "" {
			t.Error("release did not clear pr")
		}
		zh := add("release clears hold note")
		run("hold", zh, "parked for some reason")
		run("release", zh)
		if field(zh, "reason") != "" {
			t.Error("release did not clear reason")
		}
		run("self-heal")
		if got := field(zr, "status"); got != "open" {
			t.Errorf("self-heal re-zombied a reopened task -> %q", got)
		}
	})
}

// ── file-local helpers ──

func stripLine(t *testing.T, path, prefix string) {
	t.Helper()
	var keep []string
	for _, ln := range strings.Split(clitest.Read(t, path), "\n") {
		if !strings.HasPrefix(ln, prefix) {
			keep = append(keep, ln)
		}
	}
	clitest.WriteFile(t, path, strings.Join(keep, "\n"))
}

func setField(t *testing.T, path, key, val string) {
	t.Helper()
	lines := strings.Split(clitest.Read(t, path), "\n")
	for i, ln := range lines {
		if strings.HasPrefix(ln, key+":") {
			lines[i] = key + ": " + val
		}
	}
	clitest.WriteFile(t, path, strings.Join(lines, "\n"))
}

func countLines(text, want string) int {
	n := 0
	for _, ln := range strings.Split(text, "\n") {
		if ln == want {
			n++
		}
	}
	return n
}

func containsLine(s, line string) bool {
	for _, ln := range strings.Split(s, "\n") {
		if ln == line {
			return true
		}
	}
	return false
}

// TestTodoAddConcurrentUniquePrefixes reproduces the production duplicate-id
// bug at the process level: N parallel `todo add` calls with DISTINCT slugs
// used to race the max-NNNN scan and each win its own O_EXCL create, leaving
// two files per prefix (0240, 0252, 0281 … in one real queue). The prefix,
// not the file name, must be the unit of exclusivity.
func TestTodoAddConcurrentUniquePrefixes(t *testing.T) {
	h := clitest.New(t)
	const n = 24
	ids := make([]string, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := h.Run("todo", "add", fmt.Sprintf("parallel task %c", 'a'+i), "-p", strconv.Itoa(i))
			if r.Code != 0 {
				t.Errorf("add %d: exit %d\n%s", i, r.Code, r.Stderr)
				return
			}
			ids[i] = r.Out()
		}()
	}
	wg.Wait()
	seen := map[string]string{}
	for _, id := range ids {
		if len(id) < 5 {
			t.Fatalf("malformed id %q in %v", id, ids)
		}
		if prev, dup := seen[id[:4]]; dup {
			t.Errorf("duplicate prefix %s: %s and %s", id[:4], prev, id)
		}
		seen[id[:4]] = id
	}
	if len(seen) != n {
		t.Errorf("%d unique prefixes, want %d: %v", len(seen), n, ids)
	}
	if got := listIDs(h.Run("todo", "list").Stdout); len(got) != n {
		t.Errorf("list shows %d tasks, want %d", len(got), n)
	}
}

// TestTodoAddSoftCapWarning: past OpenSoftCap open tasks `add` still succeeds
// but says so on stderr, so a fold-in that ignores the /distill cap is at
// least visible in its transcript.
func TestTodoAddSoftCapWarning(t *testing.T) {
	h := clitest.New(t)
	for i := 1; i <= 20; i++ {
		r := h.Run("todo", "add", fmt.Sprintf("task %d", i))
		if r.Code != 0 || r.Stderr != "" {
			t.Fatalf("add %d: exit %d stderr %q", i, r.Code, r.Stderr)
		}
	}
	r := h.Run("todo", "add", "one over")
	if r.Code != 0 || r.Out() == "" {
		t.Fatalf("add over cap must still succeed: exit %d out %q", r.Code, r.Out())
	}
	if !strings.Contains(r.Stderr, "21 open tasks (soft cap 20)") {
		t.Errorf("stderr = %q, want soft-cap warning", r.Stderr)
	}
	// done tasks don't count: closing two leaves 19 open, so the next add (20) is quiet
	for _, id := range []string{r.Out(), "0001-task-1"} {
		if d := h.Run("todo", "done", id, "--force"); d.Code != 0 {
			t.Fatalf("done %s: exit %d %s", id, d.Code, d.Stderr)
		}
	}
	if r2 := h.Run("todo", "add", "back under"); r2.Stderr != "" {
		t.Errorf("stderr after a done = %q, want none", r2.Stderr)
	}
}
