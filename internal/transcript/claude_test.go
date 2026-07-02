package transcript

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// turnRepr renders a Turn in the same shape the legacy-Python cross-check
// driver printed its turn dicts, so expected values below are copy-pasted
// Python output (prompt/texts via json.dumps → pyJSONString/pyJSONList).
func turnRepr(tn Turn) string {
	tools := make([]string, 0, tn.Tools.Len())
	for _, k := range tn.Tools.Keys() {
		tools = append(tools, k+"×"+strconv.Itoa(tn.Tools.Get(k)))
	}
	return strings.Join([]string{
		"dt=" + tn.DT,
		"cwd=" + tn.CWD,
		"prompt=" + pyJSONString(tn.Prompt),
		"texts=" + pyJSONList(tn.Texts),
		"tools=" + strings.Join(tools, ","),
		"files=" + strings.Join(tn.Files.Keys(), ","),
		"turn_ts=" + tn.TurnTS,
		fmt.Sprintf("tok=%d/%d/%d/%d", tn.Input, tn.Output, tn.CacheCreate, tn.CacheRead),
		"model=" + tn.Model,
	}, "|")
}

func pyJSONList(xs []string) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = pyJSONString(x)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func writeFixture(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func checkTurns(t *testing.T, turns []Turn, want []string) {
	t.Helper()
	if len(turns) != len(want) {
		t.Fatalf("got %d turns, want %d", len(turns), len(want))
	}
	for i, tn := range turns {
		if got := turnRepr(tn); got != want[i] {
			t.Errorf("turn %d:\n got: %s\nwant: %s", i, got, want[i])
		}
	}
}

// A Claude transcript covering: multi-turn segmentation, sidechain skip,
// token dedup by message id (msg_1 counted once), None-id dedup (the two
// id-less assistants share one usage slot), Skill naming via input.skill and
// input.name, file_path/path basenames with dedup, string vs list content,
// string assistant content (ignored), invalid JSON lines, whitespace prompts.
const claudeMulti = `{"type":"user","timestamp":"2026-01-02T03:04:05.123Z","cwd":"/repo","message":{"content":"first prompt"}}
{"type":"assistant","timestamp":"2026-01-02T03:04:06.000Z","message":{"id":"msg_1","model":"claude-opus-4-5","usage":{"input_tokens":10,"output_tokens":20,"cache_creation_input_tokens":5,"cache_read_input_tokens":7},"content":[{"type":"text","text":"Working on it."},{"type":"tool_use","name":"Edit","input":{"file_path":"/repo/a/b.go"}}]}}
{"type":"assistant","timestamp":"2026-01-02T03:04:07.000Z","message":{"id":"msg_1","usage":{"input_tokens":99,"output_tokens":99},"content":[{"type":"tool_use","name":"Read","input":{"path":"/repo/c.md"}}]}}
{"type":"user","isSidechain":true,"message":{"content":[{"type":"text","text":"sidechain prompt"}]}}
{"type":"assistant","message":{"id":"msg_2","model":"claude-opus-4-5","usage":{"input_tokens":3,"output_tokens":4},"content":[{"type":"tool_use","name":"Skill","input":{"skill":"ship"}},{"type":"tool_use","name":"Skill","input":{"name":"review"}},{"type":"text","text":"Done. Shipped the fix and opened a PR."}]}}
{"type":"user","timestamp":"2026-01-02T04:00:00Z","cwd":"/repo2","message":{"content":[{"type":"text","text":"second "},{"type":"text","text":"prompt"}]}}
{"type":"user","message":{"content":"<system-reminder>injected noise</system-reminder>"}}
{"type":"assistant","message":{"content":[{"type":"text","text":"Second answer!"}]}}
{"type":"assistant","message":{"usage":{"input_tokens":50,"output_tokens":60},"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/repo2/x.py"}},{"type":"tool_use","name":"Edit","input":{"file_path":"/deep/x.py"}}]}}
{"type":"assistant","message":{"id":"msg_str","content":"raw string content ignored"}}
{"type":"summary","summary":"ignored"}
not json at all
{"type":"user","message":{"content":"   "}}
`

func TestClaudeTurns(t *testing.T) {
	t.Parallel()
	path := writeFixture(t, "claude-multi.jsonl", claudeMulti)

	// Expected values produced by the legacy hooks/devbrain_lib.py
	// transcript_turns() over this exact fixture.
	turn1 := `dt=2026-01-02T03:04:05.123Z|cwd=/repo|prompt="first prompt"|texts=["Working on it.", "Done. Shipped the fix and opened a PR."]|tools=Edit×1,Read×1,Skill:ship×1,Skill:review×1|files=b.go,c.md|turn_ts=2026-01-02T03:04:07.000Z|tok=13/24/5/7|model=claude-opus-4-5`

	t.Run("filter-synthetic", func(t *testing.T) {
		t.Parallel()
		checkTurns(t, Turns(path, 0, true), []string{
			turn1,
			// The synthetic prompt is dropped, so its assistants attach here.
			// Both are id-less: the first (usage-less) claims the None slot,
			// so the second's 50/60 usage is dedup-skipped — tok stays 0.
			`dt=2026-01-02T04:00:00Z|cwd=/repo2|prompt="second prompt"|texts=["Second answer!"]|tools=Edit×2|files=x.py|turn_ts=|tok=0/0/0/0|model=`,
		})
	})

	t.Run("keep-synthetic", func(t *testing.T) {
		t.Parallel()
		checkTurns(t, Turns(path, 0, false), []string{
			turn1,
			`dt=2026-01-02T04:00:00Z|cwd=/repo2|prompt="second prompt"|texts=[]|tools=|files=|turn_ts=|tok=0/0/0/0|model=`,
			`dt=|cwd=|prompt="<system-reminder>injected noise</system-reminder>"|texts=["Second answer!"]|tools=Edit×2|files=x.py|turn_ts=|tok=0/0/0/0|model=`,
		})
	})

	t.Run("tail-lines-window", func(t *testing.T) {
		t.Parallel()
		// Last 8 physical lines start at the "second prompt" user event.
		checkTurns(t, Turns(path, 8, true), []string{
			`dt=2026-01-02T04:00:00Z|cwd=/repo2|prompt="second prompt"|texts=["Second answer!"]|tools=Edit×2|files=x.py|turn_ts=|tok=0/0/0/0|model=`,
		})
		// A window past all user prompts has no boundary at all.
		if got := Turns(path, 5, true); len(got) != 0 {
			t.Errorf("tail=5: got %d turns, want 0", len(got))
		}
	})

	t.Run("missing-file-fails-open", func(t *testing.T) {
		t.Parallel()
		if got := Turns(filepath.Join(t.TempDir(), "nope.jsonl"), 0, true); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
}

// Python sums float JSON token counts as floats ("1200.0" in its meta line);
// the port casts to int — the one sanctioned divergence from the legacy lib.
func TestClaudeFloatTokens(t *testing.T) {
	t.Parallel()
	path := writeFixture(t, "float.jsonl",
		`{"type":"user","message":{"content":"p"}}
{"type":"assistant","message":{"id":"a","usage":{"input_tokens":1200.0,"output_tokens":7},"content":[{"type":"text","text":"ok."}]}}
`)
	turns := Turns(path, 0, true)
	if len(turns) != 1 {
		t.Fatalf("got %d turns, want 1", len(turns))
	}
	if turns[0].Input != 1200 || turns[0].Output != 7 {
		t.Errorf("tokens = %d/%d, want 1200/7", turns[0].Input, turns[0].Output)
	}
}

func TestCounterAndSet(t *testing.T) {
	t.Parallel()
	var c Counter
	c.Inc("b", 1)
	c.Inc("a", 2)
	c.Inc("b", 3)
	if got := c.Keys(); !equalStrings(got, []string{"b", "a"}) {
		t.Errorf("Keys() = %v, want [b a] (insertion order)", got)
	}
	if c.Get("b") != 4 || c.Get("a") != 2 || c.Get("zz") != 0 || c.Len() != 2 {
		t.Errorf("counts wrong: b=%d a=%d zz=%d len=%d", c.Get("b"), c.Get("a"), c.Get("zz"), c.Len())
	}
	var s Set
	s.Add("y")
	s.Add("x")
	s.Add("y")
	if got := s.Keys(); !equalStrings(got, []string{"y", "x"}) {
		t.Errorf("Set.Keys() = %v, want [y x]", got)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
