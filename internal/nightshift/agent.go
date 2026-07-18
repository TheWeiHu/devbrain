package nightshift

// The one place worker harness differences live: which binary a worker slot
// runs and how a headless turn's argv is built. Everything downstream (turn
// lifecycle, merge, gate, token backfill) is harness-neutral.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type agentKind string

const (
	agentClaude agentKind = "claude"
	agentCodex  agentKind = "codex"
)

func (k agentKind) bin() string { return string(k) }

// turnArgs builds one headless turn's argv. Claude carries the drain rules
// via --append-system-prompt; codex has no equivalent, so the rules are
// prepended to the prompt, with slash-skill tokens respelled to the $-skills
// codex has installed (~/.agents/skills). model is the run's --model (empty =
// each CLI's default); it forwards to whichever binary the slot runs — claude
// `-p --model` or codex `exec -m` — so a homogeneous fleet of either agent
// honors it. A mixed fleet shares one model string across two disjoint
// namespaces (claude aliases vs codex model ids), so it only makes sense when
// both understand the value; per-worker selection is the --worker-model growth
// path.
func (k agentKind) turnArgs(prompt, rules, model string) []string {
	if k == agentCodex {
		args := []string{"exec", "--dangerously-bypass-approvals-and-sandbox"}
		if model != "" {
			args = append(args, "-m", model)
		}
		return append(args, codexSkillRefs(rules)+"\n\n"+codexSkillRefs(prompt))
	}
	args := []string{"-p", prompt,
		"--dangerously-skip-permissions",
		"--disallowedTools", "AskUserQuestion",
		"--append-system-prompt", rules}
	if model != "" {
		args = append(args, "--model", model)
	}
	return args
}

// skillRefRe matches a leading /work-style skill token; the boundary guards
// keep mid-path tokens (.nightshift/followups.md), URLs, and /workspace
// untouched. A match followed by "/" or ".<word>" is a path (/work/file,
// /work.md), not a skill — skipped below, since RE2 has no lookahead.
var skillRefRe = regexp.MustCompile(`(^|[\s"'(])/(work|distill|continue)\b`)

func codexSkillRefs(s string) string {
	var b strings.Builder
	last := 0
	for _, m := range skillRefRe.FindAllStringSubmatchIndex(s, -1) {
		end := m[1]
		pathLike := end < len(s) && (s[end] == '/' ||
			(s[end] == '.' && end+1 < len(s) && isWordByte(s[end+1])))
		if pathLike {
			continue
		}
		b.WriteString(s[last:m[3]]) // up to and including the boundary char
		b.WriteByte('$')
		b.WriteString(s[m[4]:m[5]]) // the skill name
		last = end
	}
	b.WriteString(s[last:])
	return b.String()
}

func isWordByte(c byte) bool {
	return c == '_' || ('0' <= c && c <= '9') || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}

// parseAgents expands an --agents spec into per-slot kinds. Accepted:
// "claude=2,codex=2" (counts) or a bare kind "codex" (all defaultN slots).
// Kinds are interleaved round-robin (claude=2,codex=2 -> c,x,c,x) so any
// prefix — a fixed-set worker cap or a live downscale — keeps the mix
// instead of dropping whichever kind was listed last.
func parseAgents(spec string, defaultN int) ([]agentKind, error) {
	var kinds []agentKind
	var counts []int
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, count := part, 0
		if k, v, found := strings.Cut(part, "="); found {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return nil, fmt.Errorf("orch: --agents: bad count in %q", part)
			}
			name, count = k, n
		} else {
			count = defaultN
		}
		kind := agentKind(name)
		if kind != agentClaude && kind != agentCodex {
			return nil, fmt.Errorf("orch: --agents: unknown agent %q (claude|codex)", name)
		}
		kinds = append(kinds, kind)
		counts = append(counts, count)
	}
	var out []agentKind
	for {
		took := false
		for i := range kinds {
			if counts[i] > 0 {
				out = append(out, kinds[i])
				counts[i]--
				took = true
			}
		}
		if !took {
			break
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("orch: --agents: no workers in %q", spec)
	}
	return out, nil
}

// AgentFor binds slot i to its kind. Modulo keeps the mapping total under a
// fixed-set worker cap (truncation) and live rescale growth (the launch
// ratio repeats).
func (o Options) AgentFor(i int) agentKind {
	if len(o.Agents) == 0 {
		return agentClaude
	}
	return o.Agents[i%len(o.Agents)]
}

// AgentBins is the deduped list of binaries this run needs on PATH.
func (o Options) AgentBins() []string {
	seen := map[string]bool{}
	var bins []string
	for i := 0; i < o.Workers; i++ {
		b := o.AgentFor(i).bin()
		if !seen[b] {
			seen[b] = true
			bins = append(bins, b)
		}
	}
	return bins
}
