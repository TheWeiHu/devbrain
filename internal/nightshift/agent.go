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
// codex has installed (~/.agents/skills).
func (k agentKind) turnArgs(prompt, rules string) []string {
	if k == agentCodex {
		return []string{"exec", "--dangerously-bypass-approvals-and-sandbox",
			codexSkillRefs(rules) + "\n\n" + codexSkillRefs(prompt)}
	}
	return []string{"-p", prompt,
		"--dangerously-skip-permissions",
		"--disallowedTools", "AskUserQuestion",
		"--append-system-prompt", rules}
}

// skillRefRe matches a leading /work-style skill token; the boundary guards
// keep paths (.nightshift/followups.md), URLs, and /workspace untouched.
var skillRefRe = regexp.MustCompile(`(^|[\s"'(])/(work|distill|continue)\b`)

func codexSkillRefs(s string) string {
	return skillRefRe.ReplaceAllString(s, `${1}$$${2}`)
}

// parseAgents expands an --agents spec into per-slot kinds. Accepted:
// "claude=2,codex=2" (slot-ordered counts) or a bare kind "codex" (all
// defaultN slots).
func parseAgents(spec string, defaultN int) ([]agentKind, error) {
	var out []agentKind
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
		for j := 0; j < count; j++ {
			out = append(out, kind)
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
