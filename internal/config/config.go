// Package config resolves where the devbrain data repo lives. It replaces the
// legacy installer's sed-pinning of $DATA into script copies: the binary reads
// a config file instead, written once by `devbrain install`.
//
// Precedence: $DEVBRAIN_DATA env > ~/.config/devbrain/config.json > ~/devbrain-data.
// An ABSENT config falls through to the default; a BROKEN one (unreadable,
// malformed, or naming a non-absolute path) is an error. Falling open there
// would redirect writes silently: a relative path resolves against the caller's
// cwd, and a capture hook's cwd is the user's project repo.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PrefsCapBytes is the hard ceiling for preferences/global.md. The page is
// @import'd into ~/.claude/CLAUDE.md and inlined into ~/.codex/AGENTS.md on
// every session, so past this size the steers start getting diluted and
// dropped. The dashboard meter, /distill, and the AGENTS.md refresh all read
// this one constant.
const PrefsCapBytes = 8192

// Machine roles. Exactly one machine — the curator — rewrites shared brain
// state (distill fold-in, ledger, preferences, daily maintenance). Satellites
// capture, flush, and work the queue, but never curate: their log shards merge
// conflict-free, while concurrent curation from two machines conflicts in git
// and strands the flusher.
const (
	RoleCurator   = "curator"
	RoleSatellite = "satellite"
)

// File is the persisted config shape.
type File struct {
	Data string `json:"data"`
	// GbrainDir is gbrain's install dir, detected at install time so the
	// orchestrator can put it back on a worker's profile-less PATH. "" if absent.
	GbrainDir string `json:"gbrain_dir,omitempty"`
	// Role is this machine's curation role ("" = curator).
	Role string `json:"role,omitempty"`
}

// load reads the config file, returning a zero File on any error (fail open).
// Used by the settings whose default is safe when the config is unreadable
// (Role, GbrainBinDir); data-path resolution uses loadStrict instead.
func load() File {
	f, _ := loadStrict()
	return f
}

// loadStrict reads the config file, distinguishing "absent" (zero File, no
// error) from "present but broken" (error).
func loadStrict() (File, error) {
	var f File
	p := Path()
	if p == "" {
		return f, errors.New("cannot locate config file: no HOME or XDG_CONFIG_HOME")
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, fmt.Errorf("read config %s: %w", p, err)
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return f, fmt.Errorf("parse config %s: %w", p, err)
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
	return filepath.Join(base, "devbrain", "config.json")
}

// ResolveDataDir resolves the data repo path, or errors. Callers must not
// substitute a fallback: an empty or relative root joins into a path under the
// caller's cwd, which is exactly the leak this guards against.
func ResolveDataDir() (string, error) {
	if d := os.Getenv("DEVBRAIN_DATA"); d != "" {
		return requireAbs(expandHome(d), "$DEVBRAIN_DATA")
	}
	f, err := loadStrict()
	if err != nil {
		return "", err
	}
	if f.Data != "" {
		return requireAbs(expandHome(f.Data), `"data" in `+Path())
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, "devbrain-data"), nil
}

func requireAbs(p, src string) (string, error) {
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("%s must be an absolute path, got %q", src, p)
	}
	return filepath.Clean(p), nil
}

// GbrainBinDir returns the recorded directory holding the gbrain binary, or ""
// when gbrain was absent at install (or the config is missing/corrupt).
func GbrainBinDir() string { return load().GbrainDir }

// Write persists the resolved data dir (used by `devbrain install`), preserving
// any other recorded fields.
func Write(dataDir string) error {
	f := load()
	f.Data = dataDir
	return save(f)
}

// SetGbrainDir records the gbrain binary directory, preserving the data dir.
func SetGbrainDir(dir string) error {
	f := load()
	f.GbrainDir = dir
	return save(f)
}

// Role resolves this machine's role: $DEVBRAIN_ROLE env > config file >
// curator. Anything but "satellite" is curator — the single-machine default
// must keep curating (fail open).
func Role() string {
	r := os.Getenv("DEVBRAIN_ROLE")
	if r == "" {
		r = load().Role
	}
	if strings.TrimSpace(strings.ToLower(r)) == RoleSatellite {
		return RoleSatellite
	}
	return RoleCurator
}

// SetRole records the machine role, preserving the other fields.
func SetRole(role string) error {
	f := load()
	f.Role = role
	return save(f)
}

func save(f File) error {
	p := Path()
	if p == "" {
		return os.ErrNotExist
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func expandHome(p string) string {
	if len(p) > 1 && p[0] == '~' && p[1] == '/' {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}
