package nightshift

import (
	"reflect"
	"strings"
	"testing"
)

// turnArgs is the frozen per-agent argv contract: claude keeps the exact
// legacy flags; codex prepends the (respelled) rules to the prompt because
// codex exec has no --append-system-prompt.
func TestTurnArgs(t *testing.T) {
	t.Parallel()
	rules := "NIGHTSHIFT rules: follow the /work protocol."
	opt := DefaultOptions()

	got := agentClaude.turnArgs("/work", rules, opt)
	want := []string{"-p", "/work",
		"--dangerously-skip-permissions",
		"--disallowedTools", "AskUserQuestion",
		"--append-system-prompt", rules}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("claude argv = %q", got)
	}

	// --model forwards to each binary's own flag: claude --model, codex -m.
	opt.Model = "sonnet"
	got = agentClaude.turnArgs("/work", rules, opt)
	if !reflect.DeepEqual(got, append(append([]string{}, want...), "--model", "sonnet")) {
		t.Errorf("claude argv with model = %q", got)
	}
	opt.Model = "gpt-5.1-codex"
	gotx := agentCodex.turnArgs("/work", rules, opt)
	if len(gotx) < 5 || gotx[0] != "exec" || gotx[2] != "-m" || gotx[3] != "gpt-5.1-codex" {
		t.Errorf("codex argv with model must carry -m before the prompt: %q", gotx)
	}
	if !strings.HasSuffix(gotx[len(gotx)-1], "$work") {
		t.Errorf("codex prompt must still be the last arg: %q", gotx)
	}

	opt = DefaultOptions()
	got = agentCodex.turnArgs("/work", rules, opt)
	if len(got) < 3 || got[0] != "exec" || got[1] != "--dangerously-bypass-approvals-and-sandbox" {
		t.Fatalf("codex argv shape = %q", got)
	}
	joined := strings.Join(got, " ")
	prompt := got[len(got)-1]
	if !strings.Contains(joined, `model_reasoning_effort="high"`) ||
		!strings.Contains(joined, `service_tier="default"`) ||
		!strings.Contains(joined, "--disable multi_agent") {
		t.Errorf("codex argv must pin safe run controls: %q", got)
	}
	if !strings.Contains(prompt, "NIGHTSHIFT rules") || !strings.HasSuffix(prompt, "$work") {
		t.Errorf("codex prompt must carry rules and end with $work:\n%s", prompt)
	}
	if !strings.Contains(prompt, "$work protocol") || !strings.Contains(prompt, "Do not spawn subagents") {
		t.Errorf("rules and zero-subagent budget must be present:\n%s", prompt)
	}

	opt.MaxSubagents = 2
	got = agentCodex.turnArgs("/work", rules, opt)
	joined = strings.Join(got, " ")
	if !strings.Contains(joined, "--enable multi_agent") ||
		!strings.Contains(joined, "agents.max_threads=3") ||
		!strings.Contains(got[len(got)-1], "at most 2 subagent(s)") {
		t.Errorf("nonzero subagent budget not enforced: %q", got)
	}
}

func TestCodexSkillRefs(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"/work":                          "$work",
		"BOTH of /work's brain reads":    "BOTH of $work's brain reads",
		"run /distill then /continue":    "run $distill then $continue",
		"run /work. Then rest":           "run $work. Then rest",
		".nightshift/followups.md":       ".nightshift/followups.md",
		"cd /workspace && ls":            "cd /workspace && ls",
		"see https://x.test/work/deploy": "see https://x.test/work/deploy",
		"cat /work/notes.txt":            "cat /work/notes.txt",
		"open /work.md now":              "open /work.md now",
	} {
		if got := codexSkillRefs(in); got != want {
			t.Errorf("codexSkillRefs(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseAgentsAndSlots(t *testing.T) {
	t.Parallel()

	opt, err := ParseArgs([]string{"--agents", "claude=1,codex=2"})
	if err != nil {
		t.Fatal(err)
	}
	if opt.Workers != 3 {
		t.Errorf("Workers = %d, want 3", opt.Workers)
	}
	for i, want := range []agentKind{agentClaude, agentCodex, agentCodex, agentClaude} {
		if got := opt.AgentFor(i); got != want { // slot 3 wraps (rescale growth)
			t.Errorf("AgentFor(%d) = %s, want %s", i, got, want)
		}
	}
	if got := opt.AgentBins(); !reflect.DeepEqual(got, []string{"claude", "codex"}) {
		t.Errorf("AgentBins = %q", got)
	}

	// Interleaved expansion: a worker cap that keeps only a prefix keeps the
	// mix (claude=2,codex=2 capped at 2 -> one of each, not two claude).
	opt, err = ParseArgs([]string{"--agents", "claude=2,codex=2"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(opt.Agents, []agentKind{agentClaude, agentCodex, agentClaude, agentCodex}) {
		t.Errorf("interleave = %v", opt.Agents)
	}

	// Bare kind: the whole default fleet.
	opt, err = ParseArgs([]string{"--agents", "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if opt.Workers != 3 || opt.AgentFor(0) != agentCodex || opt.AgentFor(2) != agentCodex {
		t.Errorf("bare codex: workers=%d agents=%v", opt.Workers, opt.Agents)
	}

	// Default (no --agents): all claude, single binary.
	opt, _ = ParseArgs(nil)
	if opt.AgentFor(0) != agentClaude || !reflect.DeepEqual(opt.AgentBins(), []string{"claude"}) {
		t.Errorf("default fleet must be all claude: %v", opt.Agents)
	}

	for _, bad := range [][]string{
		{"--agents", "gemini=2"},
		{"--agents", "codex=x"},
		{"--agents", ""},
		{"--agents", "codex=0"},
		{"--agents", "codex=2", "--workers", "3"},
		{"--workers", "3", "--agents", "codex=2"},
		{"--agents", "codex=1", "--tmux"},
		{"--tmux", "--agents", "claude=1,codex=1"},
	} {
		if _, err := ParseArgs(bad); err == nil {
			t.Errorf("ParseArgs(%q) must fail", bad)
		}
	}

	// tmux stays fine for an all-claude --agents spec.
	if _, err := ParseArgs([]string{"--agents", "claude=2", "--tmux"}); err != nil {
		t.Errorf("tmux + all-claude agents must parse: %v", err)
	}
}
