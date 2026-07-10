package install

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/TheWeiHu/devbrain/internal/config"
)

// prefMarker precedes the managed @import line in user memory so uninstall
// (and a hand-editing user) can see who owns it.
const prefMarker = "<!-- devbrain: global preferences page (managed by /distill; `devbrain uninstall` removes) -->"

// LinkPreferences is `devbrain link-preferences [--unlink]`: ensure (or
// remove) the single @import line in ~/.claude/CLAUDE.md pointing at the
// global preferences page /distill maintains. Idempotent; preserves all other
// memory content; a missing page is a safe no-op for Claude Code.
func LinkPreferences(args []string, stdout, stderr io.Writer) int {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(stderr, "link-preferences: %v\n", err)
		return 1
	}
	claudeDir := os.Getenv("CLAUDE_CONFIG_DIR")
	if claudeDir == "" {
		claudeDir = filepath.Join(home, ".claude")
	}
	mem := filepath.Join(claudeDir, "CLAUDE.md")
	data, err := config.ResolveDataDir()
	if err != nil {
		fmt.Fprintf(stderr, "link-preferences: %v\n", err)
		return 1
	}
	page := filepath.Join(data, "preferences", "global.md")
	disp := page
	if strings.HasPrefix(page, home+"/") {
		disp = "~/" + strings.TrimPrefix(page, home+"/")
	}
	importLine := "@" + disp

	if len(args) > 0 && args[0] == "--unlink" {
		b, err := os.ReadFile(mem)
		if err != nil {
			return 0
		}
		var kept []string
		for _, l := range strings.Split(string(b), "\n") {
			if strings.Contains(l, prefMarker) || strings.Contains(l, importLine) {
				continue
			}
			kept = append(kept, l)
		}
		if err := os.WriteFile(mem, []byte(strings.Join(kept, "\n")), 0o644); err != nil {
			fmt.Fprintf(stderr, "link-preferences: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "link-preferences: unwired from %s\n", mem)
		return 0
	}

	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		fmt.Fprintf(stderr, "link-preferences: %v\n", err)
		return 1
	}
	existing, _ := os.ReadFile(mem)
	if strings.Contains(string(existing), importLine) {
		fmt.Fprintf(stdout, "link-preferences: already wired (%s)\n", importLine)
		return 0
	}
	var out string
	if len(existing) > 0 {
		out = string(existing) + "\n" + prefMarker + "\n" + importLine + "\n"
	} else {
		out = prefMarker + "\n" + importLine + "\n"
	}
	if err := os.WriteFile(mem, []byte(out), 0o644); err != nil {
		fmt.Fprintf(stderr, "link-preferences: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "link-preferences: wired %s into %s\n", importLine, mem)
	return 0
}
