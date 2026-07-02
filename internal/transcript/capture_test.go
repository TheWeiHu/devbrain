package transcript

import (
	"os"
	"path/filepath"
	"testing"
)

const rcClaude = `{"type":"user","timestamp":"2026-01-02T03:04:05Z","cwd":"/repo","message":{"content":"do it"}}
{"type":"assistant","timestamp":"2026-01-02T03:04:08.500Z","message":{"id":"m1","model":"claude-opus-4-5","usage":{"input_tokens":11,"output_tokens":22,"cache_creation_input_tokens":33,"cache_read_input_tokens":44},"content":[{"type":"text","text":"All done. Fixed the bug in parser.go and added a test."},{"type":"tool_use","name":"Edit","input":{"file_path":"/repo/parser.go"}},{"type":"tool_use","name":"Bash","input":{"command":"go test"}},{"type":"tool_use","name":"Bash","input":{"command":"go vet"}}]}}
`

// A turn whose assistant produced no text blocks — the fallbackText path.
const rcQuiet = `{"type":"user","timestamp":"2026-01-02T03:04:05Z","cwd":"/r","message":{"content":"quiet task"}}
{"type":"assistant","timestamp":"2026-01-02T03:05:00Z","message":{"id":"m1","model":"claude-haiku-4-5","usage":{"input_tokens":1,"output_tokens":2},"content":[{"type":"tool_use","name":"Bash","input":{"command":"ls"}}]}}
`

// All expected strings below are the byte-exact output of the legacy
// hooks/devbrain_lib.py response_capture() over the same fixtures.
func TestResponseCapture(t *testing.T) {
	t.Parallel()

	t.Run("claude-with-sidecar", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := writeFixture(t, "rc-claude.jsonl", rcClaude)
		sidecar := filepath.Join(dir, "nested", "usage.jsonl") // exercises makedirs
		got := ResponseCapture(path, sidecar, "sess-1", "2026-01-01T00:00:00Z", true, "")
		want := "All done. Fixed the bug in parser.go and added a test.\n" +
			"touched: parser.go  ·  tools: Edit×1, Bash×2  ·  tokens: 11/22/33/44 · model: claude-opus-4-5\n" +
			"All done. Fixed the bug in parser.go and added a test."
		if got != want {
			t.Errorf("capture:\n got: %q\nwant: %q", got, want)
		}
		wantLine := `{"ts": "2026-01-02T03:04:08Z", "session": "sess-1", "model": "claude-opus-4-5", "in": 11, "out": 22, "cache_create": 33, "cache_read": 44, "auto": true}` + "\n"
		if b, err := os.ReadFile(sidecar); err != nil || string(b) != wantLine {
			t.Errorf("sidecar (err=%v):\n got: %q\nwant: %q", err, b, wantLine)
		}
	})

	t.Run("fallback-text-and-redaction", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := writeFixture(t, "rc-quiet.jsonl", rcQuiet)
		sidecar := filepath.Join(dir, "u2.jsonl")
		got := ResponseCapture(path, sidecar, "s2", "2026-09-09T09:09:09Z", false,
			"Ran ls and found the file. Bearer abcdefghijklmnop1234 secret.")
		want := "Ran ls and found the file. Bearer [REDACTED] secret.\n" +
			"tools: Bash×1  ·  tokens: 1/2/0/0 · model: claude-haiku-4-5\n" +
			"Ran ls and found the file. Bearer [REDACTED] secret."
		if got != want {
			t.Errorf("capture:\n got: %q\nwant: %q", got, want)
		}
		wantLine := `{"ts": "2026-01-02T03:05:00Z", "session": "s2", "model": "claude-haiku-4-5", "in": 1, "out": 2, "cache_create": 0, "cache_read": 0, "auto": false}` + "\n"
		if b, err := os.ReadFile(sidecar); err != nil || string(b) != wantLine {
			t.Errorf("sidecar (err=%v):\n got: %q\nwant: %q", err, b, wantLine)
		}
	})

	t.Run("codex-last-turn", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := writeFixture(t, "codex-eventmsg.jsonl", codexEventMsg)
		sidecar := filepath.Join(dir, "u3.jsonl")
		got := ResponseCapture(path, sidecar, "cx", "2026-01-01T00:00:00Z", false, "")
		want := "Shipped: PR #42.\n" +
			"tools: Skill:ship×1  ·  tokens: 1000/300/0/4200 · model: gpt-5.2-codex\n" +
			"Shipping now.\n\nPR opened.\n\nShipped: PR #42."
		if got != want {
			t.Errorf("capture:\n got: %q\nwant: %q", got, want)
		}
		wantLine := `{"ts": "2026-03-01T10:25:57Z", "session": "cx", "model": "gpt-5.2-codex", "in": 1000, "out": 300, "cache_create": 0, "cache_read": 4200, "auto": false}` + "\n"
		if b, err := os.ReadFile(sidecar); err != nil || string(b) != wantLine {
			t.Errorf("sidecar (err=%v):\n got: %q\nwant: %q", err, b, wantLine)
		}
	})

	t.Run("codex-no-turns-last-user-segmentation", func(t *testing.T) {
		t.Parallel()
		path := writeFixture(t, "codex-seg.jsonl", codexSeg)
		got := ResponseCapture(path, "", "", "", false, "")
		// Only events after the image-only user message count: one text, no
		// meta (no tools/tokens), so the middle line is empty.
		want := "Described the image.\n\nDescribed the image."
		if got != want {
			t.Errorf("capture:\n got: %q\nwant: %q", got, want)
		}
	})

	t.Run("claude-last-turn-is-synthetic", func(t *testing.T) {
		t.Parallel()
		// response_capture keeps synthetic prompts (filter_synthetic=False),
		// so the trailing <system-reminder> turn is the one captured.
		path := writeFixture(t, "claude-multi.jsonl", claudeMulti)
		got := ResponseCapture(path, "", "", "", false, "")
		want := "Second answer!\ntouched: x.py  ·  tools: Edit×2\nSecond answer!"
		if got != want {
			t.Errorf("capture:\n got: %q\nwant: %q", got, want)
		}
	})

	t.Run("no-tokens-no-sidecar-write", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := writeFixture(t, "codex-seg.jsonl", codexSeg)
		sidecar := filepath.Join(dir, "u4.jsonl")
		ResponseCapture(path, sidecar, "s", "ts", false, "")
		if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
			t.Errorf("sidecar written despite zero tokens (err=%v)", err)
		}
	})

	t.Run("empty-and-missing-transcripts", func(t *testing.T) {
		t.Parallel()
		if got := ResponseCapture(writeFixture(t, "empty.jsonl", ""), "", "", "", false, ""); got != "" {
			t.Errorf("empty transcript: got %q, want \"\"", got)
		}
		if got := ResponseCapture(filepath.Join(t.TempDir(), "nope.jsonl"), "", "", "", false, ""); got != "" {
			t.Errorf("missing transcript: got %q, want \"\"", got)
		}
	})

	t.Run("sidecar-without-directory-skipped", func(t *testing.T) {
		t.Parallel()
		// Python's makedirs(dirname("bare.jsonl")) raises and the write is
		// swallowed; the port skips the same way. Capture output unaffected.
		path := writeFixture(t, "rc-claude.jsonl", rcClaude)
		got := ResponseCapture(path, "bare-sidecar-should-not-exist.jsonl", "s", "t", false, "")
		if got == "" {
			t.Error("capture output should be unaffected by sidecar failure")
		}
		if _, err := os.Stat("bare-sidecar-should-not-exist.jsonl"); !os.IsNotExist(err) {
			t.Errorf("bare sidecar path was written (err=%v)", err)
		}
	})
}

// Expected values from the legacy _meta_line over equivalent turn dicts.
func TestMetaLine(t *testing.T) {
	t.Parallel()
	full := Turn{
		Tools: &Counter{}, Files: &Set{},
		Input: 1, Output: 2, CacheCreate: 3, CacheRead: 4, Model: "m1",
	}
	full.Files.Add("a.go")
	full.Files.Add("b.md")
	full.Tools.Inc("Edit", 2)
	full.Tools.Inc("Bash", 1)

	cases := []struct {
		name          string
		turn          Turn
		includeTokens bool
		want          string
	}{
		{"all-parts", full, true, "touched: a.go, b.md  ·  tools: Edit×2, Bash×1  ·  tokens: 1/2/3/4 · model: m1"},
		{"tokens-excluded", full, false, "touched: a.go, b.md  ·  tools: Edit×2, Bash×1"},
		{"tokens-only-no-model", Turn{Output: 5}, true, "tokens: 0/5/0/0"},
		{"model-without-tokens-omitted", Turn{Model: "m"}, true, ""},
		{"empty", Turn{Tools: &Counter{}, Files: &Set{}}, true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := MetaLine(c.turn, c.includeTokens); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// Expected values from the legacy _iso_seconds (fromisoformat + strftime):
// note the wall-clock fields are kept even for non-UTC offsets.
func TestIsoSeconds(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"2026-01-02T03:04:05Z", "2026-01-02T03:04:05Z"},
		{"2026-01-02T03:04:05.123Z", "2026-01-02T03:04:05Z"},
		{"2026-01-02T03:04:05+00:00", "2026-01-02T03:04:05Z"},
		{"2026-01-02T03:04:05.999+05:30", "2026-01-02T03:04:05Z"},
		{"2026-01-02 03:04:05", "2026-01-02T03:04:05Z"},
		{"2026-01-02", "2026-01-02T00:00:00Z"},
		{"garbage", "FB"},
		{"", "FB"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			t.Parallel()
			if got := isoSeconds(c.in, "FB"); got != c.want {
				t.Errorf("isoSeconds(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
