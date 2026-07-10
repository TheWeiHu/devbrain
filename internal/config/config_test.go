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

func TestDataDirCorruptConfigIsAnError(t *testing.T) {
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
	if got := DataDir(); got != "" {
		t.Errorf("DataDir with corrupt config = %q, want empty", got)
	}
	if _, err := ResolveDataDir(); err == nil {
		t.Fatal("ResolveDataDir with corrupt config returned nil error")
	}
}

func TestDataDirRejectsRelativeEnvironmentPath(t *testing.T) {
	t.Setenv("DEVBRAIN_DATA", "private-data")
	if got := DataDir(); got != "" {
		t.Errorf("DataDir with relative env path = %q, want empty", got)
	}
	if _, err := ResolveDataDir(); err == nil {
		t.Fatal("ResolveDataDir with relative env path returned nil error")
	}
}

func TestDataDirRejectsRelativeConfigPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("DEVBRAIN_DATA", "")
	p := Path()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{"data":"private-data"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveDataDir(); err == nil {
		t.Fatal("ResolveDataDir with relative config path returned nil error")
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

func TestRelativeXDGConfigHomeIsRejected(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "relative-config")
	t.Setenv("DEVBRAIN_DATA", "")
	if got := Path(); got != "" {
		t.Fatalf("Path = %q, want empty for relative XDG_CONFIG_HOME", got)
	}
	if _, err := ResolveDataDir(); err == nil {
		t.Fatal("ResolveDataDir with relative XDG_CONFIG_HOME returned nil error")
	}
}

func TestDataDirResolutionSource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("DEVBRAIN_DATA", "")
	if got, err := ResolveDataDirInfo(); err != nil || got.Source != "default" {
		t.Fatalf("default resolution = %+v, %v", got, err)
	}
	t.Setenv("DEVBRAIN_DATA", "/data/from/env")
	if got, err := ResolveDataDirInfo(); err != nil || got.Source != "env" || got.Path != "/data/from/env" {
		t.Fatalf("env resolution = %+v, %v", got, err)
	}
}

func TestWriteUsesPrivateAtomicFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("DEVBRAIN_DATA", "")
	if err := Write("/data/home"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(Path())
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("config mode = %o, want 600", got)
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
