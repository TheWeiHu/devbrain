package transcript

// Codex rollout parsing — the _codex_* half of the legacy devbrain_lib.py.
// A rollout is a JSONL of {type, timestamp, payload} events; user prompts
// arrive as event_msg user_message (preferred) or response_item role=user.

import (
	"bufio"
	"encoding/json"
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

// applyCodexRuntime captures the effective runtime settings Codex persists in
// turn_context and thread_settings_applied events. Older rollouts may omit the
// service tier; empty means unknown, never an inferred billing tier.
func applyCodexRuntime(t *Turn, e map[string]any) {
	p := getMap(e, "payload")
	switch getStr(e, "type") {
	case "turn_context":
		if m := codexModelFromTurnContext(e); m != "" {
			t.Model = m
		}
		if effort := getStr(p, "effort"); effort != "" {
			t.ReasoningEffort = effort
		} else if effort := getStr(getMap(getMap(p, "collaboration_mode"), "settings"), "reasoning_effort"); effort != "" {
			t.ReasoningEffort = effort
		}
		if tier := getStr(p, "service_tier"); tier != "" {
			t.ServiceTier = tier
		}
	case "event_msg":
		if getStr(p, "type") != "thread_settings_applied" {
			return
		}
		s := getMap(p, "thread_settings")
		if m := getStr(s, "model"); m != "" {
			t.Model = m
		}
		if effort := getStr(s, "reasoning_effort"); effort != "" {
			t.ReasoningEffort = effort
		}
		if tier := getStr(s, "service_tier"); tier != "" {
			t.ServiceTier = tier
		}
	case "session_meta":
		if parent := getStr(p, "parent_thread_id"); parent != "" {
			t.ParentSession = parent
			return
		}
		spawn := getMap(getMap(getMap(p, "source"), "subagent"), "thread_spawn")
		if parent := getStr(spawn, "parent_thread_id"); parent != "" {
			t.ParentSession = parent
		}
	}
}

func isCodexRuntimeContext(e map[string]any) bool {
	if getStr(e, "type") == "turn_context" {
		return true
	}
	p := getMap(e, "payload")
	return getStr(e, "type") == "event_msg" && getStr(p, "type") == "thread_settings_applied"
}

// applyNightshiftRuntime fills settings that an older Codex rollout omitted
// from its first turn. Nightshift writes this read-only run contract beside the
// worker log; transcript evidence always wins when both are present.
func applyNightshiftRuntime(t *Turn) {
	if t.CWD == "" {
		return
	}
	b, err := os.ReadFile(filepath.Join(t.CWD, ".nightshift", "runtime.json"))
	if err != nil {
		return
	}
	var runtime struct {
		Model           string `json:"model"`
		ReasoningEffort string `json:"reasoning_effort"`
		ServiceTier     string `json:"service_tier"`
	}
	if json.Unmarshal(b, &runtime) != nil {
		return
	}
	if t.Model == "" {
		t.Model = runtime.Model
	}
	if t.ReasoningEffort == "" {
		t.ReasoningEffort = runtime.ReasoningEffort
	}
	if t.ServiceTier == "" {
		t.ServiceTier = runtime.ServiceTier
	}
}

// execCmd renders an exec_command_begin payload's command: the ["sh","-lc",
// cmd] wrapper unwraps to cmd, other array shapes join, a plain string passes
// through.
func execCmd(p map[string]any) string {
	switch c := p["command"].(type) {
	case string:
		return c
	case []any:
		parts := make([]string, 0, len(c))
		for _, x := range c {
			if s, ok := x.(string); ok {
				parts = append(parts, s)
			}
		}
		if len(parts) == 3 && (parts[1] == "-lc" || parts[1] == "-c") {
			return parts[2]
		}
		return strings.Join(parts, " ")
	}
	return ""
}

// codexDetails is _codex_details: texts/tools/files/tokens/model for one
// turn's events; prior events contribute only their turn_context model.
func codexDetails(events, prior []map[string]any) Turn {
	t := Turn{Tools: &Counter{}, Files: &Set{}}
	var tin, tout, tcr float64
	execIdx := map[string]int{} // call_id -> index into t.Execs
	lastBegin := -1

	for _, e := range prior {
		applyCodexRuntime(&t, e)
	}

	for _, e := range events {
		applyCodexRuntime(&t, e)
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
				t.Execs = append(t.Execs, Exec{TS: getStr(e, "timestamp"), Cmd: execCmd(p)})
				if id := getStr(p, "call_id"); id != "" {
					execIdx[id] = len(t.Execs) - 1
				}
				lastBegin = len(t.Execs) - 1
			case "exec_command_end":
				i := -1 // begin/end pair by call_id; only an id-less end falls back
				if id := getStr(p, "call_id"); id != "" {
					if j, ok := execIdx[id]; ok {
						i = j
					}
				} else {
					i = lastBegin
				}
				if i >= 0 && i < len(t.Execs) {
					out := getStr(p, "aggregated_output")
					if out == "" {
						out = getStr(p, "stdout")
					}
					t.Execs[i].Out = out
				}
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
			case getStr(p, "type") == "function_call" && getStr(p, "name") == "spawn_agent":
				t.SubagentCount++
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
// turn. Each turn inherits the model from the latest turn_context seen before
// its prompt; turn_contexts arriving mid-turn announce the NEXT turn and never
// relabel the open one.
func codexTurns(events []map[string]any, filterSynthetic bool) []Turn {
	var turns []Turn
	var cur *Turn
	var curEvents, curPrior []map[string]any
	var latestTurnContext, latestThreadSettings map[string]any
	parentSession := ""
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
		d.ParentSession = parentSession
		applyNightshiftRuntime(&d)
		turns = append(turns, d)
		cur, curEvents, curPrior = nil, nil, nil
	}

	for _, e := range events {
		if c := codexCwd(e); c != "" {
			cwd = c
		}
		if getStr(e, "type") == "session_meta" {
			var meta Turn
			applyCodexRuntime(&meta, e)
			if meta.ParentSession != "" {
				parentSession = meta.ParentSession
			}
		}
		if getStr(e, "type") == "turn_context" {
			latestTurnContext = e
		} else if isCodexRuntimeContext(e) {
			latestThreadSettings = e
		}
		prompt := codexPromptText(e)
		isBoundary := prompt != "" && (getStr(e, "type") == "event_msg" || !preferEventMsgUser)
		if isBoundary {
			if filterSynthetic && redact.IsSynthetic(prompt) {
				continue
			}
			finish()
			cur = &Turn{DT: getStr(e, "timestamp"), CWD: cwd, Prompt: prompt}
			curEvents = nil
			curPrior = nil
			if latestThreadSettings != nil {
				curPrior = append(curPrior, latestThreadSettings)
			}
			if latestTurnContext != nil {
				curPrior = append(curPrior, latestTurnContext)
			}
			continue
		}
		// Codex emits the NEXT turn's turn_context before its user_message.
		// It has already been captured in the latest runtime context for the next
		// turn's prior; keeping it out of curEvents stops it relabeling the
		// turn that is still open.
		if cur != nil && isCodexEvent(e) && !isCodexRuntimeContext(e) {
			curEvents = append(curEvents, e)
		}
	}
	finish()

	if len(turns) == 0 {
		d := codexDetails(events, nil)
		d.ParentSession = parentSession
		if d.Input != 0 || d.Output != 0 || d.CacheCreate != 0 || d.CacheRead != 0 {
			for _, e := range events {
				if ts := getStr(e, "timestamp"); ts != "" {
					d.DT = ts
					break
				}
			}
			d.CWD = cwd
			applyNightshiftRuntime(&d)
			turns = append(turns, d)
		}
	}
	return turns
}
