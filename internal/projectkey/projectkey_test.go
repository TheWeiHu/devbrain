package projectkey

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// RemoteToKey is pinned by the golden table captured from the legacy python.
func TestRemoteToKeyGolden(t *testing.T) {
	gold, err := os.ReadFile(filepath.Join("..", "..", "testdata", "golden", "remote-keys.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimRight(string(gold), "\n"), "\n") {
		url, want, ok := strings.Cut(line, "\t")
		if !ok {
			t.Fatalf("bad golden line %q", line)
		}
		if want == "None" {
			want = ""
		}
		if got := RemoteToKey(url); got != want {
			t.Errorf("RemoteToKey(%q) = %q, want %q", url, got, want)
		}
	}
}

func TestSanitize(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ in, want string }{
		{"TheWeiHu__devbrain", "theweihu__devbrain"},
		{"has spaces here", "has-spaces-here"},
		{"weird!@#chars", "weirdchars"},
		{"dots.and_under-scores", "dots.and_under-scores"},
		{"", ""},
	} {
		if got := Sanitize(c.in); got != c.want {
			t.Errorf("Sanitize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func initRepo(t *testing.T, remote string) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"remote", "add", "origin", remote},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func TestProjectKey(t *testing.T) {
	for _, c := range []struct{ remote, want string }{
		{"https://github.com/TheWeiHu/devbrain.git", "theweihu__devbrain"},
		{"git@github.com:Owner/Repo.git", "owner__repo"},
		{"/local/path/repo", "miscellaneous"},
		{"../sibling", "miscellaneous"},
		{"file:///x/y", "miscellaneous"},
	} {
		if got := ProjectKey(initRepo(t, c.remote)); got != c.want {
			t.Errorf("ProjectKey(remote=%q) = %q, want %q", c.remote, got, c.want)
		}
	}
	// no repo at all -> miscellaneous
	if got := ProjectKey(t.TempDir()); got != "miscellaneous" {
		t.Errorf("no-repo ProjectKey = %q", got)
	}
}

func TestProjectKeyEnvOverride(t *testing.T) {
	t.Setenv("DEVBRAIN_PROJECT", "My Project")
	if got := ProjectKey(t.TempDir()); got != "my-project" {
		t.Errorf("override = %q", got)
	}
}

// The data repo has its own git remote, but must never be minted as a project:
// a session that cd'd into it (or a subdir) resolves to "" so callers refuse.
func TestProjectKeyRefusesDataRepo(t *testing.T) {
	data := initRepo(t, "https://github.com/TheWeiHu/devbrain-data.git")
	t.Setenv("DEVBRAIN_DATA", data)
	t.Setenv("DEVBRAIN_PROJECT", "")

	if !InDataRepo(data) {
		t.Error("InDataRepo(data root) = false, want true")
	}
	if got := ProjectKey(data); got != "" {
		t.Errorf("ProjectKey(data root) = %q, want \"\"", got)
	}

	// A subdir of the data repo resolves to the same toplevel -> still refused.
	sub := filepath.Join(data, "projects", "some-proj")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := ProjectKey(sub); got != "" {
		t.Errorf("ProjectKey(data subdir) = %q, want \"\"", got)
	}

	// Anything under the data dir is off-limits — even a separate repo nested
	// inside it (a pathological layout; capture there is refused, not routed).
	nested := initNestedRepo(t, filepath.Join(data, "acme-widget"), "git@github.com:acme/widget.git")
	if got := ProjectKey(nested); got != "" {
		t.Errorf("ProjectKey(repo nested in data dir) = %q, want \"\"", got)
	}

	// An explicit DEVBRAIN_PROJECT still routes, even from inside the data repo.
	t.Setenv("DEVBRAIN_PROJECT", "redlens")
	if got := ProjectKey(data); got != "redlens" {
		t.Errorf("ProjectKey(data, env override) = %q, want redlens", got)
	}
}

// The data dir need not be a git repo (local-only, remote-less, or a synced
// plain folder). Path-based detection must still refuse it — a git/remote check
// would silently re-mint the bogus project here.
func TestProjectKeyRefusesNonGitDataRepo(t *testing.T) {
	data := t.TempDir() // plain directory, no git init
	t.Setenv("DEVBRAIN_DATA", data)
	t.Setenv("DEVBRAIN_PROJECT", "")

	if !InDataRepo(data) {
		t.Error("InDataRepo(non-git data root) = false, want true")
	}
	sub := filepath.Join(data, "projects", "x")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := ProjectKey(sub); got != "" {
		t.Errorf("ProjectKey(non-git data subdir) = %q, want \"\"", got)
	}
}

// initNestedRepo git-inits at an explicit path (initRepo always uses TempDir).
func initNestedRepo(t *testing.T, dir, remote string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", remote}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func TestWorktreeSlug(t *testing.T) {
	dir := initRepo(t, "https://github.com/o/r.git")
	want := Sanitize(filepath.Base(dir))
	if got := WorktreeSlug(dir); got != want {
		t.Errorf("WorktreeSlug = %q, want %q", got, want)
	}
	if got := WorktreeSlug("/nonexistent-dir-xyz"); got == "" {
		t.Error("slug must never be empty")
	}
}

func TestProjectKeyAlias(t *testing.T) {
	data := t.TempDir()
	t.Setenv("DEVBRAIN_DATA", data)
	t.Setenv("DEVBRAIN_PROJECT", "")
	if err := os.MkdirAll(filepath.Join(data, "preferences"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "preferences", "project-aliases"),
		[]byte("# renames\nlongtail = acme__impetuous\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// bare repo-name alias reroutes a live checkout still on the old remote
	if got := ProjectKey(initRepo(t, "https://github.com/acme/longtail.git")); got != "acme__impetuous" {
		t.Errorf("aliased ProjectKey = %q, want acme__impetuous", got)
	}
	// unaliased repos pass through
	if got := ProjectKey(initRepo(t, "https://github.com/acme/widgets.git")); got != "acme__widgets" {
		t.Errorf("unaliased ProjectKey = %q, want acme__widgets", got)
	}
}

func TestCanonical(t *testing.T) {
	a := map[string]string{"longtail": "acme__impetuous", "acme__old": "acme__new"}
	for _, c := range []struct{ in, want string }{
		{"acme__old", "acme__new"},                // exact key
		{"theweihu__longtail", "acme__impetuous"}, // bare repo name after "__"
		{"acme__widgets", "acme__widgets"},        // passthrough
	} {
		if got := Canonical(c.in, a); got != c.want {
			t.Errorf("Canonical(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
