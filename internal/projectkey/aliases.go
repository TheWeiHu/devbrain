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
				if o, k := strings.TrimSpace(o), strings.TrimSpace(k); o != "" && k != "" {
					aliases[o] = k
				}
			}
		}
		break
	}
	return aliases
}

// Canonical maps key through the alias table: exact key first, then bare
// repo name after the first "__" — owner-preserving, so `old = owner__new`
// never captures a different owner's same-named repo; a cross-owner
// transfer needs a full-key line. Unaliased keys pass through unchanged.
func Canonical(key string, aliases map[string]string) string {
	target := aliases[key]
	if target == "" {
		if owner, repo, ok := strings.Cut(key, "__"); ok {
			if k := aliases[repo]; k != "" && strings.HasPrefix(Sanitize(k), owner+"__") {
				target = k
			}
		}
	}
	if target == "" {
		return key
	}
	return Sanitize(target)
}
