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
