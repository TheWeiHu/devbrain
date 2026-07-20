package projectkey

import (
	"os"
	"path/filepath"
	"strings"
)

// Aliases loads the rename map from $DATA/preferences/project-aliases
// (legacy top-level import-aliases / .import-aliases fallbacks): lines of
// `old-name = project-key`, # comments. A repo rename can't be seen offline
// — old checkouts keep the old remote URL — so this file is the one knob
// that reroutes every capture path: live identity, import routing, and
// dead-worktree matching. Empty map when absent.
func Aliases(data string) map[string]string {
	aliases := map[string]string{}
	for _, name := range []string{
		filepath.Join("preferences", "project-aliases"),
		"import-aliases", ".import-aliases",
	} {
		raw, err := os.ReadFile(filepath.Join(data, name))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(raw), "\n") {
			line, _, _ = strings.Cut(line, "#")
			if o, k, found := strings.Cut(strings.TrimSpace(line), "="); found {
				aliases[strings.TrimSpace(o)] = strings.TrimSpace(k)
			}
		}
		break
	}
	return aliases
}

// Canonical maps key through the alias table — exact key first, then the
// bare repo name after the first "__" (GitHub owners can't contain
// underscores, so the first "__" ends the owner; this is the form import
// routing matches dir names against, so one `old-repo = key` line covers
// every path). Unaliased keys pass through unchanged.
func Canonical(key string, aliases map[string]string) string {
	if k := aliases[key]; k != "" {
		return Sanitize(k)
	}
	if _, repo, ok := strings.Cut(key, "__"); ok {
		if k := aliases[repo]; k != "" {
			return Sanitize(k)
		}
	}
	return key
}
