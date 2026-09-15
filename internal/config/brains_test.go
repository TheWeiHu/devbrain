package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBrainRegistrationPreservesLegacyEnvDefault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("DEVBRAIN_BRAIN", "")
	t.Setenv("DEVBRAIN_DATA", "/legacy/data")
	if err := RegisterBrain("work", "/work/data"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVBRAIN_DATA", "")
	r, err := Catalog()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := r.Named("default")
	if b.Data != "/legacy/data" {
		t.Fatalf("default moved: %+v", b)
	}
	if err := AssignBrain("example__project", "work"); err != nil {
		t.Fatal(err)
	}
	if err := SetRole(RoleSatellite); err != nil {
		t.Fatal(err)
	}
	r, err = Catalog()
	if err != nil || r.ForProject("example__project").Name != "work" {
		t.Fatalf("role update lost routing: %+v %v", r, err)
	}
	if err := Write("/work/data"); err != nil {
		t.Fatal(err)
	}
	r, _ = Catalog()
	b, _ = r.Named("default")
	if b.Data != "/legacy/data" {
		t.Fatal("install repointed default")
	}
}

func TestBrainRegistryRejectsInvalidAndOverlappingDestinations(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("DEVBRAIN_DATA", "")
	t.Setenv("DEVBRAIN_BRAIN", "")
	if err := Write("/personal/data"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, path string }{{"../work", "/work/data"}, {"work", "relative"}, {"work", "/personal/data/nested"}, {"work", "/personal"}} {
		if err := RegisterBrain(tc.name, tc.path); err == nil {
			t.Fatalf("accepted %+v", tc)
		}
	}
	if err := AssignBrain("example__project", "missing"); err == nil {
		t.Fatal("unknown assignment accepted")
	}
	if err := AssignBrain("..", "default"); err == nil {
		t.Fatal("traversal assignment accepted")
	}
	if err := os.WriteFile(Path(), []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVBRAIN_DATA", "/personal/data")
	if _, err := Catalog(); err == nil {
		t.Fatal("data override hid corrupted registry")
	}
}

func TestBrainRegistryRejectsSymlinkOverlap(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("DEVBRAIN_DATA", "")
	t.Setenv("DEVBRAIN_BRAIN", "")
	root := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	if err := Write(root); err != nil {
		t.Fatal(err)
	}
	if err := RegisterBrain("work", alias); err == nil {
		t.Fatal("symlink registered same data twice")
	}
}
