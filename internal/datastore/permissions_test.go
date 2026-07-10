package datastore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsurePrivateRootTightensExistingDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateRoot(root); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o700 {
		t.Fatalf("root mode = %o, want 700", got)
	}
}

func TestEnsurePrivateRootRejectsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateRoot(path); err == nil {
		t.Fatal("file data path returned nil error")
	}
}
