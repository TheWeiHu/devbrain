package nightshift

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// WriteOnlySet records a fixed-set run's scope and clears it for a full-drain
// run, so a stale file from a prior --only run never mis-scopes the next.
func TestWriteOnlySet(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".nightshift"), 0o755)
	f := filepath.Join(dir, ".nightshift", "only.txt")

	fixed := NewOrch(Options{Repo: dir, FixedSet: true, Only: "0001,0003-gamma"}, io.Discard)
	fixed.WriteOnlySet()
	if b, _ := os.ReadFile(f); string(b) != "0001,0003-gamma\n" {
		t.Fatalf("fixed-set only.txt = %q", b)
	}

	NewOrch(Options{Repo: dir}, io.Discard).WriteOnlySet() // full-drain clears it
	if _, err := os.Stat(f); !os.IsNotExist(err) {
		t.Fatalf("full-drain run must clear only.txt, stat err = %v", err)
	}
}
