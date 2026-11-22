package transcript

// Codex opens a thread by injecting its own context as role=user
// response_items: the AGENTS.md chain wrapped in <INSTRUCTIONS> (with an
// <environment_context> block appended) and, after a cwd change, a bare
// <environment_context>. Nobody typed those. Captured as prompts they open
// every Codex log with a ~10 KB copy of AGENTS.md, which a brain fold then
// reads as owner instructions and re-learns in every project.

import (
	"strings"
	"testing"
)

const codexInjected = `{"type":"session_meta","payload":{"cwd":"/codex/inj"}}
{"type":"turn_context","payload":{"collaboration_mode":{"settings":{"model":"gpt-5.3"}}}}
{"type":"response_item","timestamp":"2026-08-01T00:00:00Z","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# AGENTS.md instructions for /codex/inj\n\n<INSTRUCTIONS>\n## House rules\n\n- Never push to main.\n</INSTRUCTIONS>\n<environment_context>\n  <cwd>/codex/inj</cwd>\n  <shell>zsh</shell>\n</environment_context>"}]}}
{"type":"response_item","timestamp":"2026-08-01T00:00:01Z","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fix the flaky test"}]}}
{"type":"response_item","timestamp":"2026-08-01T00:00:05Z","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Pinned the clock."}]}}
{"type":"event_msg","timestamp":"2026-08-01T00:00:06Z","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":10,"cached_input_tokens":0,"output_tokens":5}}}}
{"type":"response_item","timestamp":"2026-08-01T00:01:00Z","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>\n  <cwd>/codex/inj/sub</cwd>\n</environment_context>"}]}}
{"type":"response_item","timestamp":"2026-08-01T00:01:01Z","payload":{"type":"message","role":"user","content":"second task"}}
{"type":"response_item","timestamp":"2026-08-01T00:01:05Z","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Done."}]}}
{"type":"event_msg","timestamp":"2026-08-01T00:01:06Z","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":20,"cached_input_tokens":0,"output_tokens":3}}}}
`

func TestCodexInjectedContextIsNotAPrompt(t *testing.T) {
	t.Parallel()
	path := writeFixture(t, "codex-injected.jsonl", codexInjected)
	want := []string{
		`dt=2026-08-01T00:00:01Z|cwd=/codex/inj|prompt="fix the flaky test"|texts=["Pinned the clock."]|tools=|files=|turn_ts=2026-08-01T00:00:06Z|tok=10/5/0/0|model=gpt-5.3`,
		`dt=2026-08-01T00:01:01Z|cwd=/codex/inj|prompt="second task"|texts=["Done."]|tools=|files=|turn_ts=2026-08-01T00:01:06Z|tok=20/3/0/0|model=gpt-5.3`,
	}
	// Injected context is never a prompt, whichever way synthetic prompts
	// (the <skill> markers) are treated.
	checkTurns(t, Turns(path, 0, true), want)
	checkTurns(t, Turns(path, 0, false), want)
	for _, turn := range Turns(path, 0, false) {
		if strings.Contains(turn.Prompt, "AGENTS.md") || strings.Contains(turn.Prompt, "environment_context") {
			t.Errorf("Codex-injected context leaked into a prompt: %q", turn.Prompt)
		}
	}
}

func TestStripCodexInjected(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, in, want string }{
		{"instructions-only", "# AGENTS.md instructions for /x\n\n<INSTRUCTIONS>\n- rule\n</INSTRUCTIONS>", ""},
		{"instructions-then-environment", "# AGENTS.md instructions\n\n<INSTRUCTIONS>\n- rule\n</INSTRUCTIONS>\n<environment_context>\n  <cwd>/x</cwd>\n</environment_context>", ""},
		{"environment-only", "<environment_context>\n  <cwd>/x</cwd>\n</environment_context>", ""},
		{"legacy-user-instructions", "<user_instructions>\nbe terse\n</user_instructions>", ""},
		{"environment-then-typed-text", "<environment_context>\n  <cwd>/x</cwd>\n</environment_context>\n\nfix the flaky test", "fix the flaky test"},
		{"typed-text-untouched", "fix the flaky test", "fix the flaky test"},
		{"prose-mention-untouched", "why does <environment_context> appear in my logs?", "why does <environment_context> appear in my logs?"},
		{"unclosed-block-untouched", "<environment_context>\n  <cwd>/x</cwd>", "<environment_context>\n  <cwd>/x</cwd>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := stripCodexInjected(c.in); got != c.want {
				t.Errorf("stripCodexInjected(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
