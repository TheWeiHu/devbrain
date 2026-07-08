package todo

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TheWeiHu/devbrain/internal/task"
)

// fixtureWork is a clone whose local origin carries a nightshift branch (with
// one well-formed merge subject and several near-misses) plus todo/* branches.
// Built once in TestMain; derive tests chdir into it — no network anywhere.
var fixtureWork string

func gitRun(t testing.TB, dir string, args ...string) {
	cmd := exec.Command("git", append([]string{"-C", dir,
		"-c", "user.email=a@b.c", "-c", "user.name=t",
		"-c", "commit.gpgsign=false"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "todo-fixture-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := func() int {
		defer os.RemoveAll(tmp)
		t := shimTB{tmp: tmp}
		origin := filepath.Join(tmp, "origin.git")
		work := filepath.Join(tmp, "work")
		gitRun(t, tmp, "init", "-q", "--bare", origin)
		gitRun(t, tmp, "init", "-q", work)
		gitRun(t, work, "remote", "add", "origin", origin)
		if err := os.WriteFile(filepath.Join(work, "f"), []byte("base\n"), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		gitRun(t, work, "add", "f")
		gitRun(t, work, "commit", "-qm", "init")
		gitRun(t, work, "push", "-q", "origin", "HEAD:main")
		// nightshift branch: one valid merge subject + near-miss subjects
		gitRun(t, work, "checkout", "-qb", "nightshift")
		gitRun(t, work, "commit", "--allow-empty", "-qm", "nightshift: merge todo/0007-fix-thing into nightshift")
		gitRun(t, work, "commit", "--allow-empty", "-qm", "nightshift: merge todo/12-short into nightshift")          // not 4 digits
		gitRun(t, work, "commit", "--allow-empty", "-qm", "nightshift: merge todo/0008-UPPER into nightshift")        // uppercase slug
		gitRun(t, work, "commit", "--allow-empty", "-qm", "say nightshift: merge todo/0011-prefixed into nightshift") // not anchored at start
		gitRun(t, work, "push", "-q", "origin", "nightshift")
		// remote todo/* branches: one valid, one that must not match the regex
		gitRun(t, work, "checkout", "-qB", "todo/0009-other", "nightshift")
		gitRun(t, work, "commit", "--allow-empty", "-qm", "work 0009")
		gitRun(t, work, "push", "-q", "origin", "todo/0009-other")
		gitRun(t, work, "checkout", "-qB", "todo/nomatch", "nightshift")
		gitRun(t, work, "push", "-q", "origin", "todo/nomatch")
		gitRun(t, work, "checkout", "-q", "nightshift")
		fixtureWork = work
		return m.Run()
	}()
	os.Exit(code)
}

// shimTB lets gitRun (a testing.TB helper) run inside TestMain.
type shimTB struct {
	testing.TB
	tmp string
}

func (s shimTB) Fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.RemoveAll(s.tmp)
	os.Exit(1)
}
func (s shimTB) Helper() {}

// withNow pins the injectable clock for one test.
func withNow(t *testing.T, at time.Time) {
	old := Now
	Now = func() time.Time { return at }
	t.Cleanup(func() { Now = old })
}

func TestSlugify(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Add retry queue", "add-retry-queue"},
		{"Fix Mobile FOOTER overlap!!", "fix-mobile-footer-overlap"},
		{"  weird -- spacing  ", "weird-spacing"},
		{"!!!", ""},
		{"", ""},
		{"under_scores drop", "underscores-drop"},
		{"UPPER lower 123", "upper-lower-123"},
		{"----", ""},
		// cap at 40 AFTER collapsing/trimming; a dash re-exposed at the cap stays
		{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa b", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-"},
		{"this title is quite long and will definitely be capped", "this-title-is-quite-long-and-will-defini"},
	}
	for _, c := range cases {
		if got := slugify(c.in); got != c.want {
			t.Errorf("slugify(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestOnlyMatch(t *testing.T) {
	cases := []struct {
		name, only, id string
		want           bool
	}{
		{"unset filter matches all", "", "0081-foo-bar", true},
		{"full slug match", "0081-foo-bar,0082-baz", "0081-foo-bar", true},
		{"bare 4-digit match", "0081", "0081-foo-bar", true},
		{"space separated", "0082-baz 0081-foo-bar", "0081-foo-bar", true},
		{"comma+space mix", "0082-baz, 0081", "0081-foo-bar", true},
		{"no match", "0082-baz,0083", "0081-foo-bar", false},
		{"prefix is not a match", "0081-foo", "0081-foo-bar", false},
		{"number must be exact", "81", "0081-foo-bar", false},
		{"id without dash matches itself", "0081", "0081", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("DEVBRAIN_TODO_ONLY", c.only)
			if got := onlyMatch(c.id); got != c.want {
				t.Errorf("only=%q id=%q: got %v, want %v", c.only, c.id, got, c.want)
			}
		})
	}
}

func TestEpochOf(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"1970-01-01T00:00:00Z", 0},
		{"2026-07-02T00:00:10Z", time.Date(2026, 7, 2, 0, 0, 10, 0, time.UTC).Unix()},
		{"garbage", 0},
		{"", 0},
		{"2026-07-02T00:00:10+00:00", 0}, // strict literal-Z format only
	}
	for _, c := range cases {
		if got := epochOf(c.in); got != c.want {
			t.Errorf("epochOf(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func taskContent(fields string) string {
	return "---\nid: 0001-x\n" + fields + "---\n\n# X\n"
}

func TestLeaseAlive(t *testing.T) {
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	withNow(t, now)
	stamp := func(age int64) string {
		return time.Unix(now.Unix()-age, 0).UTC().Format("2006-01-02T15:04:05Z")
	}
	cases := []struct {
		name, ttl string
		claimedAt string
		want      bool
	}{
		{"fresh claim alive", "", stamp(0), true},
		{"just under default ttl", "", stamp(5399), true},
		{"at default ttl dead", "", stamp(5400), false},
		{"future claim dead", "", stamp(-10), false},
		{"empty claimed_at dead", "", "", false},
		{"unparseable claimed_at = epoch 0 = stale", "", "not-a-time", false},
		{"custom ttl alive", "60", stamp(59), true},
		{"custom ttl dead", "60", stamp(60), false},
		{"non-numeric ttl dead", "soon", stamp(0), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("DEVBRAIN_TODO_CLAIM_TTL", c.ttl)
			cli := &cli{}
			content := taskContent("status: taken\nclaimed_at: " + c.claimedAt + "\n")
			if got := cli.leaseAlive(task.Parse(content, "")); got != c.want {
				t.Errorf("claimed_at=%q ttl=%q: got %v, want %v", c.claimedAt, c.ttl, got, c.want)
			}
		})
	}
}

func TestArchive(t *testing.T) {
	t.Setenv("DEVBRAIN_TODO_DERIVE_GIT", "0")
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	withNow(t, now)
	dir := t.TempDir()
	stamp := func(daysAgo int) string {
		return now.AddDate(0, 0, -daysAgo).UTC().Format("2006-01-02T15:04:05Z")
	}
	write := func(id, status, doneAt string) {
		body := "---\nid: " + id + "\nstatus: " + status + "\n"
		if doneAt != "" {
			body += "done_at: " + doneAt + "\n"
		}
		body += "---\n\n# " + id + "\n"
		if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("0001-old-done", "done", stamp(40))        // archived: aged out
	write("0002-recent-done", "done", stamp(5))      // kept: too recent
	write("0003-undated-done", "done", "")           // kept: no done_at signal
	write("0004-still-open", "open", "")             // kept: not done
	write("0009-newest-old-done", "done", stamp(90)) // archived: highest id

	newCLI := func() (*cli, *bytes.Buffer) {
		var out bytes.Buffer
		return &cli{dir: dir, project: "p", stdout: &out, stderr: &out, stdin: strings.NewReader("")}, &out
	}

	c, out := newCLI()
	if code := c.archive([]string{"30"}); code != 0 {
		t.Fatalf("archive exit %d\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "archive: 2 task(s) archived") {
		t.Errorf("summary missing/wrong:\n%s", out.String())
	}

	archived := []string{"0001-old-done", "0009-newest-old-done"}
	kept := []string{"0002-recent-done", "0003-undated-done", "0004-still-open"}
	for _, id := range archived {
		if _, err := os.Stat(filepath.Join(dir, id+".md")); !os.IsNotExist(err) {
			t.Errorf("%s still in board dir, want moved", id)
		}
		if _, err := os.Stat(filepath.Join(dir, "archive", id+".md")); err != nil {
			t.Errorf("%s not in archive/: %v", id, err)
		}
	}
	for _, id := range kept {
		if _, err := os.Stat(filepath.Join(dir, id+".md")); err != nil {
			t.Errorf("%s should stay on the board: %v", id, err)
		}
	}

	// `list all` reads the board dir only — archived cards drop off it.
	c, out = newCLI()
	c.list([]string{"all"})
	if s := out.String(); strings.Contains(s, "0001-old-done") || strings.Contains(s, "0009-newest-old-done") {
		t.Errorf("list still shows archived tasks:\n%s", s)
	}

	// Archived ids stay counted: the next id clears the archived 0009, not reuses it.
	c, _ = newCLI()
	id, err := c.allocFile("next-task")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(id, "0010-") {
		t.Errorf("allocFile after archive = %q, want 0010- (archived 0009 still counted)", id)
	}

	// A second pass with nothing newly aged out is a no-op.
	c, out = newCLI()
	c.archive([]string{"30"})
	if !strings.Contains(out.String(), "archive: 0 task(s) archived") {
		t.Errorf("second pass not a no-op:\n%s", out.String())
	}

	// Bad days arg fails cleanly.
	c, out = newCLI()
	if code := c.archive([]string{"-3"}); code != 1 {
		t.Errorf("negative days exit = %d, want 1", code)
	}
}

func TestEffectiveStatusStored(t *testing.T) {
	t.Setenv("DEVBRAIN_TODO_DERIVE_GIT", "0")
	cases := []struct{ stored, want string }{
		{"open", "open"},
		{"", "open"}, // missing status defaults open
		{"taken", "taken"},
		{"review", "review"},
		{"held", "held"},
		{"done", "done"},
	}
	for _, c := range cases {
		cli := &cli{}
		got := cli.effectiveStatus(task.Parse(taskContent("status: "+c.stored+"\n"), ""), "0001-x")
		if got != c.want {
			t.Errorf("stored %q: got %q, want %q", c.stored, got, c.want)
		}
	}
}

// inFixture runs the test body chdir'd into the fixture work clone.
func inFixture(t *testing.T) {
	t.Helper()
	t.Chdir(fixtureWork)
}

func TestDeriveParsing(t *testing.T) {
	inFixture(t)
	t.Setenv("DEVBRAIN_TODO_DERIVE_GIT", "1")
	c := &cli{}
	c.deriveInit()
	if !c.deriveOn {
		t.Fatal("derive should be on inside the fixture repo")
	}
	if !c.doneIDs["0007-fix-thing"] {
		t.Error("well-formed merge subject not parsed into done ids")
	}
	for _, bad := range []string{"12-short", "0008-UPPER", "0011-prefixed"} {
		if c.doneIDs[bad] {
			t.Errorf("near-miss subject %q must not derive done", bad)
		}
	}
	if !c.branchIDs["0009-other"] {
		t.Error("remote todo/0009-other branch not parsed into review ids")
	}
	if c.branchIDs["nomatch"] {
		t.Error("todo/nomatch (no NNNN- prefix) must not derive review")
	}
}

func TestDeriveOffOutsideRepoOrWhenDisabled(t *testing.T) {
	t.Run("env off", func(t *testing.T) {
		inFixture(t)
		t.Setenv("DEVBRAIN_TODO_DERIVE_GIT", "0")
		c := &cli{}
		c.deriveInit()
		if c.deriveOn {
			t.Error("derive must stay off without DEVBRAIN_TODO_DERIVE_GIT=1")
		}
	})
	t.Run("outside a repo", func(t *testing.T) {
		t.Chdir(t.TempDir())
		t.Setenv("DEVBRAIN_TODO_DERIVE_GIT", "1")
		c := &cli{}
		c.deriveInit()
		if c.deriveOn {
			t.Error("derive must stay off outside a git work tree")
		}
	})
}

// newBoardCLI returns a cli over a fresh temp todo dir with derive-git off.
func newBoardCLI(t *testing.T) (*cli, *bytes.Buffer, string) {
	t.Helper()
	t.Setenv("DEVBRAIN_TODO_DERIVE_GIT", "0")
	dir := t.TempDir()
	var out bytes.Buffer
	return &cli{dir: dir, project: "p", stdout: &out, stderr: &out, stdin: strings.NewReader("")}, &out, dir
}

func TestLog(t *testing.T) {
	withNow(t, time.Date(2026, 7, 8, 9, 0, 0, 0, time.UTC))
	stamp := "2026-07-08T09:00:00Z"

	t.Run("pr-backed backfill is born done", func(t *testing.T) {
		c, out, dir := newBoardCLI(t)
		if code := c.log([]string{"recorded X", "https://gh/pr/251"}); code != 0 {
			t.Fatalf("log exit %d: %s", code, out.String())
		}
		id := strings.TrimSpace(out.String())
		b, err := os.ReadFile(filepath.Join(dir, id+".md"))
		if err != nil {
			t.Fatal(err)
		}
		tk := task.Parse(string(b), "p")
		if tk.Status != "done" || tk.Raw("origin") != "backfill" || tk.PR != "https://gh/pr/251" {
			t.Errorf("got status=%q origin=%q pr=%q", tk.Status, tk.Raw("origin"), tk.PR)
		}
		if tk.Created != stamp || tk.DoneAt != stamp {
			t.Errorf("created=%q done_at=%q, want both %q", tk.Created, tk.DoneAt, stamp)
		}
	})

	t.Run("pr-less backfill succeeds", func(t *testing.T) {
		c, out, dir := newBoardCLI(t)
		if code := c.log([]string{"recorded Y"}); code != 0 {
			t.Fatalf("log exit %d: %s", code, out.String())
		}
		id := strings.TrimSpace(out.String())
		b, _ := os.ReadFile(filepath.Join(dir, id+".md"))
		tk := task.Parse(string(b), "p")
		if tk.Status != "done" || tk.Raw("origin") != "backfill" || tk.PR != "" {
			t.Errorf("got status=%q origin=%q pr=%q", tk.Status, tk.Raw("origin"), tk.PR)
		}
	})

	t.Run("a third positional errors", func(t *testing.T) {
		c, _, _ := newBoardCLI(t)
		if code := c.log([]string{"title", "pr", "extra"}); code != 1 {
			t.Errorf("extra positional exit = %d, want 1", code)
		}
	})

	t.Run("no title errors", func(t *testing.T) {
		c, _, _ := newBoardCLI(t)
		if code := c.log([]string{"-p", "40"}); code != 1 {
			t.Errorf("no title exit = %d, want 1", code)
		}
	})
}

// TestLogIgnoresFixedSetFence pins the invariant that a backfill is NOT parked
// held by an active --only run (unlike `add`) — a closed ledger entry is not a
// unit of work to hand out.
func TestLogIgnoresFixedSetFence(t *testing.T) {
	withNow(t, time.Date(2026, 7, 8, 9, 0, 0, 0, time.UTC))
	t.Setenv("DEVBRAIN_TODO_DERIVE_GIT", "0")
	root := t.TempDir()
	ns := filepath.Join(root, ".nightshift")
	if err := os.MkdirAll(ns, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ns, "only.txt"), []byte("0001-x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A live orchestrator pid activates the fence — use this test process.
	if err := os.WriteFile(filepath.Join(ns, "orchestrator.pid"), []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	if fixedSetRepo() == "" {
		t.Fatal("fixed-set fence not active — test setup wrong")
	}

	board := filepath.Join(root, "todo")
	if err := os.MkdirAll(board, 0o755); err != nil {
		t.Fatal(err)
	}
	newCLI := func() (*cli, *bytes.Buffer) {
		var out bytes.Buffer
		return &cli{dir: board, project: "p", stdout: &out, stderr: &out, stdin: strings.NewReader("")}, &out
	}

	// Sanity: `add` under an active fence is parked held.
	c, out := newCLI()
	if code := c.add([]string{"fenced task"}); code != 0 {
		t.Fatalf("add exit %d: %s", code, out.String())
	}
	b, _ := os.ReadFile(filepath.Join(board, strings.TrimSpace(out.String())+".md"))
	if st := task.Parse(string(b), "p").Status; st != "held" {
		t.Fatalf("add under fence status=%q, want held (setup)", st)
	}

	// `log` ignores the fence: born done, not held.
	c, out = newCLI()
	if code := c.log([]string{"recorded Z", "https://gh/pr/9"}); code != 0 {
		t.Fatalf("log exit %d: %s", code, out.String())
	}
	b, _ = os.ReadFile(filepath.Join(board, strings.TrimSpace(out.String())+".md"))
	tk := task.Parse(string(b), "p")
	if tk.Status != "done" || tk.Raw("origin") != "backfill" {
		t.Errorf("log under fence status=%q origin=%q, want done/backfill", tk.Status, tk.Raw("origin"))
	}
}

func TestReopenClearsOrigin(t *testing.T) {
	withNow(t, time.Date(2026, 7, 8, 9, 0, 0, 0, time.UTC))

	t.Run("reopened backfill drops origin", func(t *testing.T) {
		c, out, dir := newBoardCLI(t)
		c.log([]string{"recorded X", "https://gh/pr/1"})
		id := strings.TrimSpace(out.String())
		out.Reset()
		if code := c.reopen([]string{id}); code != 0 {
			t.Fatalf("reopen exit %d: %s", code, out.String())
		}
		b, _ := os.ReadFile(filepath.Join(dir, id+".md"))
		tk := task.Parse(string(b), "p")
		if tk.Status != "open" || tk.Raw("origin") != "" {
			t.Errorf("got status=%q origin=%q, want open/empty", tk.Status, tk.Raw("origin"))
		}
	})

	t.Run("normal reopen gains no stray origin line", func(t *testing.T) {
		content := "---\nid: 0001-x\nstatus: done\npr: https://gh/pr/2\ndone_at: 2026-07-01T00:00:00Z\n---\n\n# X\n"
		got := clearOnReopen(content)
		if strings.Contains(got, "origin:") {
			t.Errorf("normal reopen added an origin line:\n%s", got)
		}
	})
}

func TestEffectiveStatusDerived(t *testing.T) {
	inFixture(t)
	t.Setenv("DEVBRAIN_TODO_DERIVE_GIT", "1")
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	withNow(t, now)
	fresh := now.Add(-time.Minute).UTC().Format("2006-01-02T15:04:05Z")
	stale := now.Add(-3 * time.Hour).UTC().Format("2006-01-02T15:04:05Z")
	cases := []struct {
		name, fields, id, want string
	}{
		{"merged id derives done over stored open", "status: open\n", "0007-fix-thing", "done"},
		{"held always wins, even for merged id", "status: held\n", "0007-fix-thing", "held"},
		{"remote branch derives review", "status: open\n", "0009-other", "review"},
		{"done ignored without merge evidence", "status: done\n", "0042-unmerged", "open"},
		{"fresh lease derives taken", "status: taken\nclaimed_at: " + fresh + "\n", "0042-unmerged", "taken"},
		{"expired lease derives open", "status: taken\nclaimed_at: " + stale + "\n", "0042-unmerged", "open"},
		{"merge evidence beats branch and lease", "status: open\nclaimed_at: " + fresh + "\n", "0007-fix-thing", "done"},
	}
	c := &cli{} // one cli: derive is primed once, like one process
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := c.effectiveStatus(task.Parse(taskContent(tc.fields), ""), tc.id); got != tc.want {
				t.Errorf("id=%s fields=%q: got %q, want %q", tc.id, tc.fields, got, tc.want)
			}
		})
	}
}
