package hooks_test

// Go-native port of scripts/test-codex-hooks-feature.sh: verifies that
// `devbrain install --only capture,codex` enables the Codex hooks feature
// by calling `codex features enable hooks`, and gracefully handles the case
// where codex is not on PATH.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TheWeiHu/devbrain/internal/clitest"
)

// codexMockBin writes a fake `codex` that records its args to codex-calls.log.
func codexMockBin(t *testing.T, bindir string) {
	t.Helper()
	logFile := filepath.Join(bindir, "codex-calls.log")
	script := "#!/usr/bin/env bash\necho \"$*\" >> \"" + logFile + "\"\nexit 0\n"
	clitest.WriteExec(t, filepath.Join(bindir, "codex"), script)
}

// runInstallSandboxed runs `devbrain install --only capture,codex` in a fresh
// HOME sandbox with the given PATH and codexHome. Returns combined output.
func runInstallSandboxed(t *testing.T, h *clitest.Harness, sandboxHome, codexHome, path string) string {
	t.Helper()
	// create data/projects dir and a bare git repo for DEVBRAIN_DATA
	dataDir := filepath.Join(sandboxHome, "data")
	if err := os.MkdirAll(filepath.Join(dataDir, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	clitest.Git(t, "", "init", "-q", dataDir)

	r := h.RunWith(clitest.RunOpts{
		Env: map[string]string{
			"HOME":         sandboxHome,
			"PATH":         path,
			"SHELL":        "/bin/zsh",
			"DEVBRAIN_DATA": dataDir,
			"CODEX_HOME":   codexHome,
			// suppress interactive prompts
			"DEVBRAIN_NO_IMPORT":        "1",
			"DEVBRAIN_OPEN_DASHBOARD":   "0",
			"DEVBRAIN_NIGHTSHIFT":       "",
			"DEVBRAIN_GBRAIN":           "0",
			// reset any harness-level project override
			"DEVBRAIN_PROJECT": "",
		},
		Dir: sandboxHome,
	}, "install", "--only", "capture,codex", "--yes")
	return r.Stdout + r.Stderr
}

func TestCodexHooksFeature(t *testing.T) {
	// Require python3 is NOT needed — we don't shell out to it.
	// But we do need the built devbrain binary, which the harness provides.
	h := clitest.New(t)

	// Find a minimal clean PATH for subprocess (no real codex leaking in).
	sysPath := "/usr/bin:/bin"
	pyDir := ""
	if py, err := exec.LookPath("python3"); err == nil {
		pyDir = filepath.Dir(py)
	}
	if pyDir != "" && pyDir != "/usr/bin" && pyDir != "/bin" {
		sysPath = sysPath + ":" + pyDir
	}

	// ── Case 1: codex on PATH -> installer enables hooks feature ─────────────
	sb1 := t.TempDir()
	bin1 := filepath.Join(sb1, "bin")
	codexHome1 := filepath.Join(sb1, ".codex")
	if err := os.MkdirAll(bin1, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(codexHome1, 0o755); err != nil {
		t.Fatal(err)
	}
	codexMockBin(t, bin1)
	path1 := bin1 + ":/usr/bin:/bin"
	if pyDir != "" {
		path1 = bin1 + ":" + sysPath
	}
	out1 := runInstallSandboxed(t, h, sb1, codexHome1, path1)

	callsLog1 := filepath.Join(bin1, "codex-calls.log")
	callsContent1 := ""
	if b, err := os.ReadFile(callsLog1); err == nil {
		callsContent1 = string(b)
	}

	if !strings.Contains(callsContent1, "features enable hooks") {
		t.Errorf("installer invokes 'codex features enable hooks': not found in calls log %q", callsContent1)
	}
	if !strings.Contains(out1, "enabled Codex") && !strings.Contains(out1, "hooks") {
		// The message may vary; check either form.
		t.Logf("out1: %s", out1)
	}
	// The output must say we enabled the hooks feature.
	if !strings.Contains(out1, "enabled Codex") {
		t.Errorf("prints the enabled-feature confirmation: 'enabled Codex' missing\n%s", out1)
	}
	if _, err := os.Stat(filepath.Join(codexHome1, "hooks.json")); err != nil {
		t.Errorf("codex hooks.json still registered: %v", err)
	}
	if !strings.Contains(out1, "hooks feature enabled") {
		t.Errorf("final summary claims enabled: 'hooks feature enabled' missing\n%s", out1)
	}

	// ── Case 2: codex NOT on PATH -> graceful NOTE, install still succeeds ───
	sb2 := t.TempDir()
	codexHome2 := filepath.Join(sb2, ".codex")
	if err := os.MkdirAll(codexHome2, 0o755); err != nil {
		t.Fatal(err)
	}
	out2 := runInstallSandboxed(t, h, sb2, codexHome2, sysPath)

	if !strings.Contains(out2, "codex not on PATH") {
		t.Errorf("codex-absent prints the manual NOTE: 'codex not on PATH' missing\n%s", out2)
	}
	if _, err := os.Stat(filepath.Join(codexHome2, "hooks.json")); err != nil {
		t.Errorf("install still succeeds (hooks.json): %v", err)
	}
	if strings.Contains(out2, "hooks feature enabled") {
		t.Errorf("codex-absent summary does NOT claim enabled: found 'hooks feature enabled'\n%s", out2)
	}
	if !strings.Contains(out2, "enable hooks yourself") {
		t.Errorf("codex-absent summary tells user to enable: 'enable hooks yourself' missing\n%s", out2)
	}

	// ── Case 3: existing config.toml is backed up before the enable ──────────
	sb3 := t.TempDir()
	bin3 := filepath.Join(sb3, "bin")
	codexHome3 := filepath.Join(sb3, ".codex")
	if err := os.MkdirAll(bin3, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(codexHome3, 0o755); err != nil {
		t.Fatal(err)
	}
	codexMockBin(t, bin3)
	clitest.WriteFile(t, filepath.Join(codexHome3, "config.toml"), "[features]\nother = true\n")
	path3 := bin3 + ":" + sysPath
	runInstallSandboxed(t, h, sb3, codexHome3, path3)

	// A backup file must exist.
	entries, err := os.ReadDir(codexHome3)
	if err != nil {
		t.Fatal(err)
	}
	foundBak := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "config.toml.bak.") {
			foundBak = true
			break
		}
	}
	if !foundBak {
		t.Errorf("existing config.toml backed up before edit: no config.toml.bak.* found in %s", codexHome3)
	}
}
