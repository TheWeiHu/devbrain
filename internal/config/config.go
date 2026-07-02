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
)

// File is the persisted config shape. One key today; room to grow.
type File struct {
	Data string `json:"data"`
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
	if p := Path(); p != "" {
		if b, err := os.ReadFile(p); err == nil {
			var f File
			if json.Unmarshal(b, &f) == nil && f.Data != "" {
				return expandHome(f.Data)
			}
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "devbrain-data")
}

// Write persists the resolved data dir (used by `devbrain install`).
func Write(dataDir string) error {
	p := Path()
	if p == "" {
		return os.ErrNotExist
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(File{Data: dataDir}, "", "  ")
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
