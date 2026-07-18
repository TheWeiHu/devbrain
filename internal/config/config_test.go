package config

import (
	"os"
	"path/filepath"
	"testing"
)

// mustResolve fails the test if resolution errors.
func mustResolve(t *testing.T) string {
	t.Helper()
	got, err := ResolveDataDir()
	if err != nil {
		t.Fatalf("ResolveDataDir: %v", err)
	}
	return got
}

func TestDataDirPrecedence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("DEVBRAIN_DATA", "")

	// 3) default
	if got, want := mustResolve(t), filepath.Join(home, "devbrain-data"); got != want {
		t.Errorf("default: got %q want %q", got, want)
	}

	// 2) config file
	if err := Write("/data/from/config"); err != nil {
		t.Fatal(err)
	}
	if got := mustResolve(t); got != "/data/from/config" {
		t.Errorf("config: got %q", got)
	}

	// 1) env wins over config
	t.Setenv("DEVBRAIN_DATA", "/data/from/env")
	if got := mustResolve(t); got != "/data/from/env" {
		t.Errorf("env: got %q", got)
	}
}

// A config that exists but cannot be trusted must error, not silently redirect
// writes to the default — half the brain landing in another repo with no
// warning is worse than a dead hook.
func TestDataDirBrokenConfigFailsLoud(t *testing.T) {
	writeConfig := func(t *testing.T, body string) {
		t.Helper()
		p := Path()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, body string
	}{
		{"malformed json", "{not json"},
		{"relative path", `{"data":"devbrain-data"}`},
		{"dot-relative path", `{"data":"./devbrain-data"}`},
		{"parent-relative path", `{"data":"../devbrain-data"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", "")
			t.Setenv("DEVBRAIN_DATA", "")
			writeConfig(t, tc.body)
			got, err := ResolveDataDir()
			if err == nil {
				t.Fatalf("want error, got %q", got)
			}
			if got != "" {
				t.Errorf("must not return a path alongside an error, got %q", got)
			}
		})
	}
}

// A relative $DEVBRAIN_DATA resolves against the hook's cwd — the user's
// project repo — so it must error too.
func TestDataDirRelativeEnvFailsLoud(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("DEVBRAIN_DATA", "devbrain-data")
	if got, err := ResolveDataDir(); err == nil {
		t.Fatalf("want error for relative env, got %q", got)
	}
}

// An absent config is not a broken one: the default must stay silent.
func TestDataDirMissingConfigUsesDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("DEVBRAIN_DATA", "")
	if got, want := mustResolve(t), filepath.Join(home, "devbrain-data"); got != want {
		t.Errorf("got %q want %q", got, want)
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
	if got, want := mustResolve(t), filepath.Join(home, "my-data"); got != want {
		t.Errorf("tilde: got %q want %q", got, want)
	}
	// Same for the env var — expansion happens before the absolute check.
	t.Setenv("DEVBRAIN_DATA", "~/env-data")
	if got, want := mustResolve(t), filepath.Join(home, "env-data"); got != want {
		t.Errorf("tilde env: got %q want %q", got, want)
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
	if got := mustResolve(t); got != "/data/home" {
		t.Errorf("data dir: got %q", got)
	}
	// And SetGbrainDir must not clobber the data dir.
	if err := SetGbrainDir(""); err != nil {
		t.Fatal(err)
	}
	if got := mustResolve(t); got != "/data/home" {
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

func TestRolePrecedence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("DEVBRAIN_ROLE", "")

	// Default: curator.
	if got := Role(); got != RoleCurator {
		t.Errorf("default role = %q, want curator", got)
	}

	// Config file.
	if err := SetRole(RoleSatellite); err != nil {
		t.Fatal(err)
	}
	if got := Role(); got != RoleSatellite {
		t.Errorf("config role = %q, want satellite", got)
	}
	// SetRole must not clobber the data dir.
	if err := Write("/data/home"); err != nil {
		t.Fatal(err)
	}
	if err := SetRole(RoleSatellite); err != nil {
		t.Fatal(err)
	}
	if got := mustResolve(t); got != "/data/home" {
		t.Errorf("data dir lost after SetRole: got %q", got)
	}

	// Env wins over config.
	t.Setenv("DEVBRAIN_ROLE", "curator")
	if got := Role(); got != RoleCurator {
		t.Errorf("env role = %q, want curator", got)
	}

	// Junk normalizes to curator (fail open — a lone machine must curate).
	t.Setenv("DEVBRAIN_ROLE", "bogus")
	if got := Role(); got != RoleCurator {
		t.Errorf("bogus role = %q, want curator", got)
	}
}
