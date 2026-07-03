package install

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/TheWeiHu/devbrain/internal/config"
	"github.com/TheWeiHu/devbrain/internal/jsonedit"
)

// goHookShapeRe matches the shape of commands this installer registers:
// `[DEVBRAIN_HARNESS=codex ]<program> hook <event>`.
var goHookShapeRe = regexp.MustCompile(`^(DEVBRAIN_HARNESS=codex )?(\S+) hook [a-z-]+$`)

// isOurHookCommand recognizes a registered devbrain hook command: the right
// shape AND a program that is either named devbrain (any install location —
// survives a brew relocation) or the currently running binary (covers test
// binaries and renamed installs).
func isOurHookCommand(cmd string) bool {
	m := goHookShapeRe.FindStringSubmatch(cmd)
	if m == nil {
		return false
	}
	prog := m[2]
	return strings.Contains(filepath.Base(prog), "devbrain") || prog == BinaryPath()
}

// Uninstall reverses `devbrain install` plus the legacy bash-installer
// footprint. The data repo is never touched.
func Uninstall(args []string, stdout, stderr io.Writer) int {
	c, err := newCtx(stdout, stderr, os.Stdin)
	if err != nil {
		fmt.Fprintf(stderr, "uninstall: %v\n", err)
		return 1
	}
	c.data = config.DataDir()

	// 1. Flusher (new form) + legacy sweep (old copies, plists, rc lines, …).
	c.removeFlusher()
	migrate(c, false)

	// 2. Drop the binary's hook entries from settings.json / hooks.json.
	for _, sf := range []string{
		filepath.Join(c.claude, "settings.json"),
		filepath.Join(c.codex, "hooks.json"),
	} {
		cmds := matchingHookCommands(sf, isOurHookCommand)
		if len(cmds) == 0 {
			continue
		}
		backup(sf)
		if jsonedit.UnregisterHook(sf, cmds) == nil {
			fmt.Fprintf(c.stdout, "removed devbrain hooks from %s\n", sf)
		}
	}

	// 3. Back-compat symlinks on ~/.local/bin (ours by name): what install
	//    creates (backCompatAliases) plus legacy shims only uninstall sweeps.
	lb := filepath.Join(c.home, ".local", "bin")
	for _, alias := range append(append([]string{}, backCompatAliases...), legacyAliases...) {
		p := filepath.Join(lb, alias)
		if fi, err := os.Lstat(p); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			_ = os.Remove(p)
		}
	}

	// 4. git-gate reversal (install recorded the checkout it configured).
	gateFile := filepath.Join(c.claude, ".git-gate-repo")
	if b, err := os.ReadFile(gateFile); err == nil {
		repo := strings.TrimSpace(string(b))
		if repo != "" {
			out, _ := exec.Command("git", "-C", repo, "config", "--local", "core.hooksPath").Output()
			if strings.TrimSpace(string(out)) == "scripts/git-hooks" {
				if run("git", "-C", repo, "config", "--local", "--unset", "core.hooksPath") == nil {
					fmt.Fprintf(c.stdout, "removed git-gate (core.hooksPath in %s)\n", repo)
				}
			}
		}
		_ = os.Remove(gateFile)
	}

	// 5. Skills.
	c.removeSkills()

	// 6. Marker blocks + the managed preferences @import.
	if stripClaudeMd(filepath.Join(c.claude, "CLAUDE.md")) {
		fmt.Fprintf(c.stdout, "removed devbrain block from %s\n", filepath.Join(c.claude, "CLAUDE.md"))
	}
	codexMd := filepath.Join(c.codex, "AGENTS.md")
	if b, err := os.ReadFile(codexMd); err == nil {
		stripped := stripMarkerBlock(string(b))
		if stripped != string(b) && os.WriteFile(codexMd, []byte(stripped), 0o644) == nil {
			fmt.Fprintf(c.stdout, "removed devbrain block from %s\n", codexMd)
		}
	}

	// 7. The machine config — wiring, not data (the data repo stays).
	if p := config.Path(); exists(p) {
		_ = os.Remove(p)
		_ = os.Remove(filepath.Dir(p)) // only if empty
		fmt.Fprintf(c.stdout, "removed %s\n", p)
	}

	fmt.Fprintf(c.stdout, "Done. The data repo (%s) was left untouched.\n", c.display(c.data))
	fmt.Fprintln(c.stdout, "The devbrain binary itself is not removed — `brew uninstall devbrain` (or delete it from PATH) if you're done with it.")
	return 0
}

// execOutput captures a command's stdout as a string.
func execOutput(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return string(out), err
}

// runStdin runs a command feeding it the given stdin.
func runStdin(stdin, name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	_ = cmd.Run()
}
