package importer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/TheWeiHu/devbrain/internal/config"
	"github.com/TheWeiHu/devbrain/internal/projectkey"
)

type captureRouting struct {
	registry config.Registry
	selected string
	owners   map[string]string
	aliases  map[string]string
	known    map[string]string
}

func newCaptureRouting(data string) (*captureRouting, error) {
	r, err := config.Catalog()
	if err != nil {
		return nil, err
	}
	selectedBrain, selected, err := r.Selected()
	if err != nil && data == "" {
		return nil, err
	}
	if len(r.Brains) == 1 && data != "" {
		if !filepath.IsAbs(data) {
			return nil, fmt.Errorf("--data must be an absolute path")
		}
		r.Brains[0].Data = filepath.Clean(data)
	}
	x := &captureRouting{registry: r, owners: map[string]string{}, aliases: map[string]string{}, known: map[string]string{}}
	if len(r.Brains) > 1 {
		if selected {
			x.selected = selectedBrain.Data
		}
		if data != "" {
			found := false
			for _, b := range r.Brains {
				if config.DataID(b.Data) == config.DataID(data) {
					x.selected, found = b.Data, true
				}
			}
			if !found {
				return nil, fmt.Errorf("--data must name a registered brain")
			}
		}
	}
	for _, b := range r.Brains {
		if len(r.Brains) > 1 {
			if err := b.Available(); err != nil {
				return nil, err
			}
		}
		for alias, key := range projectkey.Aliases(b.Data) {
			if old, exists := x.aliases[alias]; exists && old != key {
				return nil, fmt.Errorf("conflicting project alias %q across brains", alias)
			}
			x.aliases[alias] = key
		}
		projects, _ := os.ReadDir(filepath.Join(b.Data, "projects"))
		for _, p := range projects {
			x.remember(p.Name())
		}
		if len(r.Brains) == 1 {
			continue
		}
		logs, _ := filepath.Glob(filepath.Join(b.Data, "projects", "*", "log", "*", "*.md"))
		for _, log := range logs {
			head, err := readHeadRunes(log, 1000)
			if err != nil {
				return nil, err
			}
			first, _, _ := strings.Cut(head, "\n")
			_, sid, ok := strings.Cut(first, " — session ")
			if !ok {
				_, sid, _ = strings.Cut(strings.TrimSuffix(filepath.Base(log), ".md"), ".")
			}
			if err := x.own(sid, b.Data); err != nil {
				return nil, err
			}
		}
		files, _ := filepath.Glob(filepath.Join(b.Data, "projects", "*", "tokens.jsonl"))
		for _, file := range files {
			raw, err := os.ReadFile(file)
			if err != nil {
				return nil, err
			}
			for _, line := range strings.Split(string(raw), "\n") {
				var row struct {
					Session string `json:"session"`
				}
				if json.Unmarshal([]byte(line), &row) == nil {
					if err := x.own(row.Session, b.Data); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	for key := range r.Projects {
		x.remember(key)
	}
	return x, nil
}

func (x *captureRouting) remember(key string) {
	if _, name, ok := strings.Cut(key, "__"); ok {
		if prior, exists := x.known[name]; exists && prior != key {
			x.known[name] = "" // an ambiguous dead checkout must not pick an owner
		} else {
			x.known[name] = key
		}
	}
}

func (x *captureRouting) own(sid, data string) error {
	if sid == "" {
		return nil
	}
	if old, exists := x.owners[sid]; exists && old != data {
		return fmt.Errorf("session %s exists in more than one brain; resolve duplicate ownership before capture", sid)
	}
	x.owners[sid] = data
	return nil
}

func (x *captureRouting) destination(key, sid string) string {
	if data := x.owners[sid]; data != "" {
		return data
	}
	b := x.registry.ForProject(key)
	if sid != "" {
		x.owners[sid] = b.Data
	}
	return b.Data
}

func (x *captureRouting) accepts(data string) bool { return x.selected == "" || x.selected == data }

func (x *captureRouting) files(pattern string) []string {
	var files []string
	for _, b := range x.registry.Brains {
		matches, _ := filepath.Glob(filepath.Join(b.Data, "projects", "*", pattern))
		files = append(files, matches...)
	}
	return files
}
