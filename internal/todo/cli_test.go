package todo_test

// Go-native port of scripts/test-todo.sh: the todo CLI's black-box contract
// (args in → stdout / exit-code / on-disk files out), driven through the built
// binary via the shared clitest harness. Reference for the rest of the suite.

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
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

	t.Run("TODO_ONLY scoping", func(t *testing.T) {
		only := func(val string, args ...string) string {
			return runWith(clitest.RunOpts{Env: map[string]string{"DEVBRAIN_TODO_ONLY": val}}, args...).Out()
		}
		if got := only(c+","+b, "next"); got != c {
			t.Errorf("ONLY slug next = %q, want top-in-set %q", got, c)
		}
		list := only(c+","+b, "list")
		if !strings.Contains(list, c) || !strings.Contains(list, b) || strings.Contains(list, a) {
			t.Errorf("ONLY did not scope list to {c,b}:\n%s", list)
		}
		if got := only(strings.SplitN(b, "-", 2)[0], "next"); got != b { // bare 4-digit num
			t.Errorf("ONLY bare-num next = %q, want %q", got, b)
		}
		if got := only(b+" "+c, "next"); got != c { // space-separated
			t.Errorf("ONLY space-sep next = %q, want %q", got, c)
		}
		if got := only("9999", "next"); got != "" {
			t.Errorf("ONLY no-match next = %q, want empty", got)
		}
		if got := only("", "next"); got != a { // empty == unfiltered
			t.Errorf("ONLY empty next = %q, want %q", got, a)
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
		run("done", a)
		if got := field(a, "status"); got != "done" {
			t.Errorf("done -> status %q, want done", got)
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
	})

	t.Run("list status filter", func(t *testing.T) {
		// State: a=done, b=open, c=open, old=review. Put b into review too.
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

// TestTodoDeriveGit covers the nightshift derived-status mode, which reads real
// git state (a bare origin carrying a nightshift merge + a todo/* branch).
func TestTodoDeriveGit(t *testing.T) {
	h := clitest.New(t)
	h.Project = "deriveproj"
	run := func(a ...string) clitest.Result { return h.Run(append([]string{"todo"}, a...)...) }
	runWith := func(o clitest.RunOpts, a ...string) clitest.Result {
		return h.RunWith(o, append([]string{"todo"}, a...)...)
	}
	add := func(title string) string { return run("add", title).Out() }

	ddone := add("derived done")
	dreview := add("derived review")
	dreset := add("derived reset")
	run("done", dreset)
	dtaken := add("derived taken")
	run("claim", dtaken)
	dheld := add("derived held")
	run("hold", dheld, "human hold")

	rem := filepath.Join(h.Data, "derive-origin.git")
	repo := filepath.Join(h.Data, "derive-repo")
	clitest.Git(t, "", "init", "-q", "--bare", rem)
	clitest.Git(t, "", "init", "-q", repo)
	clitest.Git(t, repo, "remote", "add", "origin", rem)
	clitest.WriteFile(t, filepath.Join(repo, "f"), "base\n")
	clitest.Git(t, repo, "add", "f")
	clitest.Git(t, repo, "commit", "-qm", "init")
	clitest.Git(t, repo, "push", "-q", "origin", "HEAD:main")
	clitest.Git(t, repo, "checkout", "-q", "-b", "nightshift")
	clitest.Git(t, repo, "commit", "--allow-empty", "-qm", "nightshift: merge todo/"+ddone+" into nightshift")
	clitest.Git(t, repo, "push", "-q", "origin", "nightshift")
	clitest.Git(t, repo, "checkout", "-q", "-B", "todo/"+dreview, "origin/main")
	clitest.Git(t, repo, "commit", "--allow-empty", "-qm", "work "+dreview)
	clitest.Git(t, repo, "push", "-q", "origin", "todo/"+dreview)
	clitest.Git(t, repo, "checkout", "-q", "main")

	dlist := func(args ...string) string {
		return runWith(clitest.RunOpts{Dir: repo, Env: map[string]string{"DEVBRAIN_TODO_DERIVE_GIT": "1"}}, args...).Stdout
	}
	cases := []struct{ name, want, id string }{
		{"nightshift merge -> done", "done", ddone},
		{"remote todo branch -> review", "review", dreview},
		{"done w/o merge evidence reopens", "", dreset},
		{"fresh claim lease -> taken", "taken", dtaken},
		{"held stays authoritative", "held", dheld},
	}
	for _, tc := range cases {
		if got := dlist("list", tc.want); !strings.Contains(got, tc.id) {
			t.Errorf("%s: %q not in `list %s`:\n%s", tc.name, tc.id, tc.want, got)
		}
	}
	if !strings.Contains(run("list", "done").Stdout, dreset) {
		t.Error("normal mode no longer trusts stored done")
	}

	// derive must fetch exactly once per invocation (not once per task); a git
	// shim on PATH counts the fetch spawns. TTL then suppresses the repeat.
	bin := filepath.Join(h.Data, "bin")
	log := filepath.Join(h.Data, "gitcalls")
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git not found: %v", err)
	}
	clitest.WriteExec(t, filepath.Join(bin, "git"), "#!/usr/bin/env bash\necho \"$1\" >> "+log+"\nexec "+real+" \"$@\"\n")

	fetches := func(extra map[string]string) int {
		clitest.WriteFile(t, log, "")
		env := map[string]string{"DEVBRAIN_TODO_DERIVE_GIT": "1", "PATH": bin + ":" + os.Getenv("PATH")}
		for k, v := range extra {
			env[k] = v
		}
		runWith(clitest.RunOpts{Dir: repo, Env: env}, "list", "all")
		return countLines(clitest.Read(t, log), "fetch")
	}
	if n := fetches(nil); n != 1 {
		t.Errorf("derive ran %d fetches per list, want exactly 1", n)
	}
	if n := fetches(map[string]string{"DEVBRAIN_TODO_FETCH_TTL": "3600"}); n != 0 {
		t.Errorf("fresh FETCH_HEAD + TTL ran %d fetches, want 0", n)
	}
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
