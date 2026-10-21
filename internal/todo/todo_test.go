package todo

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TheWeiHu/devbrain/internal/task"
)

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

func TestArchive(t *testing.T) {
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

// newBoardCLI returns a cli over a fresh temp todo dir with derive-git off.
func newBoardCLI(t *testing.T) (*cli, *bytes.Buffer, string) {
	t.Helper()
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
