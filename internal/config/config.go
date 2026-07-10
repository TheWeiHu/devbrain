// Package config resolves where the devbrain data repo lives. It replaces the
// legacy installer's sed-pinning of $DATA into script copies: the binary reads
// a config file instead, written once by `devbrain install`.
//
// Precedence: $DEVBRAIN_DATA env > ~/.config/devbrain/config.json > ~/devbrain-data.
// Invalid configured paths are errors: silently falling back can split one brain
// across two repositories or write a relative path into the current project.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// File is the persisted config shape.
type File struct {
	Data string `json:"data"`
	// GbrainDir is gbrain's install dir, detected at install time so the
	// orchestrator can put it back on a worker's profile-less PATH. "" if absent.
	GbrainDir string `json:"gbrain_dir,omitempty"`
}

// DataDirResolution describes the selected path and why it won precedence.
type DataDirResolution struct {
	Path   string `json:"path"`
	Source string `json:"source"`
}

func load() (File, error) {
	var f File
	p := Path()
	if p == "" {
		return f, fmt.Errorf("cannot resolve config path: home directory is unavailable")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return f, fmt.Errorf("parse %s: %w", p, err)
	}
	return f, nil
}

// Path returns the config file location ($XDG_CONFIG_HOME aware).
func Path() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".config")
	}
	if !filepath.IsAbs(base) {
		return ""
	}
	return filepath.Join(base, "devbrain", "config.json")
}

// ResolveDataDirInfo resolves the data repo path and reports the winning source.
// Configured paths must be absolute (after ~/ expansion) so hook execution from
// an arbitrary cwd can never redirect captures into that working repository.
func ResolveDataDirInfo() (DataDirResolution, error) {
	if d := os.Getenv("DEVBRAIN_DATA"); d != "" {
		p, err := validateDataDir(d, "$DEVBRAIN_DATA")
		return DataDirResolution{Path: p, Source: "env"}, err
	}
	f, err := load()
	if err == nil && f.Data != "" {
		p, pathErr := validateDataDir(f.Data, Path())
		return DataDirResolution{Path: p, Source: "config"}, pathErr
	}
	if err != nil && !os.IsNotExist(err) {
		return DataDirResolution{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return DataDirResolution{}, fmt.Errorf("resolve default data directory: %w", err)
	}
	p, err := validateDataDir(filepath.Join(home, "devbrain-data"), "default")
	return DataDirResolution{Path: p, Source: "default"}, err
}

// ResolveDataDir returns the validated absolute data repo path.
func ResolveDataDir() (string, error) {
	r, err := ResolveDataDirInfo()
	return r.Path, err
}

// DataDir is retained for internal callers that cannot return an error. An
// invalid config returns "" rather than guessing another storage location.
func DataDir() string {
	d, _ := ResolveDataDir()
	return d
}

// GbrainBinDir returns the recorded directory holding the gbrain binary, or ""
// when gbrain was absent at install (or the config is missing/corrupt).
func GbrainBinDir() string {
	f, err := load()
	if err != nil {
		return ""
	}
	return f.GbrainDir
}

// Write persists the resolved data dir (used by `devbrain install`), preserving
// any other recorded fields.
func Write(dataDir string) error {
	if _, err := validateDataDir(dataDir, "data directory"); err != nil {
		return err
	}
	f, _ := load() // An explicit write repairs an unreadable prior config.
	f.Data = dataDir
	return save(f)
}

// SetGbrainDir records the gbrain binary directory, preserving the data dir.
func SetGbrainDir(dir string) error {
	f, _ := load()
	f.GbrainDir = dir
	return save(f)
}

func save(f File) error {
	p := Path()
	if p == "" {
		return os.ErrNotExist
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	if err := os.Chmod(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".config-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, p)
}

func expandHome(p string) string {
	if len(p) > 1 && p[0] == '~' && p[1] == '/' {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

func validateDataDir(value, source string) (string, error) {
	p := expandHome(value)
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("%s data path %q is relative; use an absolute path or ~/...", source, value)
	}
	return filepath.Clean(p), nil
}
