// Package hookev reads normalized fields out of host-harness hook-event JSON
// payloads. It is the Go port of _EVENT_FIELDS / read_event /
// session_start_context in the legacy hooks/devbrain_lib.py; the contract is
// pinned by testdata/golden/read-event.jsonl.
package hookev

import (
	"os"
	"strings"

	"github.com/TheWeiHu/devbrain/internal/jsonedit"
)

// eventFields maps harness -> field -> ordered list of candidate paths.
// Mirrors _EVENT_FIELDS: claude fields have a single path; codex fields may
// list fallback paths tried until one yields a non-null value.
var eventFields = map[string]map[string][][]string{
	"claude": {
		"prompt":        {{"prompt"}},
		"cwd":           {{"cwd"}},
		"session":       {{"session_id"}},
		"transcript":    {{"transcript_path"}},
		"tool":          {{"tool_name"}},
		"command":       {{"tool_input", "command"}},
		"tool-response": {{"tool_response"}}, // value coerced to text (see coerceResponse)
		"stop-active":   {{"stop_hook_active"}},
	},
	"codex": {
		"prompt":                 {{"prompt"}},
		"cwd":                    {{"cwd"}},
		"session":                {{"session_id"}, {"thread_id"}, {"turn_id"}},
		"transcript":             {{"transcript_path"}, {"agent_transcript_path"}},
		"tool":                   {{"tool_name"}, {"tool", "name"}},
		"command":                {{"tool_input", "command"}, {"input", "command"}},
		"tool-response":          {{"tool_response"}, {"output"}},
		"stop-active":            {{"stop_hook_active"}},
		"last-assistant-message": {{"last_assistant_message"}},
	},
}

// ReadEvent returns one normalized field from a hook payload (JSON text), or
// "" when the field is absent (matching jq's `// empty`). harness "" defaults
// to $DEVBRAIN_HARNESS or "claude"; an unknown harness falls back to the
// claude mapping.
func ReadEvent(payload, field, harness string) string {
	if harness == "" {
		harness = os.Getenv("DEVBRAIN_HARNESS")
	}
	if harness == "" {
		harness = "claude"
	}
	mapping, ok := eventFields[harness]
	if !ok {
		mapping = eventFields["claude"]
	}
	paths, ok := mapping[field]
	if !ok {
		return ""
	}
	obj, err := jsonedit.Parse([]byte(payload))
	if err != nil {
		return ""
	}
	var cur *jsonedit.Value
	for _, p := range paths {
		if cur = readPath(obj, p); cur != nil {
			break
		}
	}
	if cur == nil {
		return ""
	}
	if field == "tool-response" {
		return coerceResponse(cur)
	}
	switch cur.Kind {
	case jsonedit.Bool:
		if cur.Bool {
			return "true"
		}
		return "false"
	case jsonedit.String:
		return cur.Str
	case jsonedit.Number:
		// Python str(json.loads(n)): the decoded literal round-trips for the
		// common shapes ("42" -> "42", "42.5" -> "42.5"); we keep the source
		// literal, which only diverges for exotic spellings like "1e2".
		return cur.Num.String()
	default:
		// Python renders dict/list here via str() (single-quoted repr). The
		// golden has no such case; JSON text is the closest sane equivalent.
		return pyDumps(cur)
	}
}

// readPath walks nested object keys; a missing key, a non-object step, or a
// JSON null yields nil (Python _read_path returns None for all three).
func readPath(obj *jsonedit.Value, path []string) *jsonedit.Value {
	cur := obj
	for _, key := range path {
		if cur == nil || cur.Kind != jsonedit.Object {
			return nil
		}
		cur = cur.Get(key)
		if cur == nil || cur.Kind == jsonedit.Null {
			return nil
		}
	}
	return cur
}

// coerceResponse ports _coerce_response: tool_response shape varies by
// harness/version — an object with .stdout/.output, or a bare string.
func coerceResponse(v *jsonedit.Value) string {
	switch v.Kind {
	case jsonedit.Object:
		for _, k := range []string{"stdout", "output"} {
			if m := v.Get(k); m != nil && m.Kind == jsonedit.String {
				return m.Str
			}
		}
		return pyDumps(v)
	case jsonedit.Null:
		return "" // unreachable via ReadEvent (null filtered in readPath); kept for parity
	case jsonedit.String:
		return v.Str
	default:
		return pyDumps(v)
	}
}

// SessionStartContext wraps a nudge string in the JSON shape SessionStart
// hooks emit to inject context, byte-identical to Python
// json.dumps(..., ensure_ascii=False).
func SessionStartContext(msg string) string {
	var b strings.Builder
	b.WriteString(`{"hookSpecificOutput": {"hookEventName": "SessionStart", "additionalContext": `)
	writePyString(&b, msg)
	b.WriteString(`}}`)
	return b.String()
}

// pyDumps renders a value as Python json.dumps(v, ensure_ascii=False):
// default separators (", " / ": "), no HTML escaping, insertion-ordered keys.
// Numbers keep their source literal (see the Number note in ReadEvent).
func pyDumps(v *jsonedit.Value) string {
	var b strings.Builder
	encodePy(&b, v)
	return b.String()
}

func encodePy(b *strings.Builder, v *jsonedit.Value) {
	switch v.Kind {
	case jsonedit.Null:
		b.WriteString("null")
	case jsonedit.Bool:
		if v.Bool {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case jsonedit.Number:
		b.WriteString(v.Num.String())
	case jsonedit.String:
		writePyString(b, v.Str)
	case jsonedit.Array:
		b.WriteByte('[')
		for i, e := range v.Arr {
			if i > 0 {
				b.WriteString(", ")
			}
			encodePy(b, e)
		}
		b.WriteByte(']')
	case jsonedit.Object:
		b.WriteByte('{')
		for i, m := range v.Obj {
			if i > 0 {
				b.WriteString(", ")
			}
			writePyString(b, m.Key)
			b.WriteString(": ")
			encodePy(b, m.Val)
		}
		b.WriteByte('}')
	}
}

// writePyString escapes exactly like Python json with ensure_ascii=False:
// short escapes for \" \\ \n \r \t \b \f, \u00XX for other control chars,
// everything else (including non-ASCII) raw.
func writePyString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			if r < 0x20 {
				const hex = "0123456789abcdef"
				b.WriteString(`\u00`)
				b.WriteByte(hex[r>>4])
				b.WriteByte(hex[r&0xf])
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}
