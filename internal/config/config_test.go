package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDataDirPrecedence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("DEVBRAIN_DATA", "")

	// 3) default
	if got, want := DataDir(), filepath.Join(home, "devbrain-data"); got != want {
		t.Errorf("default: got %q want %q", got, want)
	}

	// 2) config file
	if err := Write("/data/from/config"); err != nil {
		t.Fatal(err)
	}
	if got := DataDir(); got != "/data/from/config" {
		t.Errorf("config: got %q", got)
	}

	// 1) env wins over config
	t.Setenv("DEVBRAIN_DATA", "/data/from/env")
	if got := DataDir(); got != "/data/from/env" {
		t.Errorf("env: got %q", got)
	}
}

func TestDataDirCorruptConfigFailsOpen(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("DEVBRAIN_DATA", "")
	p := Path()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, want := DataDir(), filepath.Join(home, "devbrain-data"); got != want {
		t.Errorf("corrupt config must fall open to default: got %q want %q", got, want)
	}
}

func TestDataDirTildeExpansion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("DEVBRAIN_DATA", "")
	if err := Write("~/my-data"); err != nil {
		t.Fatal(err)
	}
	if got, want := DataDir(), filepath.Join(home, "my-data"); got != want {
		t.Errorf("tilde: got %q want %q", got, want)
	}
}

func TestXDGConfigHome(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if got, want := Path(), filepath.Join(xdg, "devbrain", "config.json"); got != want {
		t.Errorf("Path: got %q want %q", got, want)
	}
}
