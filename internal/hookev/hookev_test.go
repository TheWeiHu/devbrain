package hookev

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func readJSONL(t *testing.T, rel string) []map[string]any {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", rel))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []map[string]any
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// Every corpus case's ReadEvent output must equal the golden byte-for-byte
// (golden produced by the legacy devbrain_lib.py read_event).
func TestReadEventGolden(t *testing.T) {
	t.Parallel()
	cases := readJSONL(t, "testdata/corpus/read-event-cases.jsonl")
	golds := readJSONL(t, "testdata/golden/read-event.jsonl")
	if len(cases) != len(golds) {
		t.Fatalf("corpus/golden mismatch: %d vs %d", len(cases), len(golds))
	}
	for i, c := range cases {
		c, g := c, golds[i]
		name := c["name"].(string)
		if g["name"].(string) != name {
			t.Fatalf("case %d: corpus %q vs golden %q", i, name, g["name"])
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := ReadEvent(c["payload"].(string), c["field"].(string), c["harness"].(string))
			if want := g["out"].(string); got != want {
				t.Errorf("got %q want %q", got, want)
			}
		})
	}
}

// Only the Claude field mapping exists (Codex capture is sweep-based); the
// harness argument is ignored.
func TestReadEventClaudeOnly(t *testing.T) {
	payload := `{"thread_id": "th-1", "session_id": "s-1"}`
	codexOnly := `{"thread_id": "th-1"}`

	if got := ReadEvent(payload, "session", ""); got != "s-1" {
		t.Errorf("default claude: got %q want s-1", got)
	}
	if got := ReadEvent(codexOnly, "session", ""); got != "" {
		t.Errorf("claude mapping ignores thread_id: got %q", got)
	}
	if got := ReadEvent(codexOnly, "session", "codex"); got != "" {
		t.Errorf("legacy codex harness arg must be inert: got %q", got)
	}
}

// Expected literals verified against the Python session_start_context
// (json.dumps ensure_ascii=False) while porting.
func TestSessionStartContext(t *testing.T) {
	t.Parallel()
	cases := []struct{ msg, want string }{
		{"plain",
			`{"hookSpecificOutput": {"hookEventName": "SessionStart", "additionalContext": "plain"}}`},
		{"",
			`{"hookSpecificOutput": {"hookEventName": "SessionStart", "additionalContext": ""}}`},
		{"has \"quotes\" and `backticks`",
			`{"hookSpecificOutput": {"hookEventName": "SessionStart", "additionalContext": "has \"quotes\" and ` + "`backticks`" + `"}}`},
		{"line1\nline2\ttab",
			`{"hookSpecificOutput": {"hookEventName": "SessionStart", "additionalContext": "line1\nline2\ttab"}}`},
		{"unicode: café — 東京 🚀",
			`{"hookSpecificOutput": {"hookEventName": "SessionStart", "additionalContext": "unicode: café — 東京 🚀"}}`},
		{"back\\slash and \x01 control",
			`{"hookSpecificOutput": {"hookEventName": "SessionStart", "additionalContext": "back\\slash and \u0001 control"}}`},
	}
	for _, c := range cases {
		if got := SessionStartContext(c.msg); got != c.want {
			t.Errorf("SessionStartContext(%q)\n got %s\nwant %s", c.msg, got, c.want)
		}
	}
}
