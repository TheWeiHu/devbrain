package transcript

import (
	"os"
	"path/filepath"
	"testing"
)

// A Codex rollout in event_msg style: event_msg user_message boundaries win
// (prefer_event_msg_user), the mirroring response_item role=user is folded
// into the turn's events, tools from exec/mcp/patch begins, token_count with
// last_token_usage (additive, cached subtracted) then total_token_usage (max
// semantics), file_change via path and file_path, a `<skill>` marker credited
// as Skill:ship, task_complete text + completed_at timestamp.
const codexEventMsg = `{"type":"session_meta","timestamp":"2026-03-01T10:00:00.000Z","payload":{"id":"0196a-1111-2222-3333-444444444444","cwd":"/codex/repo"}}
{"type":"turn_context","payload":{"cwd":"/codex/repo","model":"gpt-5.2-codex"}}
{"type":"event_msg","timestamp":"2026-03-01T10:00:01.000Z","payload":{"type":"user_message","message":"fix the flaky test"}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fix the flaky test"}]}}
{"type":"event_msg","payload":{"type":"exec_command_begin","command":["bash","-lc","go test"]}}
{"type":"event_msg","payload":{"type":"exec_command_begin"}}
{"type":"event_msg","payload":{"type":"mcp_tool_call_begin","tool_name":"browser_navigate"}}
{"type":"event_msg","payload":{"type":"mcp_tool_call_begin"}}
{"type":"event_msg","payload":{"type":"patch_apply_begin"}}
{"type":"response_item","payload":{"type":"file_change","path":"/codex/repo/pkg/flaky_test.go"}}
{"type":"response_item","payload":{"type":"file_change","file_path":"/codex/repo/pkg/other.go"}}
{"type":"event_msg","timestamp":"2026-03-01T10:00:20.000Z","payload":{"type":"agent_message","message":"Fixed the flaky test by pinning the clock."}}
{"type":"event_msg","timestamp":"2026-03-01T10:00:21.000Z","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":1000,"cached_input_tokens":400,"output_tokens":50}},"model":"gpt-5.2-codex-mini"}}
{"type":"event_msg","timestamp":"2026-03-01T10:05:00.000Z","payload":{"type":"user_message","message":"$ship it"}}
{"type":"response_item","timestamp":"2026-03-01T10:05:01.000Z","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<skill>\n<name>ship</name>\n<path>/skills/ship</path>\n</skill>\nbody"}]}}
{"type":"response_item","timestamp":"2026-03-01T10:05:30.000Z","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Shipping now."},{"type":"text","text":" PR opened."}]}}
{"type":"event_msg","timestamp":"2026-03-01T10:05:31.000Z","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":5000,"cached_input_tokens":4000,"output_tokens":300}}}}
{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":4500,"cached_input_tokens":4200,"output_tokens":250}}}}
{"type":"event_msg","payload":{"type":"task_complete","last_agent_message":"Shipped: PR #42.","completed_at":1772360757}}
`

// A rollout with only response_item role=user prompts: those become the turn
// boundaries, the leading `<skill>` marker is synthetic (filtered vs kept),
// the model comes from turn_context.collaboration_mode.settings.model, and
// string (not list) user content is accepted.
const codexRespItem = `{"type":"session_meta","payload":{"cwd":"/codex/two"}}
{"type":"turn_context","payload":{"collaboration_mode":{"settings":{"model":"gpt-5.3"}}}}
{"type":"response_item","timestamp":"2026-04-01T00:00:00Z","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<skill>\n<name>review</name>\n</skill>"}]}}
{"type":"response_item","timestamp":"2026-04-01T00:00:01Z","payload":{"type":"message","role":"user","content":"plain string prompt"}}
{"type":"response_item","timestamp":"2026-04-01T00:00:05Z","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Reviewed. Looks good."}]}}
{"type":"event_msg","timestamp":"2026-04-01T00:00:06Z","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":10,"cached_input_tokens":0,"output_tokens":5}}}}
`

// A rollout with no user prompt but real token usage: one fallback turn.
const codexFallback = `{"type":"turn_context","payload":{"cwd":"/codex/fb","model":"gpt-5.2"}}
{"type":"event_msg","timestamp":"2026-05-01T00:00:00Z","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":7,"cached_input_tokens":2,"output_tokens":3}}}}
`

// A rollout with no prompt TEXT (image-only user response_item) and no
// tokens: zero turns; ResponseCapture falls back to last-user segmentation.
const codexSeg = `{"type":"session_meta","payload":{"cwd":"/codex/seg"}}
{"type":"turn_context","payload":{"model":"gpt-old"}}
{"type":"event_msg","timestamp":"2026-06-01T00:00:00Z","payload":{"type":"agent_message","message":"Earlier turn message."}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_image","image_url":"x"}]}}
{"type":"turn_context","payload":{"model":"gpt-new"}}
{"type":"event_msg","timestamp":"2026-06-01T00:01:00Z","payload":{"type":"agent_message","message":"Described the image."}}
`

func TestCodexTurns(t *testing.T) {
	t.Parallel()

	// Expected values produced by the legacy hooks/devbrain_lib.py
	// transcript_turns() over these exact fixtures.
	eventMsgWant := []string{
		`dt=2026-03-01T10:00:01.000Z|cwd=/codex/repo|prompt="fix the flaky test"|texts=["Fixed the flaky test by pinning the clock."]|tools=Bash×2,browser_navigate×1,MCP×1,apply_patch×1|files=flaky_test.go,other.go|turn_ts=2026-03-01T10:00:21.000Z|tok=600/50/0/400|model=gpt-5.2-codex-mini`,
		`dt=2026-03-01T10:05:00.000Z|cwd=/codex/repo|prompt="$ship it"|texts=["Shipping now.", " PR opened.", "Shipped: PR #42."]|tools=Skill:ship×1|files=|turn_ts=2026-03-01T10:25:57Z|tok=1000/300/0/4200|model=gpt-5.2-codex`,
	}

	t.Run("event-msg-boundaries", func(t *testing.T) {
		t.Parallel()
		path := writeFixture(t, "codex-eventmsg.jsonl", codexEventMsg)
		checkTurns(t, Turns(path, 0, true), eventMsgWant)
		// No synthetic event_msg prompts here, so filtering changes nothing.
		checkTurns(t, Turns(path, 0, false), eventMsgWant)
	})

	t.Run("response-item-boundaries", func(t *testing.T) {
		t.Parallel()
		path := writeFixture(t, "codex-respitem.jsonl", codexRespItem)
		checkTurns(t, Turns(path, 0, true), []string{
			`dt=2026-04-01T00:00:01Z|cwd=/codex/two|prompt="plain string prompt"|texts=["Reviewed. Looks good."]|tools=|files=|turn_ts=2026-04-01T00:00:06Z|tok=10/5/0/0|model=gpt-5.3`,
		})
		checkTurns(t, Turns(path, 0, false), []string{
			`dt=2026-04-01T00:00:00Z|cwd=/codex/two|prompt="<skill>\n<name>review</name>\n</skill>"|texts=[]|tools=|files=|turn_ts=|tok=0/0/0/0|model=gpt-5.3`,
			`dt=2026-04-01T00:00:01Z|cwd=/codex/two|prompt="plain string prompt"|texts=["Reviewed. Looks good."]|tools=|files=|turn_ts=2026-04-01T00:00:06Z|tok=10/5/0/0|model=gpt-5.3`,
		})
	})

	t.Run("token-only-fallback-turn", func(t *testing.T) {
		t.Parallel()
		path := writeFixture(t, "codex-fallback.jsonl", codexFallback)
		want := []string{
			`dt=2026-05-01T00:00:00Z|cwd=/codex/fb|prompt=""|texts=[]|tools=|files=|turn_ts=2026-05-01T00:00:00Z|tok=5/3/0/2|model=gpt-5.2`,
		}
		checkTurns(t, Turns(path, 0, true), want)
		checkTurns(t, Turns(path, 0, false), want)
	})

	t.Run("no-prompt-no-tokens", func(t *testing.T) {
		t.Parallel()
		path := writeFixture(t, "codex-seg.jsonl", codexSeg)
		if got := Turns(path, 0, false); len(got) != 0 {
			t.Errorf("got %d turns, want 0", len(got))
		}
	})
}

func TestCodexSkillName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, line, want string
	}{
		{"skill-marker", `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<skill>\n<name>ship</name>\n</skill>"}]}}`, "ship"},
		{"name-whitespace-stripped", `{"type":"response_item","payload":{"type":"message","role":"user","content":"<skill> <name> review </name></skill>"}}`, "review"},
		{"leading-whitespace-ok", `{"type":"response_item","payload":{"type":"message","role":"user","content":"  <skill>\n<name>x</name>"}}`, "x"},
		{"prose-mention-not-counted", `{"type":"response_item","payload":{"type":"message","role":"user","content":"docs mention <skill><name>x</name>"}}`, ""},
		{"no-name-tag", `{"type":"response_item","payload":{"type":"message","role":"user","content":"<skill>nameless</skill>"}}`, ""},
		{"wrong-event-type", `{"type":"event_msg","payload":{"type":"user_message","message":"<skill><name>x</name>"}}`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			e, ok := parseEvent(c.line)
			if !ok {
				t.Fatal("fixture line did not parse")
			}
			if got := codexSkillName(e); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestCodexSessionID(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	t.Run("session-meta-id-wins", func(t *testing.T) {
		t.Parallel()
		p := write("rollout-2026-03-01T10-00-00-aaaa-bbbb-cccc-dddd-eeee.jsonl", codexEventMsg)
		if got := CodexSessionID(p); got != "0196a-1111-2222-3333-444444444444" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("filename-fallback-missing-file", func(t *testing.T) {
		t.Parallel()
		p := filepath.Join(dir, "rollout-2026-03-01T10-00-00-0196aaaa-bbbb-cccc-dddd-eeeeffff0000.jsonl")
		if got := CodexSessionID(p); got != "0196aaaa-bbbb-cccc-dddd-eeeeffff0000" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("no-session-meta-in-file", func(t *testing.T) {
		t.Parallel()
		p := write("codex-fallback.jsonl", codexFallback)
		if got := CodexSessionID(p); got != "codex-fallback" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("short-stem", func(t *testing.T) {
		t.Parallel()
		if got := CodexSessionID(filepath.Join(dir, "x.jsonl")); got != "x" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("stem-shorter-than-suffix", func(t *testing.T) {
		t.Parallel()
		if got := CodexSessionID(filepath.Join(dir, "x.json")); got != "nosession" {
			t.Errorf("got %q", got)
		}
	})
}
