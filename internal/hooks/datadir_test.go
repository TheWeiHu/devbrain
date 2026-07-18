package hooks

import (
	"os"
	"path/filepath"
	"testing"
)

// treeOf snapshots every path under root, so a test can prove nothing was
// created, removed, or rewritten.
func treeOf(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if fi.IsDir() {
			out[rel] = "<dir>"
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[rel] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func sameTree(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// Handlers that route through the data dir, and so must fail loud when it is
// unresolvable. turn-marker is excluded: it writes only to $NIGHTSHIFT_MARKER
// and never touches the data repo, so it has nothing to refuse.
var resolvesDataDir = map[string]bool{"gbrain": true, "session-start": true}

// A relative configured data path used to resolve against the hook's cwd — the
// user's project repo — writing the private prompt log into it, where it could
// get committed. Every capture handler must now refuse instead of writing.
func TestCaptureRefusesRelativeDataDir(t *testing.T) {
	for name, handler := range Handlers {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", "")
			t.Setenv("DEVBRAIN_DATA", "") // config file is the only source
			t.Setenv("DEVBRAIN_PROJECT", "fix__demo")
			t.Setenv("DEVBRAIN_HARNESS", "")
			fixedClock(t)

			cfg := filepath.Join(home, ".config", "devbrain", "config.json")
			if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(cfg, []byte(`{"data":"devbrain-data"}`), 0o644); err != nil {
				t.Fatal(err)
			}

			// A stand-in for the user's project repo: the hook runs with this cwd.
			project := t.TempDir()
			if err := os.WriteFile(filepath.Join(project, "main.go"), []byte("package main\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			before := treeOf(t, project)

			wd, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chdir(project); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Chdir(wd) })

			ev := payload(t, map[string]any{
				"tool_name":     "Bash",
				"tool_input":    map[string]any{"command": `gbrain search "secret query"`},
				"tool_response": map[string]any{"stdout": "[0.9] fix__demo/page -- body\n"},
				"cwd":           project,
			})
			if err := handler(ev); err == nil && resolvesDataDir[name] {
				t.Error("want a loud error for a relative configured data path, got nil")
			}
			if got := treeOf(t, project); !sameTree(before, got) {
				t.Errorf("capture wrote outside the resolved data dir:\nbefore %v\nafter  %v", before, got)
			}
			// And nothing landed at the relative path resolved from anywhere else.
			if _, err := os.Stat(filepath.Join(home, "devbrain-data")); err == nil {
				t.Error("must not silently fall open to the default data dir")
			}
		})
	}
}
