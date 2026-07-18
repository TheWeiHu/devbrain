package install

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/TheWeiHu/devbrain/internal/config"
)

const (
	markerStart = "<!-- devbrain:start -->"
	markerEnd   = "<!-- devbrain:end -->"
)

// stripMarkerBlock removes every markerStart..markerEnd block (inclusive),
// exactly like the legacy awk: `$0==s {skip=1} !skip {print} $0==e {skip=0}`.
func stripMarkerBlock(content string) string {
	var out []string
	skip := false
	for _, line := range strings.Split(content, "\n") {
		if line == markerStart {
			skip = true
		}
		if !skip {
			out = append(out, line)
		}
		if line == markerEnd {
			skip = false
		}
	}
	return strings.Join(out, "\n")
}

// writeMarkerBlock strips any prior devbrain block from path and appends a
// fresh one (idempotent; user content outside the block is preserved; an
// unchanged result is not rewritten, so periodic refreshes are no-op writes).
func writeMarkerBlock(path, body string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw := ""
	if b, err := os.ReadFile(path); err == nil {
		raw = string(b)
	}
	existing := stripMarkerBlock(raw)
	if existing != "" && !strings.HasSuffix(existing, "\n") {
		existing += "\n" // legacy awk always ended lines with \n
	}
	out := existing + markerStart + "\n" + body + markerEnd + "\n"
	if out == raw {
		return nil
	}
	return os.WriteFile(path, []byte(out), 0o644)
}

// claudeMdBody is the standing instruction block for ~/.claude/CLAUDE.md.
func claudeMdBody(dataDisplay string) string {
	return fmt.Sprintf(`## devbrain (cross-project brain)

Every prompt is captured to the private data repo at `+"`%s`"+`
(routing by git remote -> `+"`projects/<project>/`"+`). On resume or when the
user asks "where was I" / "continue", run `+"`/continue`"+` to pull this project's
brain and refresh the live world. After meaningful progress, run `+"`/distill`"+`
to curate new log into brain pages.

**Query the brain before you answer or ask — make it your first lookup, not a
last resort.** Before answering a non-trivial question about a project, before
asking the user something the brain may already record, and whenever you pick
up or resume work, run `+"`gbrain search \"<terms>\"`"+` (or `+"`gbrain query \"<question>\"`"+`
with an OpenAI key) FIRST. The brain is usually faster and more current than
re-deriving from the code or asking — even mid-task, not just on `+"`/continue`"+`.
To READ a page a search surfaces, pass its FULL `+"`<project>/<page>`"+` slug from the
output to `+"`gbrain get \"<project>/<page>\" --fuzzy`"+` — not the bare page name (the
brain is one namespace, so a bare slug is `+"`page_not_found`"+`), and do not pipe the
read through `+"`2>/dev/null`"+`, which hides gbrain's own "Did you mean" fix-hints.

**End your final message of each turn with a one-sentence recap** of what
you actually did or concluded this turn — outcome, not preamble. devbrain's
sweep captures the last sentence of your final message as the turn's log
summary, so it must stand alone: name the concrete thing you changed (file,
flag, function) and the result, so a future session reading only that line
knows what happened without the surrounding conversation.
  Good: "Capped the captured recap at 500 chars and added a good/bad example to
  the install prompt; synced the live hook and CLAUDE.md."
  Bad:  "Done." / "Here's the summary above." / "Let me know if you need
  anything else." — a sign-off, a bare status, or a question is useless as a log
  line. Write the recap last; everything above it is working notes.
`, dataDisplay)
}

// agentsMdBody is the Codex counterpart for ~/.codex/AGENTS.md. prefs is the
// current global-preferences page ("" = no section) — AGENTS.md has no
// @import, so the content is inlined and machine-refreshed on every flush.
func agentsMdBody(dataDisplay, prefs string) string {
	body := fmt.Sprintf(`## devbrain (cross-project brain)

Every prompt is captured to the private data repo at `+"`%s`"+`
(routing by git remote -> `+"`projects/<project>/`"+`). On resume or when the
user asks "where was I" / "continue", run `+"`$continue`"+` to pull this project's
brain and refresh the live world. After meaningful progress, run `+"`$distill`"+`
to curate new log into brain pages. The devbrain skills are installed at
`+"`~/.agents/skills`"+`: `+"`$continue`"+`, `+"`$work`"+`, `+"`$distill`"+`, `+"`$reconcile`"+`, `+"`$audit`"+`.

**Query the brain before you answer or ask — make it your first lookup, not a
last resort.** Before answering a non-trivial question about a project, before
asking the user something the brain may already record, and whenever you pick
up or resume work, run `+"`gbrain search \"<terms>\"`"+` (or `+"`gbrain query \"<question>\"`"+`
with an OpenAI key) FIRST. To read a surfaced page, pass its full
`+"`<project>/<page>`"+` slug to `+"`gbrain get \"<project>/<page>\" --fuzzy`"+`.

**At the start of a session in a repo, brief yourself** — devbrain injects no
context into Codex, so fetch your own: run `+"`gbrain search \"<repo topic>\"`"+` and
`+"`devbrain todo list`"+` to see what the brain records and what's queued.

**End your final message of each turn with a one-sentence recap** of what
you actually did or concluded this turn — outcome, not preamble. devbrain
sweeps the last sentence of your final message from the transcript as the
turn's log summary, so it must stand alone: name the concrete thing you
changed (file, flag, function) and the result.
  Good: "Capped the captured recap at 500 chars and added a good/bad example to
  the install prompt; synced the live hook and CLAUDE.md."
  Bad:  "Done." / "Here's the summary above." / "Let me know if you need
  anything else." — a sign-off, a bare status, or a question is useless as a log
  line. Write the recap last; everything above it is working notes.
`, dataDisplay)
	if prefs != "" {
		body += "\n## Global preferences\n\n" +
			"Maintained by $distill at preferences/global.md; this copy is machine-refreshed — edit the page, not this block.\n\n" +
			prefs + "\n"
	}
	return body
}

// prefsSection reads the global preferences page, trimmed and capped at
// config.PrefsCapBytes (the same ceiling the dashboard meter shows). "" when
// the page is absent or empty.
func prefsSection() string {
	data, err := config.ResolveDataDir()
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(data, "preferences", "global.md"))
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(b))
	if len(s) > config.PrefsCapBytes {
		s = strings.ToValidUTF8(s[:config.PrefsCapBytes], "") // never split a rune
	}
	return s
}

// RefreshAgentsPrefs rebuilds the devbrain block in ~/.codex/AGENTS.md so the
// inlined preferences track the page. Run by every flush tick; a no-op when
// the file has no devbrain block (install --without codex) or nothing
// changed. Fail-open — capture must never die on an instruction refresh.
func RefreshAgentsPrefs() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	codex := os.Getenv("CODEX_HOME")
	if codex == "" {
		codex = filepath.Join(home, ".codex")
	}
	md := filepath.Join(codex, "AGENTS.md")
	raw, err := os.ReadFile(md)
	if err != nil || !strings.Contains(string(raw), markerStart) {
		return
	}
	data, err := config.ResolveDataDir()
	if err != nil {
		return
	}
	_ = writeMarkerBlock(md, agentsMdBody(display(data, home), prefsSection()))
}

func (c *ctx) writeClaudeMd() error {
	md := filepath.Join(c.claude, "CLAUDE.md")
	if err := writeMarkerBlock(md, claudeMdBody(c.display(c.data))); err != nil {
		return err
	}
	fmt.Fprintf(c.stdout, "  wrote devbrain block -> %s\n", md)
	return nil
}

func (c *ctx) writeAgentsMd() error {
	md := filepath.Join(c.codex, "AGENTS.md")
	if err := writeMarkerBlock(md, agentsMdBody(c.display(c.data), prefsSection())); err != nil {
		return err
	}
	fmt.Fprintf(c.stdout, "  wrote devbrain block -> %s\n", md)
	if exists(filepath.Join(c.codex, "AGENTS.override.md")) {
		fmt.Fprintf(c.stdout, "  NOTE: %s exists, so Codex will prefer it over AGENTS.md\n", filepath.Join(c.codex, "AGENTS.override.md"))
	}
	return nil
}

// stripClaudeMd removes the devbrain block and the managed preferences import
// lines from a user memory file (uninstall).
func stripClaudeMd(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var kept []string
	for _, line := range strings.Split(stripMarkerBlock(string(b)), "\n") {
		if strings.Contains(line, "devbrain: global preferences page") {
			continue
		}
		if strings.HasPrefix(line, "@") && strings.HasSuffix(line, "/preferences/global.md") {
			continue
		}
		kept = append(kept, line)
	}
	return os.WriteFile(path, []byte(strings.Join(kept, "\n")), 0o644) == nil
}
