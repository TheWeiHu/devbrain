package install

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TheWeiHu/devbrain/internal/config"
)

// setupHome builds a throwaway HOME with stubbed schedulers on PATH (so no
// test can ever touch the host's launchd/systemd/cron) and pins every env
// knob the installer reads. Returns the home dir.
func setupHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	stub := filepath.Join(home, ".stubbin")
	if err := os.MkdirAll(stub, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"launchctl", "systemctl", "crontab", "loginctl", "codex"} {
		script := "#!/bin/sh\necho \"$0 $*\" >> \"" + filepath.Join(home, "stub-calls.log") + "\"\nexit 0\n"
		if err := os.WriteFile(filepath.Join(stub, s), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("PATH", stub+":/usr/bin:/bin")
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("DEVBRAIN_DATA", "")
	t.Setenv("DEVBRAIN_NO_IMPORT", "1")
	t.Setenv("DEVBRAIN_OPEN_DASHBOARD", "0") // never spawn `<test-binary> queue`
	t.Setenv("DEVBRAIN_NIGHTSHIFT", "")
	t.Setenv("DEVBRAIN_GBRAIN", "0")
	t.Chdir(home) // git-gate must not see the real checkout
	return home
}

func install(t *testing.T, args ...string) (string, int) {
	t.Helper()
	var out bytes.Buffer
	rc := Run(args, &out, &out, strings.NewReader(""))
	return out.String(), rc
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// legacy fixture: the exact footprint the bash installer left behind.
func buildLegacyInstall(t *testing.T, home, pinnedData string) {
	t.Helper()
	hooks := filepath.Join(home, ".claude", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	// sed-pinned capture copy + friends
	capture := "#!/usr/bin/env bash\nDATA=\"${DEVBRAIN_DATA:-" + pinnedData + "}\"\necho hi\n"
	for _, f := range []string{"devbrain-capture.sh", "devbrain-flush.sh", "devbrain-todo.sh"} {
		if err := os.WriteFile(filepath.Join(hooks, f), []byte(capture), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"devbrain_lib.py", "model_pricing.py", "devbrain-import", "devbrain"} {
		if err := os.WriteFile(filepath.Join(hooks, f), []byte("# legacy\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// old settings.json: devbrain entries + a grouped entry with a FOREIGN sibling
	settings := `{
  "model": "opus",
  "hooks": {
    "UserPromptSubmit": [
      {"hooks": [{"type": "command", "command": "` + hooks + `/devbrain-capture.sh"}]}
    ],
    "Stop": [
      {"hooks": [
        {"type": "command", "command": "` + hooks + `/devbrain-capture-response.sh"},
        {"type": "command", "command": "/usr/local/bin/their-hook.sh"}
      ]}
    ]
  }
}`
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	// old codex hooks.json
	codexHooks := filepath.Join(home, ".codex", "hooks")
	if err := os.MkdirAll(codexHooks, 0o755); err != nil {
		t.Fatal(err)
	}
	cj := `{"hooks": {"UserPromptSubmit": [{"hooks": [{"type": "command", "command": "DEVBRAIN_HARNESS=codex ` + codexHooks + `/devbrain-capture.sh"}]}]}}`
	if err := os.WriteFile(filepath.Join(home, ".codex", "hooks.json"), []byte(cj), 0o644); err != nil {
		t.Fatal(err)
	}
	// old flusher plist (mentions devbrain-flush.sh)
	la := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(la, 0o755); err != nil {
		t.Fatal(err)
	}
	plist := "<plist><string>" + hooks + "/devbrain-flush.sh</string></plist>"
	if err := os.WriteFile(filepath.Join(la, "com.devbrain.flush.plist"), []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}
	// rc-file marker lines
	rc := "# mine\n\n# added by devbrain installer\nexport PATH=\"$HOME/.local/bin:$PATH\"\n"
	if err := os.WriteFile(filepath.Join(home, ".zshrc"), []byte(rc), 0o644); err != nil {
		t.Fatal(err)
	}
	// ~/.local/bin symlinks into the old hooks dir (+ one foreign entry)
	lb := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(lb, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, l := range []string{"devbrain", "devbrain-todo"} {
		if err := os.Symlink(filepath.Join(hooks, l), filepath.Join(lb, l)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(lb, "unrelated"), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	// old nightshift toolset dir
	if err := os.MkdirAll(filepath.Join(home, ".claude", "nightshift", "prompts"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationFromLegacyInstall(t *testing.T) {
	home := setupHome(t)
	pinned := filepath.Join(home, "pinned-data")
	buildLegacyInstall(t, home, pinned)

	out, rc := install(t, "--yes")
	if rc != 0 {
		t.Fatalf("install failed (%d):\n%s", rc, out)
	}

	// config.json seeded with the recovered pinned path
	cfg := mustRead(t, config.Path())
	if !strings.Contains(cfg, pinned) {
		t.Errorf("config.json not seeded with pinned path %q:\n%s", pinned, cfg)
	}
	// data repo created AT the pinned path (not the default)
	if _, err := os.Stat(filepath.Join(pinned, ".git")); err != nil {
		t.Errorf("data repo not created at pinned path: %v", err)
	}

	s := mustRead(t, filepath.Join(home, ".claude", "settings.json"))
	if strings.Contains(s, "devbrain-capture.sh") || strings.Contains(s, "devbrain-capture-response.sh") {
		t.Errorf("legacy hook entries survived:\n%s", s)
	}
	if !strings.Contains(s, "/usr/local/bin/their-hook.sh") {
		t.Errorf("foreign sibling hook was dropped:\n%s", s)
	}
	if !strings.Contains(s, " hook gbrain") || !strings.Contains(s, " hook session-start") {
		t.Errorf("new binary hook entries missing:\n%s", s)
	}
	if strings.Contains(s, " hook capture") || strings.Contains(s, " hook response") {
		t.Errorf("retired capture hooks were registered (capture is sweep-based):\n%s", s)
	}
	if !strings.Contains(s, `"model": "opus"`) {
		t.Errorf("unrelated settings key lost:\n%s", s)
	}
	cx := mustRead(t, filepath.Join(home, ".codex", "hooks.json"))
	if strings.Contains(cx, "devbrain-capture.sh") {
		t.Errorf("legacy codex hook entry survived:\n%s", cx)
	}
	if strings.Contains(cx, "DEVBRAIN_HARNESS=codex ") {
		t.Errorf("codex hook entries written (Codex gets no hooks — capture is sweep-based):\n%s", cx)
	}

	// old script copies deleted
	for _, f := range []string{"devbrain-capture.sh", "devbrain_lib.py", "model_pricing.py", "devbrain"} {
		if _, err := os.Stat(filepath.Join(home, ".claude", "hooks", f)); err == nil {
			t.Errorf("legacy copy %s not deleted", f)
		}
	}

	// plist replaced: no more devbrain-flush.sh; runs `<binary> flush` (darwin path
	// is exercised regardless of host GOOS via ctx.goos default = runtime.GOOS)
	plist := filepath.Join(home, "Library", "LaunchAgents", "com.devbrain.flush.plist")
	if b, err := os.ReadFile(plist); err == nil {
		if strings.Contains(string(b), "devbrain-flush.sh") {
			t.Errorf("legacy plist survived:\n%s", b)
		}
		if !strings.Contains(string(b), "<string>flush</string>") {
			t.Errorf("new plist does not run '<binary> flush':\n%s", b)
		}
	} else {
		// non-darwin: the legacy plist must at least be gone
		t.Logf("no plist on this GOOS (ok on linux)")
	}

	// rc marker + export removed, user line kept
	zshrc := mustRead(t, filepath.Join(home, ".zshrc"))
	if strings.Contains(zshrc, "devbrain installer") || strings.Contains(zshrc, ".local/bin") {
		t.Errorf("legacy rc lines survived:\n%s", zshrc)
	}
	if !strings.Contains(zshrc, "# mine") {
		t.Errorf("user rc content lost:\n%s", zshrc)
	}

	// old symlinks into ~/.claude/hooks removed; foreign file kept; fresh
	// back-compat aliases now point at the binary
	lb := filepath.Join(home, ".local", "bin")
	if target, err := os.Readlink(filepath.Join(lb, "devbrain-todo")); err != nil || strings.Contains(target, "/.claude/hooks") {
		t.Errorf("devbrain-todo alias not re-pointed (target=%q err=%v)", target, err)
	}
	if _, err := os.Stat(filepath.Join(lb, "unrelated")); err != nil {
		t.Errorf("foreign ~/.local/bin file removed: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(lb, "devbrain")); err == nil {
		t.Errorf("legacy devbrain symlink survived")
	}

	// nightshift dir gone
	if _, err := os.Stat(filepath.Join(home, ".claude", "nightshift")); err == nil {
		t.Errorf("legacy nightshift dir survived")
	}

	// idempotent: a second run must not change settings.json
	before := mustRead(t, filepath.Join(home, ".claude", "settings.json"))
	if out2, rc2 := install(t, "--yes"); rc2 != 0 {
		t.Fatalf("second install failed:\n%s", out2)
	}
	after := mustRead(t, filepath.Join(home, ".claude", "settings.json"))
	if before != after {
		t.Errorf("second run changed settings.json:\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
}

func TestMarkerBlockIdempotency(t *testing.T) {
	dir := t.TempDir()
	md := filepath.Join(dir, "CLAUDE.md")
	if err := os.WriteFile(md, []byte("# my rules\nkeep me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeMarkerBlock(md, "body one\n"); err != nil {
		t.Fatal(err)
	}
	if err := writeMarkerBlock(md, "body two\n"); err != nil {
		t.Fatal(err)
	}
	got := mustRead(t, md)
	if strings.Contains(got, "body one") {
		t.Errorf("old block not stripped:\n%s", got)
	}
	if strings.Count(got, markerStart) != 1 || strings.Count(got, markerEnd) != 1 {
		t.Errorf("marker block not exactly once:\n%s", got)
	}
	if !strings.Contains(got, "keep me") {
		t.Errorf("user content lost:\n%s", got)
	}
	// strip removes the whole block and only the block
	stripped := stripMarkerBlock(got)
	if strings.Contains(stripped, "body two") || strings.Contains(stripped, markerStart) {
		t.Errorf("strip left block content:\n%s", stripped)
	}
	if !strings.Contains(stripped, "keep me") {
		t.Errorf("strip removed user content:\n%s", stripped)
	}
}

func TestTCCGuardRefusesProtectedPath(t *testing.T) {
	home := setupHome(t)
	t.Setenv("DEVBRAIN_DATA", filepath.Join(home, "Desktop", "brain"))
	out, rc := install(t, "--yes")
	if rc == 0 {
		t.Fatalf("install must refuse a Desktop data home non-interactively:\n%s", out)
	}
	if !strings.Contains(out, "refusing") {
		t.Errorf("expected a refusal message:\n%s", out)
	}
}

func TestComponentToggleMatrix(t *testing.T) {
	// --only skills: writes skills, but no hooks, no plist, no CLAUDE.md block
	home := setupHome(t)
	if out, rc := install(t, "--only", "skills", "--yes"); rc != 0 {
		t.Fatalf("--only skills failed:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "continue", "SKILL.md")); err != nil {
		t.Errorf("--only skills did not install skills: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "work", "SKILL.md")); err != nil {
		t.Errorf("--only skills did not install ~/.agents skills: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json")); err == nil && strings.Contains(string(b), "hook capture") {
		t.Errorf("--only skills registered hooks:\n%s", b)
	}
	if _, err := os.Stat(filepath.Join(home, "Library", "LaunchAgents", "com.devbrain.flush.plist")); err == nil {
		t.Errorf("--only skills wrote the flusher plist")
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "CLAUDE.md")); err == nil {
		t.Errorf("--only skills wrote CLAUDE.md")
	}

	// --only capture: the gbrain trace hook yes, skills no. (Prompt capture
	// itself is sweep-based — no capture hook exists.)
	home2 := setupHome(t)
	if out, rc := install(t, "--only", "capture", "--yes"); rc != 0 {
		t.Fatalf("--only capture failed:\n%s", out)
	}
	s := mustRead(t, filepath.Join(home2, ".claude", "settings.json"))
	if !strings.Contains(s, " hook gbrain") {
		t.Errorf("--only capture missing the gbrain hook:\n%s", s)
	}
	if strings.Contains(s, " hook capture") || strings.Contains(s, " hook response") || strings.Contains(s, " hook session-start") {
		t.Errorf("--only capture registered retired or other components' hooks:\n%s", s)
	}
	if _, err := os.Stat(filepath.Join(home2, ".claude", "skills", "continue")); err == nil {
		t.Errorf("--only capture installed skills")
	}

	// --without codex: no hooks.json, no AGENTS.md
	home3 := setupHome(t)
	if out, rc := install(t, "--without", "codex,flusher,git-gate", "--yes"); rc != 0 {
		t.Fatalf("--without codex failed:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(home3, ".codex", "hooks.json")); err == nil {
		t.Errorf("--without codex wrote hooks.json")
	}
	if _, err := os.Stat(filepath.Join(home3, ".codex", "AGENTS.md")); err == nil {
		t.Errorf("--without codex wrote AGENTS.md")
	}

	// unknown component is an error
	if _, rc := install(t, "--only", "nonsense"); rc != 1 {
		t.Errorf("unknown component must exit 1, got %d", rc)
	}
}

func TestDryRunPreviewsWithoutWriting(t *testing.T) {
	home := setupHome(t)
	out, rc := install(t, "--dry-run", "--yes")
	if rc != 0 {
		t.Fatalf("--dry-run failed:\n%s", out)
	}
	for _, want := range []string{"nothing below is written", "settings.json", "config.json", "(dry run"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run output missing %q:\n%s", want, out)
		}
	}
	// The preview must not touch a single target path.
	for _, p := range []string{
		config.Path(),
		filepath.Join(home, "devbrain-data", ".git"),
		filepath.Join(home, ".claude", "settings.json"),
		filepath.Join(home, ".claude", "CLAUDE.md"),
		filepath.Join(home, ".codex", "hooks.json"),
		filepath.Join(home, "Library", "LaunchAgents", "com.devbrain.flush.plist"),
	} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("dry-run wrote %s", p)
		}
	}
	// --explain is a preview too (annotated), never a mutation.
	if _, rc := install(t, "--explain", "--yes"); rc != 0 {
		t.Errorf("--explain should exit 0")
	}
	if _, err := os.Stat(config.Path()); err == nil {
		t.Errorf("--explain wrote config.json")
	}
}

// The global gbrain install is gated: it runs only on explicit consent, and
// always with the pinned package.
func TestGbrainInstallGatedAndPinned(t *testing.T) {
	newHome := func() string {
		home := setupHome(t)
		t.Setenv("DEVBRAIN_GBRAIN", "") // undecided — let the gate decide
		// stub bun into the on-PATH stub dir so consent would install
		bun := filepath.Join(home, ".stubbin", "bun")
		script := "#!/bin/sh\necho \"$0 $*\" >> \"" + filepath.Join(home, "stub-calls.log") + "\"\nexit 0\n"
		if err := os.WriteFile(bun, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		return home
	}

	// no consent (unattended --yes): bun is never invoked
	home := newHome()
	if _, rc := install(t, "--yes"); rc != 0 {
		t.Fatal("install --yes failed")
	}
	if b, _ := os.ReadFile(filepath.Join(home, "stub-calls.log")); strings.Contains(string(b), "add -g gbrain") {
		t.Errorf("unattended --yes ran a global gbrain install:\n%s", b)
	}

	// with --install-deps and an override: bun installs the pinned package
	home2 := newHome()
	t.Setenv("DEVBRAIN_GBRAIN_PACKAGE", "gbrain@9.9.9")
	if _, rc := install(t, "--yes", "--install-deps"); rc != 0 {
		t.Fatal("install --install-deps failed")
	}
	b, _ := os.ReadFile(filepath.Join(home2, "stub-calls.log"))
	if !strings.Contains(string(b), "add -g gbrain@9.9.9") {
		t.Errorf("--install-deps did not run the pinned global install:\n%s", b)
	}
}

// A failed gbrain install must say WHY (bun's error line) and how to retry
// with a different package source — not just "install failed".
func TestGbrainInstallFailureSurfacesReason(t *testing.T) {
	home := setupHome(t)
	t.Setenv("DEVBRAIN_GBRAIN", "")
	bun := filepath.Join(home, ".stubbin", "bun")
	script := "#!/bin/sh\necho 'error: No matching version for gbrain@0.18.2' >&2\nexit 1\n"
	if err := os.WriteFile(bun, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	out, rc := install(t, "--yes", "--install-deps")
	if rc != 0 {
		t.Fatalf("install must stay non-fatal on gbrain failure:\n%s", out)
	}
	for _, want := range []string{
		"install failed",
		"bun: error: No matching version for gbrain@0.18.2",
		"DEVBRAIN_GBRAIN_PACKAGE",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("failure output missing %q:\n%s", want, out)
		}
	}
}

func TestUninstallReversesInstall(t *testing.T) {
	home := setupHome(t)
	if out, rc := install(t, "--yes"); rc != 0 {
		t.Fatalf("install failed:\n%s", out)
	}
	var out bytes.Buffer
	if rc := Uninstall(nil, &out, &out); rc != 0 {
		t.Fatalf("uninstall failed:\n%s", out.String())
	}
	s := mustRead(t, filepath.Join(home, ".claude", "settings.json"))
	if strings.Contains(s, "hook capture") {
		t.Errorf("uninstall left hook entries:\n%s", s)
	}
	if _, err := os.Stat(config.Path()); err == nil {
		t.Errorf("uninstall left config.json")
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "continue")); err == nil {
		t.Errorf("uninstall left skills")
	}
	// the data repo must survive
	if _, err := os.Stat(filepath.Join(home, "devbrain-data", ".git")); err != nil {
		t.Errorf("uninstall touched the data repo: %v", err)
	}
	if md := mustRead(t, filepath.Join(home, ".claude", "CLAUDE.md")); strings.Contains(md, "devbrain:start") {
		t.Errorf("uninstall left the CLAUDE.md block:\n%s", md)
	}
}

func TestLinkPreferences(t *testing.T) {
	home := setupHome(t)
	t.Setenv("DEVBRAIN_DATA", filepath.Join(home, "d"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "cc"))
	mem := filepath.Join(home, "cc", "CLAUDE.md")

	var out bytes.Buffer
	if rc := LinkPreferences(nil, &out, &out); rc != 0 {
		t.Fatalf("link failed:\n%s", out.String())
	}
	got := mustRead(t, mem)
	// the page sits under $HOME, so the import is written ~-relative
	if !strings.Contains(got, "@~/d/preferences/global.md") {
		t.Errorf("import line missing:\n%s", got)
	}
	// idempotent
	if rc := LinkPreferences(nil, &out, &out); rc != 0 {
		t.Fatal("re-link failed")
	}
	if n := strings.Count(mustRead(t, mem), "/preferences/global.md"); n != 1 {
		t.Errorf("import line duplicated (%d)", n)
	}
	// unlink drops the managed lines, keeps user content
	if err := os.WriteFile(mem, []byte("keep\n"+mustRead(t, mem)), 0o644); err != nil {
		t.Fatal(err)
	}
	if rc := LinkPreferences([]string{"--unlink"}, &out, &out); rc != 0 {
		t.Fatal("unlink failed")
	}
	got = mustRead(t, mem)
	if strings.Contains(got, "/preferences/global.md") || !strings.Contains(got, "keep") {
		t.Errorf("unlink wrong:\n%s", got)
	}
}

func TestBinaryPathIsAbsolute(t *testing.T) {
	p := BinaryPath()
	if !filepath.IsAbs(p) {
		t.Errorf("BinaryPath not absolute: %q", p)
	}
}
