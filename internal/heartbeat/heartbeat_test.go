package heartbeat

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/TheWeiHu/devbrain/internal/gitx"
)

// repoWith builds a git repo whose history is one capture commit per entry.
func repoWith(t *testing.T, entries []Host) string {
	t.Helper()
	dir := t.TempDir()
	repo := gitx.Repo{Dir: dir}
	mustRun := func(env []string, args ...string) {
		if _, err := repo.RunEnv(env, args...); err != nil {
			t.Fatal(err)
		}
	}
	mustRun(nil, "init", "-q")
	mustRun(nil, "config", "user.name", "t")
	mustRun(nil, "config", "user.email", "t@t")
	for i, e := range entries {
		f := filepath.Join(dir, fmt.Sprintf("f%d", i))
		if err := os.WriteFile(f, []byte(e.Name), 0o644); err != nil {
			t.Fatal(err)
		}
		mustRun(nil, "add", ".")
		stamp := e.Last.Format(time.RFC3339)
		mustRun([]string{"GIT_COMMITTER_DATE=" + stamp, "GIT_AUTHOR_DATE=" + stamp},
			"commit", "-q", "-m", "capture: whenever on "+e.Name)
	}
	return dir
}

func TestCheckClassifiesSelfOthersAndAgedOut(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	Now = func() time.Time { return now }
	defer func() { Now = time.Now }()
	dir := repoWith(t, []Host{
		{Name: selfHost(), Last: now.Add(-72 * time.Hour)},      // self, stale
		{Name: "other-box", Last: now.Add(-10 * 24 * time.Hour)}, // other, stale
		{Name: "fresh-box", Last: now.Add(-2 * time.Hour)},       // healthy
		{Name: "retired-box", Last: now.Add(-90 * 24 * time.Hour)}, // aged out
	})
	st := Check(dir)
	if !st.SelfStale || st.Self == nil {
		t.Fatalf("self should be stale: %+v", st)
	}
	if len(st.StaleOthers) != 1 || st.StaleOthers[0].Name != "other-box" {
		t.Fatalf("want only other-box stale, got %+v", st.StaleOthers)
	}
	if st.Wedged {
		t.Fatal("clean repo reported wedged")
	}
	if Warning(dir) == "" {
		t.Fatal("stale self must warn")
	}
}

func TestHealthyRepoStaysSilent(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	Now = func() time.Time { return now }
	defer func() { Now = time.Now }()
	dir := repoWith(t, []Host{
		{Name: selfHost(), Last: now.Add(-1 * time.Hour)},
		{Name: "other-box", Last: now.Add(-3 * time.Hour)},
	})
	if w := Warning(dir); w != "" {
		t.Fatalf("healthy repo warned: %q", w)
	}
}

func TestWedgedRepoWinsOverEverything(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	Now = func() time.Time { return now }
	defer func() { Now = time.Now }()
	dir := repoWith(t, []Host{{Name: selfHost(), Last: now.Add(-1 * time.Hour)}})
	repo := gitx.Repo{Dir: dir}
	// Manufacture an unmerged index entry: conflicting edits on two branches.
	shared := filepath.Join(dir, "shared")
	write := func(s string) {
		if err := os.WriteFile(shared, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) { _, _ = repo.Run(args...) }
	write("base\n")
	run("add", ".")
	run("commit", "-q", "-m", "base")
	run("checkout", "-q", "-b", "side")
	write("side\n")
	run("commit", "-q", "-am", "side")
	run("checkout", "-q", "-")
	write("main\n")
	run("commit", "-q", "-am", "main")
	run("merge", "side") // conflicts; leaves unmerged entries
	st := Check(dir)
	if !st.Wedged {
		t.Fatal("conflicted repo not reported wedged")
	}
	if w := Warning(dir); w == "" {
		t.Fatal("wedged repo must warn")
	}
}

func TestNonRepoDirDegradesToHealthy(t *testing.T) {
	if w := Warning(t.TempDir()); w != "" {
		t.Fatalf("non-repo warned: %q", w)
	}
}
