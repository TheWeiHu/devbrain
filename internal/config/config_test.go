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

func TestWritePreservesGbrainDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("DEVBRAIN_DATA", "")

	if err := SetGbrainDir("/opt/gbrain/bin"); err != nil {
		t.Fatal(err)
	}
	// A later data-dir write must NOT clobber the recorded gbrain dir.
	if err := Write("/data/home"); err != nil {
		t.Fatal(err)
	}
	if got := GbrainBinDir(); got != "/opt/gbrain/bin" {
		t.Errorf("gbrain dir lost after Write: got %q", got)
	}
	if got := DataDir(); got != "/data/home" {
		t.Errorf("data dir: got %q", got)
	}
	// And SetGbrainDir must not clobber the data dir.
	if err := SetGbrainDir(""); err != nil {
		t.Fatal(err)
	}
	if got := DataDir(); got != "/data/home" {
		t.Errorf("data dir lost after SetGbrainDir: got %q", got)
	}
	if got := GbrainBinDir(); got != "" {
		t.Errorf("gbrain dir not cleared: got %q", got)
	}
}

func TestGbrainBinDirMissingConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	if got := GbrainBinDir(); got != "" {
		t.Errorf("no config must yield empty gbrain dir, got %q", got)
	}
}
