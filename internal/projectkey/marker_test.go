package projectkey

import (
	"os"
	"path/filepath"
	"testing"
)

// A directory with no usable git remote can name its project with a
// .devbrain-project marker file instead of landing in "miscellaneous".
func TestProjectKeyMarkerFile(t *testing.T) {
	t.Setenv("DEVBRAIN_DATA", t.TempDir())
	t.Setenv("DEVBRAIN_PROJECT", "")

	dir := t.TempDir() // not a git repo at all
	if err := os.WriteFile(filepath.Join(dir, ".devbrain-project"), []byte("Local Research\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ProjectKey(dir); got != "local-research" {
		t.Errorf("marker ProjectKey = %q, want %q", got, "local-research")
	}

	// a subdirectory inherits the nearest marker above it
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := ProjectKey(sub); got != "local-research" {
		t.Errorf("subdir ProjectKey = %q, want %q", got, "local-research")
	}

	// a marker inside a repo with a remote wins over the remote-derived key
	repo := initRepo(t, "https://github.com/acme/widgets.git")
	if err := os.WriteFile(filepath.Join(repo, ".devbrain-project"), []byte("acme__platform"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ProjectKey(repo); got != "acme__platform" {
		t.Errorf("marker-in-repo ProjectKey = %q, want %q", got, "acme__platform")
	}

	// blank and dot-only markers are ignored, never a traversal into projects/..
	for _, bad := range []string{"  \n", "..", "./"} {
		d := t.TempDir()
		if err := os.WriteFile(filepath.Join(d, ".devbrain-project"), []byte(bad), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := ProjectKey(d); got != "miscellaneous" {
			t.Errorf("marker %q ProjectKey = %q, want miscellaneous", bad, got)
		}
	}
}
