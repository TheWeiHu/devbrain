// Package config resolves where the devbrain data repo lives. It replaces the
// legacy installer's sed-pinning of $DATA into script copies: the binary reads
// a config file instead, written once by `devbrain install`.
//
// Precedence: $DEVBRAIN_DATA env > ~/.config/devbrain/config.json > ~/devbrain-data.
// Every failure falls open to the next step — a hook must never die on a
// missing or corrupt config.
package config

import (
	"encoding/json"
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
func load() File {
	var f File
	if p := Path(); p != "" {
		if b, err := os.ReadFile(p); err == nil {
			_ = json.Unmarshal(b, &f)
		}
	}
	return f
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

// DataDir resolves the data repo path. Never returns "" unless HOME itself is
// unresolvable.
func DataDir() string {
	if d := os.Getenv("DEVBRAIN_DATA"); d != "" {
		return d
	}
	if f := load(); f.Data != "" {
		return expandHome(f.Data)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "devbrain-data")
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
