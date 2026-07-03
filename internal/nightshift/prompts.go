package nightshift

// Worker prompt resolution. The legacy orchestrator read prompts/ from disk
// beside itself; the binary embeds them, but a prompts/ dir next to the repo
// (or an assets/prompts checkout copy) still WINS so users can edit the
// text without rebuilding.

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/TheWeiHu/devbrain/assets"
	"github.com/TheWeiHu/devbrain/internal/gitx"
	"github.com/TheWeiHu/devbrain/internal/nightshift/plan"
)

// promptText resolves one prompt file: repo-adjacent disk copies first
// (assets/prompts or prompts inside the repo checkout), then the embed.
func promptText(repo, name string) string {
	for _, p := range []string{
		filepath.Join(repo, "assets", "prompts", name),
		filepath.Join(repo, "prompts", name),
	} {
		if b, err := os.ReadFile(p); err == nil {
			return string(b)
		}
	}
	b, _ := assets.Prompts.ReadFile("prompts/" + name)
	return string(b)
}

// DrainRules is the per-turn --append-system-prompt text for /work turns.
func DrainRules(repo string) string { return promptText(repo, "nightshift-drain.txt") }

// PlanRules is the planning-turn prompt used when the queue empties.
func PlanRules(repo string) string { return promptText(repo, "nightshift-plan.txt") }

// WarnCIScope scans the repo's workflows and warns (with the fix) about any
// that would fire CI on per-task PRs into nightshift. Warn-only.
func (o *Orch) WarnCIScope() {
	dir := filepath.Join(o.Opt.Repo, ".github", "workflows")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	unsafe := ""
	for _, e := range ents {
		n := e.Name()
		if filepath.Ext(n) != ".yml" && filepath.Ext(n) != ".yaml" {
			continue
		}
		if plan.CIScopeUnsafe(filepath.Join(dir, n)) {
			unsafe += " " + n
		}
	}
	if unsafe == "" {
		return
	}
	fmt.Fprintf(o.Out, "orch: ⚠ CI-scope: workflow(s)%s fire CI on per-task PRs into nightshift.\n", unsafe)
	fmt.Fprintln(o.Out, "orch:   Each failing push will email you. The local merge gate already replicates")
	fmt.Fprintln(o.Out, "orch:   the suite per branch, so per-task PR CI is redundant.")
	fmt.Fprintln(o.Out, "orch:   Fix — scope the pull_request trigger to main only:")
	fmt.Fprintln(o.Out, "orch:     on:")
	fmt.Fprintln(o.Out, "orch:       pull_request:")
	fmt.Fprintln(o.Out, "orch:         branches: [main]")
	fmt.Fprintln(o.Out, "orch:   (warn-only; your repo's YAML is not auto-modified)")
}

// wtRepo is a git handle on an arbitrary worktree path.
func wtRepo(dir string) gitx.Repo { return gitx.Repo{Dir: dir} }
