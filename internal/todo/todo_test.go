package todo

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
