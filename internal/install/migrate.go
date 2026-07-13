package install

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/TheWeiHu/devbrain/internal/config"
	"github.com/TheWeiHu/devbrain/internal/jsonedit"
)

// legacyHookRe matches hook commands the bash installer registered: script
// copies under a hooks/ dir, plus the bare devbrain-import alias.
var legacyHookRe = regexp.MustCompile(`/hooks/devbrain-.*\.(sh|py)|devbrain-import`)

// pinnedDataRe extracts the data path install.sh sed-pinned into the capture
// copy: DATA="${DEVBRAIN_DATA:-<path>}".
var pinnedDataRe = regexp.MustCompile(`\$\{DEVBRAIN_DATA:-([^}]*)\}`)

// legacyClaudeFiles is the exact file list scripts/uninstall.sh removed from
// ~/.claude/hooks.
var legacyClaudeFiles = []string{
	"devbrain_lib.py", "devbrain-hook-common.sh", "devbrain-project-key.sh",
	"devbrain-capture.sh", "devbrain-capture-response.sh", "devbrain-capture-memory.sh",
	"devbrain-flush.sh", "devbrain-rebuild.sh", "devbrain-brain.sh", "devbrain-todo.sh",
	"devbrain-capture-gbrain.sh", "devbrain-session-start-nudge.sh",
	"devbrain-link-preferences.sh", "devbrain-import", "devbrain-queue.py",
	"devbrain-dashboard.html", "devbrain-queue-dashboard.html", "model_pricing.py",
	"devbrain", "devbrain.version", "devbrain-uninstall.sh", "devbrain-release.sh",
	// installed by the legacy ORCHESTRATOR (ensure_marker_hook), not install.sh —
	// the one file the legacy uninstaller never knew about
	"devbrain-turn-marker.sh",
}

// legacyCodexFiles is the same for ~/.codex/hooks.
var legacyCodexFiles = []string{
	"devbrain_lib.py", "devbrain-project-key.sh", "devbrain-capture.sh",
	"devbrain-capture-response.sh", "devbrain-capture-gbrain.sh",
	"devbrain-session-start-nudge.sh",
}

// migrate detects and removes a legacy bash-installer footprint. Check-then-act
// everywhere: a fresh machine skips silently, a second run is a no-op. When
// seedConfig is set (install), the sed-pinned data path is recovered into
// config.json BEFORE the old capture copy is deleted.
func migrate(c *ctx, seedConfig bool) {
	// (pre) Recover the pinned data path from the old capture copy.
	if seedConfig && !exists(config.Path()) && os.Getenv("DEVBRAIN_DATA") == "" {
		if b, err := os.ReadFile(filepath.Join(c.claude, "hooks", "devbrain-capture.sh")); err == nil {
			if m := pinnedDataRe.FindStringSubmatch(string(b)); m != nil {
				if pinned := strings.TrimSpace(m[1]); pinned != "" && !strings.Contains(pinned, "$") {
					if config.Write(pinned) == nil {
						fmt.Fprintf(c.stdout, "  migrated pinned data home -> %s (%s)\n", config.Path(), pinned)
					}
				}
			}
		}
	}

	// (a) Drop legacy hook commands from settings.json / hooks.json.
	for _, sf := range []string{
		filepath.Join(c.claude, "settings.json"),
		filepath.Join(c.codex, "hooks.json"),
	} {
		cmds := matchingHookCommands(sf, legacyHookRe.MatchString)
		if len(cmds) == 0 {
			continue
		}
		backup(sf)
		if jsonedit.UnregisterHook(sf, cmds) == nil {
			fmt.Fprintf(c.stdout, "  removed %d legacy hook entr%s from %s\n", len(cmds), plural(len(cmds), "y", "ies"), sf)
		}
	}

	// (a2) Retire hook-based capture. Capture is sweep-based now: drop the
	// four retired capture hooks from Claude's settings.json and EVERY
	// devbrain hook from Codex's hooks.json (Codex gets no hooks at all —
	// its trust gate silently disabled them on each rewrite anyway).
	retired := map[string]bool{"capture": true, "response": true, "subagent-response": true, "memory": true}
	isDevbrainHook := func(cmd string) bool {
		if !goHookShapeRe.MatchString(cmd) {
			return false
		}
		fields := strings.Fields(cmd)
		return strings.Contains(filepath.Base(fields[len(fields)-3]), "devbrain")
	}
	retiredClaude := func(cmd string) bool {
		fields := strings.Fields(cmd)
		return isDevbrainHook(cmd) && retired[fields[len(fields)-1]]
	}
	if sf := filepath.Join(c.claude, "settings.json"); true {
		if cmds := matchingHookCommands(sf, retiredClaude); len(cmds) > 0 {
			backup(sf)
			if jsonedit.UnregisterHook(sf, cmds) == nil {
				fmt.Fprintf(c.stdout, "  removed %d retired capture hook entr%s from %s (capture is sweep-based now)\n", len(cmds), plural(len(cmds), "y", "ies"), sf)
			}
		}
	}
	if sf := filepath.Join(c.codex, "hooks.json"); true {
		if cmds := matchingHookCommands(sf, isDevbrainHook); len(cmds) > 0 {
			backup(sf)
			if jsonedit.UnregisterHook(sf, cmds) == nil {
				fmt.Fprintf(c.stdout, "  removed %d devbrain Codex hook entr%s from %s (Codex capture is sweep-based, no hooks)\n", len(cmds), plural(len(cmds), "y", "ies"), sf)
			}
		}
	}

	// (b) Delete the legacy script copies.
	removed := 0
	for _, f := range legacyClaudeFiles {
		if p := filepath.Join(c.claude, "hooks", f); exists(p) && os.Remove(p) == nil {
			removed++
		}
	}
	for _, f := range legacyCodexFiles {
		if p := filepath.Join(c.codex, "hooks", f); exists(p) && os.Remove(p) == nil {
			removed++
		}
	}
	if removed > 0 {
		fmt.Fprintf(c.stdout, "  removed %d legacy installed script cop%s\n", removed, plural(removed, "y", "ies"))
	}

	// (c) A launchd plist that still points at the devbrain-flush.sh copy.
	plist := c.plistPath()
	if b, err := os.ReadFile(plist); err == nil && strings.Contains(string(b), "devbrain-flush.sh") {
		_ = run("launchctl", "unload", plist)
		if os.Remove(plist) == nil {
			fmt.Fprintln(c.stdout, "  removed legacy flusher LaunchAgent")
		}
	}

	// (d) systemd user units / crontab lines pointing at devbrain-flush.sh.
	sd := filepath.Join(c.home, ".config", "systemd", "user")
	sdRemoved := false
	for _, f := range []string{"devbrain-flush.service", "devbrain-flush.timer"} {
		p := filepath.Join(sd, f)
		if b, err := os.ReadFile(p); err == nil && strings.Contains(string(b), "devbrain-flush.sh") {
			_ = run("systemctl", "--user", "disable", "--now", "devbrain-flush.timer")
			if os.Remove(p) == nil {
				sdRemoved = true
			}
		}
	}
	if sdRemoved {
		_ = run("systemctl", "--user", "daemon-reload")
		fmt.Fprintln(c.stdout, "  removed legacy systemd flush units")
	}
	if c.goos != "darwin" {
		removeCrontabLegacyFlush()
	}

	// (e) ~/.local/bin symlinks into the old ~/.claude/hooks copies.
	lb := filepath.Join(c.home, ".local", "bin")
	if entries, err := os.ReadDir(lb); err == nil {
		for _, e := range entries {
			if !strings.HasPrefix(e.Name(), "devbrain") && e.Name() != "nightshift" {
				continue
			}
			p := filepath.Join(lb, e.Name())
			if target, err := os.Readlink(p); err == nil && strings.Contains(target, "/.claude/hooks") {
				if os.Remove(p) == nil {
					fmt.Fprintf(c.stdout, "  removed legacy symlink %s\n", c.display(p))
				}
			}
		}
	}

	// (f) The old nightshift toolset dir (the fleet now lives in the binary).
	ns := filepath.Join(c.claude, "nightshift")
	if exists(ns) && os.RemoveAll(ns) == nil {
		fmt.Fprintln(c.stdout, "  removed legacy nightshift toolset dir")
	}

	// (g) Shell-rc PATH lines the bash installer added (marker + next line).
	for _, rc := range []string{".zshrc", ".bash_profile", ".bashrc", ".profile"} {
		p := filepath.Join(c.home, rc)
		if stripInstallerRcLines(p) {
			fmt.Fprintf(c.stdout, "  removed devbrain PATH entry from %s\n", c.display(p))
		}
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// matchingHookCommands parses a settings file and returns every hook command
// the predicate accepts. Unreadable/malformed files return nothing (the
// register path will surface real corruption).
func matchingHookCommands(path string, match func(string) bool) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	obj, err := jsonedit.Parse(b)
	if err != nil {
		return nil
	}
	hooks := obj.Get("hooks")
	if hooks == nil || hooks.Kind != jsonedit.Object {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, m := range hooks.Obj {
		if m.Val.Kind != jsonedit.Array {
			continue
		}
		for _, entry := range m.Val.Arr {
			if entry.Kind != jsonedit.Object {
				continue
			}
			inner := entry.Get("hooks")
			if inner == nil || inner.Kind != jsonedit.Array {
				continue
			}
			for _, h := range inner.Arr {
				if h.Kind != jsonedit.Object {
					continue
				}
				cv := h.Get("command")
				if cv != nil && cv.Kind == jsonedit.String && match(cv.Str) && !seen[cv.Str] {
					seen[cv.Str] = true
					out = append(out, cv.Str)
				}
			}
		}
	}
	return out
}

// removeCrontabLegacyFlush filters devbrain-flush.sh lines out of the user
// crontab (Linux legacy fallback scheduler).
func removeCrontabLegacyFlush() {
	if !haveCmd("crontab") {
		return
	}
	out, err := execOutput("crontab", "-l")
	if err != nil || !strings.Contains(out, "devbrain-flush.sh") {
		return
	}
	var kept []string
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if strings.Contains(l, "devbrain-flush.sh") {
			continue
		}
		kept = append(kept, l)
	}
	runStdin(strings.Join(kept, "\n")+"\n", "crontab", "-")
}

// stripInstallerRcLines removes the `# added by devbrain installer` marker and
// the export line that follows it. Returns true when something was removed.
func stripInstallerRcLines(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), "# added by devbrain installer") {
		return false
	}
	lines := strings.Split(string(b), "\n")
	var kept []string
	skip := 0
	for _, l := range lines {
		if skip > 0 {
			skip--
			continue
		}
		if strings.Contains(l, "# added by devbrain installer") {
			skip = 1 // drop this marker line and the export line after it
			continue
		}
		kept = append(kept, l)
	}
	return os.WriteFile(path, []byte(strings.Join(kept, "\n")), 0o644) == nil
}
