package transcript

// Codex rollout parsing — the _codex_* half of the legacy devbrain_lib.py.
// A rollout is a JSONL of {type, timestamp, payload} events; user prompts
// arrive as event_msg user_message (preferred) or response_item role=user.

import (
	"bufio"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/TheWeiHu/devbrain/internal/redact"
)

func isCodexEvent(e map[string]any) bool {
	switch getStr(e, "type") {
	case "session_meta", "event_msg", "response_item", "turn_context":
		return true
	}
	return false
}

// isCodexUserPrompt is _is_codex_user_prompt.
func isCodexUserPrompt(e map[string]any) bool {
	p := getMap(e, "payload")
	switch getStr(e, "type") {
	case "event_msg":
		return getStr(p, "type") == "user_message" && pyStrip(getStr(p, "message")) != ""
	case "response_item":
		return getStr(p, "type") == "message" && getStr(p, "role") == "user"
	}
	return false
}

// codexPromptText is _codex_prompt_text: the stripped user prompt carried by
// an event, or "".
func codexPromptText(e map[string]any) string {
	p := getMap(e, "payload")
	switch getStr(e, "type") {
	case "event_msg":
		if getStr(p, "type") == "user_message" {
			return pyStrip(getStr(p, "message"))
		}
	case "response_item":
		if getStr(p, "type") != "message" || getStr(p, "role") != "user" {
			return ""
		}
		switch c := p["content"].(type) {
		case string:
			return pyStrip(c)
		case []any:
			var b strings.Builder
			for _, x := range c {
				bm, ok := x.(map[string]any)
				if !ok {
					continue
				}
				switch getStr(bm, "type") {
				case "input_text", "text", "output_text":
					b.WriteString(getStr(bm, "text"))
				}
			}
			return pyStrip(b.String())
		}
	}
	return ""
}

// A Codex skill run injects the SKILL.md as a role=user response_item opening
// with `<skill>\n<name>NAME</name>…` — Codex's equivalent of Claude's Skill
// tool_use, credited as Skill:NAME. Anchored on a leading `<skill>` so prose
// that merely mentions the tag is never miscounted.
var codexSkillRe = regexp.MustCompile(`<skill>\s*<name>([^<]+)</name>`)

// codexSkillName returns the skill NAME if e is a Codex `<skill>` marker, else "".
func codexSkillName(e map[string]any) string {
	if getStr(e, "type") != "response_item" {
		return ""
	}
	text := codexPromptText(e)
	if !strings.HasPrefix(pyLStrip(text), "<skill>") {
		return ""
	}
	m := codexSkillRe.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	return pyStrip(m[1])
}

// CodexSessionID is codex_session_id: session_meta.payload.id, falling back
// to the trailing UUID in the filename (last 5 dash-parts of the stem).
func CodexSessionID(path string) string {
	stem := []rune(filepath.Base(path))
	if len(stem) > 6 {
		stem = stem[:len(stem)-6] // strip ".jsonl"
	} else {
		stem = nil
	}
	parts := strings.Split(string(stem), "-")
	if len(parts) > 5 {
		parts = parts[len(parts)-5:]
	}
	sid := strings.Join(parts, "-")
	if sid == "" {
		sid = "nosession"
	}
	f, err := os.Open(path)
	if err != nil {
		return sid
	}
	defer f.Close()
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadString('\n')
		if line != "" {
			if e, ok := parseEvent(pyStrip(line)); ok && getStr(e, "type") == "session_meta" {
				if id := getStr(getMap(e, "payload"), "id"); id != "" {
					return id
				}
				return sid
			}
		}
		if err != nil {
			return sid
		}
	}
}

// codexCwd is _codex_cwd.
func codexCwd(e map[string]any) string {
	switch getStr(e, "type") {
	case "session_meta", "turn_context":
		return getStr(getMap(e, "payload"), "cwd")
	}
	return ""
}

// codexModelFromTurnContext is _codex_model_from_turn_context.
func codexModelFromTurnContext(e map[string]any) string {
	if getStr(e, "type") != "turn_context" {
		return ""
	}
	p := getMap(e, "payload")
	if m := getStr(p, "model"); m != "" {
		return m
	}
	return getStr(getMap(getMap(p, "collaboration_mode"), "settings"), "model")
}

// codexDetails is _codex_details: texts/tools/files/tokens/model for one
// turn's events; prior events contribute only their turn_context model.
func codexDetails(events, prior []map[string]any) Turn {
	t := Turn{Tools: &Counter{}, Files: &Set{}}
	var tin, tout, tcr float64

	for _, e := range prior {
		if m := codexModelFromTurnContext(e); m != "" {
			t.Model = m
		}
	}

	for _, e := range events {
		if m := codexModelFromTurnContext(e); m != "" {
			t.Model = m
		}
		p := getMap(e, "payload")
		switch getStr(e, "type") {
		case "event_msg":
			switch getStr(p, "type") {
			case "agent_message":
				if msg := getStr(p, "message"); msg != "" {
					t.Texts = append(t.Texts, msg)
				}
				if ts := getStr(e, "timestamp"); ts != "" {
					t.TurnTS = ts
				}
			case "exec_command_begin":
				t.Tools.Inc("Bash", 1)
			case "mcp_tool_call_begin":
				name := getStr(p, "tool_name")
				if name == "" {
					name = "MCP"
				}
				t.Tools.Inc(name, 1)
			case "patch_apply_begin":
				t.Tools.Inc("apply_patch", 1)
			case "token_count":
				info := getMap(p, "info")
				if usage := getMap(info, "last_token_usage"); len(usage) > 0 {
					// additive per-turn usage; cached input reported separately
					cached := num(usage["cached_input_tokens"])
					tin += math.Max(num(usage["input_tokens"])-cached, 0)
					tout += num(usage["output_tokens"])
					tcr += cached
				} else {
					// running totals -> max semantics
					usage := getMap(info, "total_token_usage")
					cached := num(usage["cached_input_tokens"])
					tin = math.Max(tin, math.Max(num(usage["input_tokens"])-cached, 0))
					tout = math.Max(tout, num(usage["output_tokens"]))
					tcr = math.Max(tcr, cached)
				}
				if m := getStr(p, "model"); m != "" {
					t.Model = m
				}
				if ts := getStr(e, "timestamp"); ts != "" {
					t.TurnTS = ts
				}
			case "task_complete":
				if msg := getStr(p, "last_agent_message"); msg != "" {
					t.Texts = append(t.Texts, msg)
				}
				if f, ok := numOK(p["completed_at"]); ok && f != 0 {
					sec := math.Floor(f)
					ts := time.Unix(int64(sec), int64((f-sec)*1e9)).UTC()
					if y := ts.Year(); y >= 1 && y <= 9999 { // Python fromtimestamp range
						t.TurnTS = ts.Format("2006-01-02T15:04:05Z")
					}
				}
			}
		case "response_item":
			switch {
			case getStr(p, "type") == "message" && getStr(p, "role") == "assistant":
				for _, b := range asList(p["content"]) {
					bm, ok := b.(map[string]any)
					if !ok {
						continue
					}
					switch getStr(bm, "type") {
					case "output_text", "text":
						t.Texts = append(t.Texts, getStr(bm, "text"))
					}
				}
				if ts := getStr(e, "timestamp"); ts != "" {
					t.TurnTS = ts
				}
			case getStr(p, "type") == "file_change":
				path := getStr(p, "path")
				if path == "" {
					path = getStr(p, "file_path")
				}
				if path != "" {
					t.Files.Add(basename(path))
				}
			default:
				if skill := codexSkillName(e); skill != "" { // the injected `<skill><name>…` marker
					t.Tools.Inc("Skill:"+skill, 1)
				}
			}
		}
	}
	t.Input, t.Output, t.CacheCreate, t.CacheRead = int(tin), int(tout), 0, int(tcr)
	return t
}

// codexTurns is _codex_transcript_turns: segment a rollout into prompt-led
// turns. When the rollout carries event_msg user_messages those are the only
// boundaries (response_item user messages are mirrors); otherwise
// response_item role=user events bound turns. Synthetic prompts never start a
// turn but stay visible to later turns as prior events.
func codexTurns(events []map[string]any, filterSynthetic bool) []Turn {
	var turns []Turn
	var cur *Turn
	var curEvents, curPrior, prior []map[string]any
	cwd := ""
	preferEventMsgUser := false
	for _, e := range events {
		if getStr(e, "type") == "event_msg" && codexPromptText(e) != "" {
			preferEventMsgUser = true
			break
		}
	}

	finish := func() {
		if cur == nil {
			return
		}
		d := codexDetails(curEvents, curPrior)
		d.DT, d.CWD, d.Prompt = cur.DT, cur.CWD, cur.Prompt
		turns = append(turns, d)
		cur, curEvents, curPrior = nil, nil, nil
	}

	for _, e := range events {
		if c := codexCwd(e); c != "" {
			cwd = c
		}
		prompt := codexPromptText(e)
		isBoundary := prompt != "" && (getStr(e, "type") == "event_msg" || !preferEventMsgUser)
		if isBoundary {
			if filterSynthetic && redact.IsSynthetic(prompt) {
				prior = append(prior, e)
				continue
			}
			finish()
			cur = &Turn{DT: getStr(e, "timestamp"), CWD: cwd, Prompt: prompt}
			curEvents = nil
			curPrior = append([]map[string]any(nil), prior...)
			prior = append(prior, e)
			continue
		}
		if cur != nil && isCodexEvent(e) {
			curEvents = append(curEvents, e)
		}
		prior = append(prior, e)
	}
	finish()

	if len(turns) == 0 {
		d := codexDetails(events, nil)
		if d.Input != 0 || d.Output != 0 || d.CacheCreate != 0 || d.CacheRead != 0 {
			for _, e := range events {
				if ts := getStr(e, "timestamp"); ts != "" {
					d.DT = ts
					break
				}
			}
			d.CWD = cwd
			turns = append(turns, d)
		}
	}
	return turns
}
