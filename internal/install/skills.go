package install

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/TheWeiHu/devbrain/assets"
)

// skillNames lists the embedded skills (the top-level dirs of assets/skills).
func skillNames() []string {
	entries, err := fs.ReadDir(assets.Skills, "skills")
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names
}

// skillsDirs returns the two user-level skills roots: Claude Code's and the
// cross-agent ~/.agents one Codex reads.
func (c *ctx) skillsDirs() []string {
	return []string{
		filepath.Join(c.claude, "skills"),
		filepath.Join(c.home, ".agents", "skills"),
	}
}

// installSkills extracts the embedded skills into both roots (rm -rf + write,
// as the legacy copy did — an upgrade fully replaces each skill dir).
func (c *ctx) installSkills() error {
	for _, root := range c.skillsDirs() {
		if err := os.MkdirAll(root, 0o755); err != nil {
			return err
		}
	}
	for _, name := range skillNames() {
		for _, root := range c.skillsDirs() {
			claudeRoot := filepath.Clean(root) == filepath.Clean(filepath.Join(c.claude, "skills"))
			dst := filepath.Join(root, name)
			if err := os.RemoveAll(dst); err != nil {
				return err
			}
			src := "skills/" + name
			err := fs.WalkDir(assets.Skills, src, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				rel, _ := filepath.Rel(src, p)
				target := filepath.Join(dst, rel)
				if d.IsDir() {
					return os.MkdirAll(target, 0o755)
				}
				b, err := fs.ReadFile(assets.Skills, p)
				if err != nil {
					return err
				}
				// Claude and Codex intentionally use different manual-invocation
				// schemas. Keep the embedded source valid for Codex, then add the
				// Claude-only frontmatter field only to Claude's installed copy.
				if claudeRoot && name == "nightshift" && rel == "SKILL.md" {
					b = addClaudeManualInvocation(b)
				}
				return os.WriteFile(target, b, 0o644)
			})
			if err != nil {
				return err
			}
		}
		fmt.Fprintf(c.stdout, "  installed skill %s -> ~/.claude/skills + ~/.agents/skills\n", name)
	}
	return nil
}

func addClaudeManualInvocation(b []byte) []byte {
	const opening = "---\n"
	if !strings.HasPrefix(string(b), opening) || strings.Contains(string(b), "\ndisable-model-invocation:") {
		return b
	}
	return append([]byte(opening+"disable-model-invocation: true\n"), b[len(opening):]...)
}

// removeSkills deletes the devbrain skills from both roots (uninstall).
func (c *ctx) removeSkills() {
	removed := false
	for _, name := range skillNames() {
		for _, root := range c.skillsDirs() {
			p := filepath.Join(root, name)
			if exists(p) && os.RemoveAll(p) == nil {
				removed = true
			}
		}
	}
	if removed {
		fmt.Fprintln(c.stdout, "removed devbrain skills (~/.claude/skills + ~/.agents/skills)")
	}
}
