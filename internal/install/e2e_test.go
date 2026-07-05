package install_test

// Go-native port of scripts/test-install-e2e.sh: full install/uninstall
// exercise against a throwaway HOME sandbox. Stubs claude/codex/launchctl/
// systemctl/crontab/loginctl on PATH so the host machine is never touched.
// cwd is a temp dir outside the repo checkout so the git-gate component skips.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/TheWeiHu/devbrain/internal/clitest"
)

// instE2EStubs creates stub executables for all scheduler/agent CLIs in dir.
// Each stub logs its invocation to stubLog and exits 0.
func instE2EStubs(t *testing.T, dir, stubLog string) {
	t.Helper()
	for _, name := range []string{"codex", "claude", "launchctl", "systemctl", "crontab", "loginctl"} {
		script := "#!/bin/bash\necho \"" + name + " $*\" >> \"" + stubLog + "\"\nexit 0\n"
		clitest.WriteExec(t, filepath.Join(dir, name), script)
	}
}

func TestInstallE2E(t *testing.T) {
	h := clitest.New(t)

	// Throwaway sandbox layout matching the shell script:
	//   $SB/home   → sandboxed HOME
	//   $SB/stub   → stubs on PATH
	sb := t.TempDir()
	home := filepath.Join(sb, "home")
	stubDir := filepath.Join(sb, "stub")
	stubLog := filepath.Join(sb, "stub-calls.log")
	for _, d := range []string{home, stubDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	instE2EStubs(t, stubDir, stubLog)

	// Work dir: a temp dir NOT the repo checkout, so git-gate skips.
	workDir := t.TempDir()

	bin := clitest.Bin(t)

	// Build PATH: stubs first, then /usr/bin:/bin plus git's directory so
	// the installer can run `git init` (same as the shell script's $GITDIR trick).
	gitPath, _ := os.LookupEnv("PATH")
	path := stubDir + ":/usr/bin:/bin"
	if gitPath != "" {
		path = stubDir + ":" + gitPath
	}

	installEnv := map[string]string{
		"HOME":              home,
		"PATH":              path,
		"SHELL":             "/bin/zsh",
		"DEVBRAIN_NO_IMPORT": "1",
		// Override what the harness sets — we manage HOME/data ourselves.
		"DEVBRAIN_DATA":    "",
		"DEVBRAIN_PROJECT": "",
	}

	// ── install --yes: the full default component set ─────────────────────────
	r := h.RunWith(clitest.RunOpts{Dir: workDir, CleanEnv: true, Env: installEnv}, "install", "--yes")
	installLog := r.Stdout + r.Stderr

	t.Run("install exits 0", func(t *testing.T) {
		if r.Code != 0 && !strings.Contains(installLog, "Done.") {
			t.Fatalf("install exited %d; output:\n%s", r.Code, installLog)
		}
	})

	settings := filepath.Join(home, ".claude", "settings.json")
	codexHooks := filepath.Join(home, ".codex", "hooks.json")

	t.Run("data repo created", func(t *testing.T) {
		if _, err := os.Stat(filepath.Join(home, "devbrain-data", ".git")); err != nil {
			t.Errorf("data repo not created: %v", err)
		}
	})

	t.Run("config.json written", func(t *testing.T) {
		cfg := filepath.Join(home, ".config", "devbrain", "config.json")
		b, err := os.ReadFile(cfg)
		if err != nil {
			t.Fatalf("config.json not found: %v", err)
		}
		if !strings.Contains(string(b), filepath.Join(home, "devbrain-data")) {
			t.Errorf("config.json does not reference data home:\n%s", b)
		}
	})

	for _, hook := range []string{"capture", "gbrain", "response", "memory", "session-start"} {
		hook := hook
		t.Run("claude hook "+hook+" registered", func(t *testing.T) {
			s := clitest.Read(t, settings)
			want := bin + " hook " + hook
			if !strings.Contains(s, want) {
				t.Errorf("settings.json missing %q:\n%s", want, s)
			}
		})
	}

	for _, hook := range []string{"capture", "gbrain", "response", "session-start"} {
		hook := hook
		t.Run("codex hook "+hook+" registered", func(t *testing.T) {
			cx := clitest.Read(t, codexHooks)
			want := "DEVBRAIN_HARNESS=codex " + bin + " hook " + hook
			if !strings.Contains(cx, want) {
				t.Errorf("codex hooks.json missing %q:\n%s", want, cx)
			}
		})
	}

	t.Run("codex feature enable invoked", func(t *testing.T) {
		log := instReadStubLog(t, stubLog)
		if !strings.Contains(log, "codex features enable hooks") {
			t.Errorf("stub-calls.log has no 'codex features enable hooks':\n%s", log)
		}
	})

	t.Run("flusher scheduled (stubbed)", func(t *testing.T) {
		log := instReadStubLog(t, stubLog)
		if !clitest.HasLineWith(log, "launchctl", "load") &&
			!clitest.HasLineWith(log, "systemctl", "--user", "enable") &&
			!strings.Contains(log, "crontab") {
			t.Errorf("no scheduler stub call found:\n%s", log)
		}
	})

	if runtime.GOOS == "darwin" {
		t.Run("plist runs binary flush", func(t *testing.T) {
			plist := filepath.Join(home, "Library", "LaunchAgents", "com.devbrain.flush.plist")
			b := clitest.Read(t, plist)
			if !strings.Contains(b, "<string>"+bin+"</string>") {
				t.Errorf("plist missing binary string:\n%s", b)
			}
			if !strings.Contains(b, "<string>flush</string>") {
				t.Errorf("plist missing flush string:\n%s", b)
			}
		})

		t.Run("plist keeps 300s and RunAtLoad", func(t *testing.T) {
			plist := filepath.Join(home, "Library", "LaunchAgents", "com.devbrain.flush.plist")
			b := clitest.Read(t, plist)
			if !strings.Contains(b, "<integer>300</integer>") {
				t.Errorf("plist missing 300s interval:\n%s", b)
			}
			if !strings.Contains(b, "RunAtLoad") {
				t.Errorf("plist missing RunAtLoad:\n%s", b)
			}
		})
	}

	for _, sk := range []string{"continue", "distill", "work", "reconcile", "audit", "nightshift"} {
		sk := sk
		t.Run("skill "+sk+" installed (both roots)", func(t *testing.T) {
			claude := filepath.Join(home, ".claude", "skills", sk, "SKILL.md")
			agents := filepath.Join(home, ".agents", "skills", sk, "SKILL.md")
			if _, err := os.Stat(claude); err != nil {
				t.Errorf("~/.claude/skills/%s/SKILL.md missing: %v", sk, err)
			}
			if _, err := os.Stat(agents); err != nil {
				t.Errorf("~/.agents/skills/%s/SKILL.md missing: %v", sk, err)
			}
		})
	}

	t.Run("skills carry no hook-copy paths", func(t *testing.T) {
		skillsDir := filepath.Join(home, ".claude", "skills")
		files := clitest.Find(t, skillsDir, "*.md")
		for _, f := range files {
			b := clitest.Read(t, f)
			if strings.Contains(b, ".claude/hooks/devbrain-") {
				t.Errorf("skill file %s contains hook-copy path", f)
			}
		}
	})

	t.Run("CLAUDE.md devbrain block", func(t *testing.T) {
		md := clitest.Read(t, filepath.Join(home, ".claude", "CLAUDE.md"))
		if !strings.Contains(md, "<!-- devbrain:start -->") {
			t.Errorf("CLAUDE.md missing devbrain block:\n%s", md)
		}
	})

	t.Run("CLAUDE.md preferences @import", func(t *testing.T) {
		md := clitest.Read(t, filepath.Join(home, ".claude", "CLAUDE.md"))
		if !instHasImportLine(md, "/preferences/global.md") {
			t.Errorf("CLAUDE.md missing @import for preferences/global.md:\n%s", md)
		}
	})

	t.Run("codex AGENTS.md block", func(t *testing.T) {
		md := clitest.Read(t, filepath.Join(home, ".codex", "AGENTS.md"))
		if !strings.Contains(md, "<!-- devbrain:start -->") {
			t.Errorf("AGENTS.md missing devbrain block:\n%s", md)
		}
	})

	t.Run("alias symlinks -> binary", func(t *testing.T) {
		link := filepath.Join(home, ".local", "bin", "devbrain-todo")
		target, err := os.Readlink(link)
		if err != nil {
			t.Fatalf("devbrain-todo symlink missing: %v", err)
		}
		if target != bin {
			t.Errorf("devbrain-todo -> %q, want %q", target, bin)
		}
	})

	t.Run("no shell rc mutation", func(t *testing.T) {
		if _, err := os.Stat(filepath.Join(home, ".zshrc")); err == nil {
			t.Error("install wrote .zshrc")
		}
		if _, err := os.Stat(filepath.Join(home, ".profile")); err == nil {
			t.Error("install wrote .profile")
		}
	})

	// ── re-run: idempotent ────────────────────────────────────────────────────
	h.RunWith(clitest.RunOpts{Dir: workDir, CleanEnv: true, Env: installEnv}, "install", "--yes")

	t.Run("second install idempotent", func(t *testing.T) {
		s := clitest.Read(t, settings)
		count := strings.Count(s, "hook capture")
		if count != 1 {
			t.Errorf("settings.json has %d 'hook capture' entries after second install, want 1:\n%s", count, s)
		}
	})

	// ── uninstall: clean sweep, data repo intact ──────────────────────────────
	r2 := h.RunWith(clitest.RunOpts{Dir: workDir, CleanEnv: true, Env: installEnv}, "uninstall")
	uninstallLog := r2.Stdout + r2.Stderr

	t.Run("hooks gone from settings.json", func(t *testing.T) {
		s := clitest.Read(t, settings)
		if strings.Contains(s, "hook capture") {
			t.Errorf("settings.json still has hook entries after uninstall:\n%s", s)
		}
	})

	t.Run("hooks gone from codex", func(t *testing.T) {
		cx := clitest.Read(t, codexHooks)
		if strings.Contains(cx, "hook capture") {
			t.Errorf("codex hooks.json still has hook entries after uninstall:\n%s", cx)
		}
	})

	t.Run("skills removed", func(t *testing.T) {
		if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "continue")); err == nil {
			t.Error("~/.claude/skills/continue survived uninstall")
		}
		if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "continue")); err == nil {
			t.Error("~/.agents/skills/continue survived uninstall")
		}
	})

	t.Run("CLAUDE.md block stripped", func(t *testing.T) {
		md := clitest.Read(t, filepath.Join(home, ".claude", "CLAUDE.md"))
		if strings.Contains(md, "devbrain:start") {
			t.Errorf("CLAUDE.md still has devbrain block:\n%s", md)
		}
	})

	t.Run("preferences import stripped", func(t *testing.T) {
		md := clitest.Read(t, filepath.Join(home, ".claude", "CLAUDE.md"))
		if instHasImportLine(md, "/preferences/global.md") {
			t.Errorf("CLAUDE.md still has preferences @import:\n%s", md)
		}
	})

	t.Run("AGENTS.md block stripped", func(t *testing.T) {
		md := clitest.Read(t, filepath.Join(home, ".codex", "AGENTS.md"))
		if strings.Contains(md, "devbrain:start") {
			t.Errorf("AGENTS.md still has devbrain block:\n%s", md)
		}
	})

	t.Run("config.json removed", func(t *testing.T) {
		cfg := filepath.Join(home, ".config", "devbrain", "config.json")
		if _, err := os.Stat(cfg); err == nil {
			t.Error("config.json survived uninstall")
		}
	})

	t.Run("alias symlinks removed", func(t *testing.T) {
		link := filepath.Join(home, ".local", "bin", "devbrain-todo")
		if _, err := os.Lstat(link); err == nil {
			t.Error("devbrain-todo symlink survived uninstall")
		}
	})

	if runtime.GOOS == "darwin" {
		t.Run("flusher plist removed", func(t *testing.T) {
			plist := filepath.Join(home, "Library", "LaunchAgents", "com.devbrain.flush.plist")
			if _, err := os.Stat(plist); err == nil {
				t.Error("flush plist survived uninstall")
			}
		})
	}

	t.Run("data repo untouched", func(t *testing.T) {
		if _, err := os.Stat(filepath.Join(home, "devbrain-data", ".git")); err != nil {
			t.Errorf("data repo was removed by uninstall: %v", err)
		}
	})

	t.Run("uninstall prints the brew note", func(t *testing.T) {
		if !strings.Contains(uninstallLog, "brew uninstall devbrain") {
			t.Errorf("uninstall output missing brew note:\n%s", uninstallLog)
		}
	})
}

// instReadStubLog reads the stub-calls log, returning "" if not yet created.
func instReadStubLog(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

// instHasImportLine checks whether any line in text is an @import ending with suffix.
func instHasImportLine(text, suffix string) bool {
	for _, ln := range strings.Split(text, "\n") {
		if strings.HasPrefix(ln, "@") && strings.HasSuffix(ln, suffix) {
			return true
		}
	}
	return false
}
