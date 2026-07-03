package install_test

// Go-native port of scripts/test-install-path.sh: install wiring/PATH tests.
// Exercises settings.json hook wiring, idempotency, uninstall, legacy rc-line
// migration, legacy capture-copy migration, and --only scoping — all against
// throwaway HOME sandboxes. No python3 required (JSON built with encoding/json).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TheWeiHu/devbrain/internal/clitest"
)

// instRunInstall runs `devbrain install --only capture --yes` against a fresh
// sandbox home. The home must already exist with data/projects present and the
// data dir git-init'd.
func instRunInstall(t *testing.T, h *clitest.Harness, home, dataDir string, workDir string, extraEnv map[string]string) {
	t.Helper()
	env := map[string]string{
		"HOME":              home,
		"PATH":              "/usr/bin:/bin",
		"SHELL":             "/bin/zsh",
		"DEVBRAIN_BIN":      clitest.Bin(t),
		"DEVBRAIN_NO_IMPORT": "1",
		"DEVBRAIN_DATA":    dataDir,
		"DEVBRAIN_PROJECT": "",
	}
	for k, v := range extraEnv {
		env[k] = v
	}
	h.RunWith(clitest.RunOpts{Dir: workDir, CleanEnv: true, Env: env}, "install", "--only", "capture", "--yes")
}

// instInitDataDir creates the projects directory and git-initialises dataDir.
func instInitDataDir(t *testing.T, dataDir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dataDir, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	clitest.Git(t, "", "init", "-q", dataDir)
}

func TestInstallPath(t *testing.T) {
	h := clitest.New(t)
	bin := clitest.Bin(t)
	// A temp dir NOT the repo checkout — prevents git-gate from triggering.
	workDir := t.TempDir()

	// ── 1. default: settings.json points at the binary ───────────────────────
	t.Run("settings.json points at binary", func(t *testing.T) {
		sb := t.TempDir()
		instInitDataDir(t, filepath.Join(sb, "data"))
		instRunInstall(t, h, sb, filepath.Join(sb, "data"), workDir, nil)

		s := clitest.Read(t, filepath.Join(sb, ".claude", "settings.json"))
		if !strings.Contains(s, bin+" hook capture") {
			t.Errorf("settings.json does not contain %q:\n%s", bin+" hook capture", s)
		}
	})

	t.Run("no shell rc written (brew owns PATH)", func(t *testing.T) {
		sb := t.TempDir()
		instInitDataDir(t, filepath.Join(sb, "data"))
		instRunInstall(t, h, sb, filepath.Join(sb, "data"), workDir, nil)

		for _, rc := range []string{".zshrc", ".bash_profile", ".bashrc"} {
			if _, err := os.Stat(filepath.Join(sb, rc)); err == nil {
				t.Errorf("install wrote %s", rc)
			}
		}
	})

	t.Run("no hook copies under ~/.claude/hooks", func(t *testing.T) {
		sb := t.TempDir()
		instInitDataDir(t, filepath.Join(sb, "data"))
		instRunInstall(t, h, sb, filepath.Join(sb, "data"), workDir, nil)

		hooksDir := filepath.Join(sb, ".claude", "hooks")
		if _, err := os.Stat(hooksDir); err == nil {
			entries, _ := os.ReadDir(hooksDir)
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), "devbrain-") {
					t.Errorf("found legacy hook copy: %s", e.Name())
				}
			}
		}
	})

	t.Run("config.json records the data home", func(t *testing.T) {
		sb := t.TempDir()
		dataDir := filepath.Join(sb, "data")
		instInitDataDir(t, dataDir)
		instRunInstall(t, h, sb, dataDir, workDir, nil)

		cfg := filepath.Join(sb, ".config", "devbrain", "config.json")
		b, err := os.ReadFile(cfg)
		if err != nil {
			t.Fatalf("config.json not found: %v", err)
		}
		if !strings.Contains(string(b), dataDir) {
			t.Errorf("config.json does not contain data dir %q:\n%s", dataDir, b)
		}
	})

	t.Run("back-compat alias symlinks installed", func(t *testing.T) {
		sb := t.TempDir()
		instInitDataDir(t, filepath.Join(sb, "data"))
		instRunInstall(t, h, sb, filepath.Join(sb, "data"), workDir, nil)

		for _, alias := range []string{"devbrain-todo", "devbrain-import"} {
			p := filepath.Join(sb, ".local", "bin", alias)
			fi, err := os.Lstat(p)
			if err != nil {
				t.Errorf("%s not found: %v", alias, err)
				continue
			}
			if fi.Mode()&os.ModeSymlink == 0 {
				t.Errorf("%s is not a symlink", alias)
			}
		}
	})

	// ── 2. idempotent: second run does not duplicate the hook entry ───────────
	t.Run("idempotent (one capture entry)", func(t *testing.T) {
		sb := t.TempDir()
		instInitDataDir(t, filepath.Join(sb, "data"))
		instRunInstall(t, h, sb, filepath.Join(sb, "data"), workDir, nil)
		instRunInstall(t, h, sb, filepath.Join(sb, "data"), workDir, nil) // second run

		s := clitest.Read(t, filepath.Join(sb, ".claude", "settings.json"))
		count := strings.Count(s, "hook capture")
		if count != 1 {
			t.Errorf("settings.json has %d 'hook capture' entries after second install, want 1:\n%s", count, s)
		}
	})

	// ── 3. uninstall reverses it (data repo untouched) ────────────────────────
	t.Run("uninstall drops the hook entry", func(t *testing.T) {
		sb := t.TempDir()
		instInitDataDir(t, filepath.Join(sb, "data"))
		instRunInstall(t, h, sb, filepath.Join(sb, "data"), workDir, nil)

		env := map[string]string{
			"HOME":  sb,
			"PATH":  "/usr/bin:/bin",
			"SHELL": "/bin/zsh",
			"DEVBRAIN_BIN":  bin,
			"DEVBRAIN_DATA": "",
			"DEVBRAIN_PROJECT": "",
		}
		h.RunWith(clitest.RunOpts{Dir: workDir, CleanEnv: true, Env: env}, "uninstall")

		s := clitest.Read(t, filepath.Join(sb, ".claude", "settings.json"))
		if strings.Contains(s, "hook capture") {
			t.Errorf("settings.json still has hook capture after uninstall:\n%s", s)
		}
	})

	t.Run("uninstall removes config.json", func(t *testing.T) {
		sb := t.TempDir()
		instInitDataDir(t, filepath.Join(sb, "data"))
		instRunInstall(t, h, sb, filepath.Join(sb, "data"), workDir, nil)

		env := map[string]string{
			"HOME":  sb,
			"PATH":  "/usr/bin:/bin",
			"SHELL": "/bin/zsh",
			"DEVBRAIN_BIN":  bin,
			"DEVBRAIN_DATA": "",
			"DEVBRAIN_PROJECT": "",
		}
		h.RunWith(clitest.RunOpts{Dir: workDir, CleanEnv: true, Env: env}, "uninstall")

		if _, err := os.Stat(filepath.Join(sb, ".config", "devbrain", "config.json")); err == nil {
			t.Error("config.json survived uninstall")
		}
	})

	t.Run("uninstall removes alias symlinks", func(t *testing.T) {
		sb := t.TempDir()
		instInitDataDir(t, filepath.Join(sb, "data"))
		instRunInstall(t, h, sb, filepath.Join(sb, "data"), workDir, nil)

		env := map[string]string{
			"HOME":  sb,
			"PATH":  "/usr/bin:/bin",
			"SHELL": "/bin/zsh",
			"DEVBRAIN_BIN":  bin,
			"DEVBRAIN_DATA": "",
			"DEVBRAIN_PROJECT": "",
		}
		h.RunWith(clitest.RunOpts{Dir: workDir, CleanEnv: true, Env: env}, "uninstall")

		if _, err := os.Lstat(filepath.Join(sb, ".local", "bin", "devbrain-todo")); err == nil {
			t.Error("devbrain-todo symlink survived uninstall")
		}
	})

	t.Run("uninstall keeps the data repo", func(t *testing.T) {
		sb := t.TempDir()
		dataDir := filepath.Join(sb, "data")
		instInitDataDir(t, dataDir)
		instRunInstall(t, h, sb, dataDir, workDir, nil)

		env := map[string]string{
			"HOME":  sb,
			"PATH":  "/usr/bin:/bin",
			"SHELL": "/bin/zsh",
			"DEVBRAIN_BIN":  bin,
			"DEVBRAIN_DATA": dataDir,
			"DEVBRAIN_PROJECT": "",
		}
		h.RunWith(clitest.RunOpts{Dir: workDir, CleanEnv: true, Env: env}, "uninstall")

		if _, err := os.Stat(filepath.Join(dataDir, ".git")); err != nil {
			t.Errorf("data repo was removed by uninstall: %v", err)
		}
	})

	// ── 4. migration: legacy rc PATH line removed ─────────────────────────────
	t.Run("legacy rc marker+export removed", func(t *testing.T) {
		sb := t.TempDir()
		instInitDataDir(t, filepath.Join(sb, "data"))
		rc := "# my stuff\n\n# added by devbrain installer\nexport PATH=\"$HOME/.local/bin:$PATH\"\n"
		clitest.WriteFile(t, filepath.Join(sb, ".zshrc"), rc)
		instRunInstall(t, h, sb, filepath.Join(sb, "data"), workDir, nil)

		got := clitest.Read(t, filepath.Join(sb, ".zshrc"))
		if strings.Contains(got, "devbrain installer") || strings.Contains(got, "local/bin") {
			t.Errorf("legacy rc lines survived migration:\n%s", got)
		}
	})

	t.Run("user rc content preserved", func(t *testing.T) {
		sb := t.TempDir()
		instInitDataDir(t, filepath.Join(sb, "data"))
		rc := "# my stuff\n\n# added by devbrain installer\nexport PATH=\"$HOME/.local/bin:$PATH\"\n"
		clitest.WriteFile(t, filepath.Join(sb, ".zshrc"), rc)
		instRunInstall(t, h, sb, filepath.Join(sb, "data"), workDir, nil)

		got := clitest.Read(t, filepath.Join(sb, ".zshrc"))
		if !strings.Contains(got, "# my stuff") {
			t.Errorf("user rc content lost:\n%s", got)
		}
	})

	// ── 5. migration: legacy capture copy seeds config.json with pinned data ──
	t.Run("pinned data path seeds config.json", func(t *testing.T) {
		sb := t.TempDir()
		pinnedData := filepath.Join(sb, "olddata")
		instInitDataDir(t, pinnedData)

		// Plant a legacy bash capture copy with a pinned DATA= path.
		hooksDir := filepath.Join(sb, ".claude", "hooks")
		if err := os.MkdirAll(hooksDir, 0o755); err != nil {
			t.Fatal(err)
		}
		captureScript := "#!/bin/bash\nDATA=\"${DEVBRAIN_DATA:-" + pinnedData + "}\"\n"
		clitest.WriteExec(t, filepath.Join(hooksDir, "devbrain-capture.sh"), captureScript)

		// A separate "unused" data dir (the default); the pinned path must win.
		unusedData := filepath.Join(sb, "data")
		instInitDataDir(t, unusedData)

		instRunInstall(t, h, sb, unusedData, workDir, map[string]string{"DEVBRAIN_DATA": ""})

		cfg := filepath.Join(sb, ".config", "devbrain", "config.json")
		b, err := os.ReadFile(cfg)
		if err != nil {
			t.Fatalf("config.json not found: %v", err)
		}
		if !strings.Contains(string(b), pinnedData) {
			t.Errorf("config.json does not contain pinned path %q:\n%s", pinnedData, b)
		}
	})

	t.Run("legacy capture copy deleted", func(t *testing.T) {
		sb := t.TempDir()
		pinnedData := filepath.Join(sb, "olddata")
		instInitDataDir(t, pinnedData)

		hooksDir := filepath.Join(sb, ".claude", "hooks")
		if err := os.MkdirAll(hooksDir, 0o755); err != nil {
			t.Fatal(err)
		}
		captureScript := "#!/bin/bash\nDATA=\"${DEVBRAIN_DATA:-" + pinnedData + "}\"\n"
		clitest.WriteExec(t, filepath.Join(hooksDir, "devbrain-capture.sh"), captureScript)

		unusedData := filepath.Join(sb, "data")
		instInitDataDir(t, unusedData)

		instRunInstall(t, h, sb, unusedData, workDir, map[string]string{"DEVBRAIN_DATA": ""})

		if _, err := os.Stat(filepath.Join(hooksDir, "devbrain-capture.sh")); err == nil {
			t.Error("legacy devbrain-capture.sh was not deleted")
		}
	})

	// ── 6. component scoping: --only capture has no skills or flusher ─────────
	// Use the same sandbox from the first group of subtests above; we already
	// ran --only capture there. A fresh sandbox makes this clearer.
	t.Run("--only capture: no skills", func(t *testing.T) {
		sb := t.TempDir()
		instInitDataDir(t, filepath.Join(sb, "data"))
		instRunInstall(t, h, sb, filepath.Join(sb, "data"), workDir, nil)

		if _, err := os.Stat(filepath.Join(sb, ".claude", "skills", "continue")); err == nil {
			t.Error("--only capture installed skills/continue")
		}
	})

	t.Run("--only capture: no flusher plist", func(t *testing.T) {
		sb := t.TempDir()
		instInitDataDir(t, filepath.Join(sb, "data"))
		instRunInstall(t, h, sb, filepath.Join(sb, "data"), workDir, nil)

		if _, err := os.Stat(filepath.Join(sb, "Library", "LaunchAgents", "com.devbrain.flush.plist")); err == nil {
			t.Error("--only capture wrote flusher plist")
		}
	})
}

// instBuildJSONStr builds a minimal JSON string for testing. Kept as a helper
// in case a future test needs programmatic JSON construction without python3.
func instBuildJSONStr(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}
