package jsonedit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fakeBin = "/opt/devbrain/bin/devbrain" // same fake install path as capture-goldens.sh

func corpusSettings(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "corpus", "settings", name))
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(b), "REG_CMD_PLACEHOLDER", fakeBin+" hook")
}

func golden(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "golden", "settings", name+".after.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func assertFile(t *testing.T, path, goldenName string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := golden(t, goldenName); string(got) != want {
		t.Errorf("%s mismatch:\n--- got ---\n%s\n--- want ---\n%s", goldenName, got, want)
	}
}

// Replays the exact scenario sequences from capture-goldens.sh against the
// golden after-states produced by the legacy python.
func TestSettingsScenarioGoldens(t *testing.T) {
	t.Parallel()
	reg := func(t *testing.T, f, event, matcher, cmd string) {
		t.Helper()
		if err := RegisterHook(f, event, matcher, cmd); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("01-register-into-empty", func(t *testing.T) {
		t.Parallel()
		f := filepath.Join(t.TempDir(), "s.json")
		os.WriteFile(f, []byte(corpusSettings(t, "01-empty-object.json")), 0o644)
		reg(t, f, "UserPromptSubmit", "", fakeBin+" hook capture")
		reg(t, f, "SessionStart", "startup|resume", fakeBin+" hook session-start")
		assertFile(t, f, "01-register-into-empty")
	})

	t.Run("02-register-absent-file", func(t *testing.T) {
		t.Parallel()
		f := filepath.Join(t.TempDir(), "s.json")
		reg(t, f, "UserPromptSubmit", "", fakeBin+" hook capture")
		assertFile(t, f, "02-register-absent-file")
	})

	t.Run("03-04-foreign-hooks", func(t *testing.T) {
		t.Parallel()
		f := filepath.Join(t.TempDir(), "s.json")
		os.WriteFile(f, []byte(corpusSettings(t, "02-foreign-hooks.json")), 0o644)
		reg(t, f, "UserPromptSubmit", "", fakeBin+" hook capture")
		reg(t, f, "PostToolUse", "Bash", fakeBin+" hook gbrain")
		reg(t, f, "Stop", "", fakeBin+" hook response")
		assertFile(t, f, "03-register-among-foreign")
		if err := UnregisterHook(f, []string{
			fakeBin + " hook capture", fakeBin + " hook gbrain", fakeBin + " hook response",
		}); err != nil {
			t.Fatal(err)
		}
		assertFile(t, f, "04-unregister-leaves-foreign")
	})

	t.Run("05-idempotent-reregister", func(t *testing.T) {
		t.Parallel()
		f := filepath.Join(t.TempDir(), "s.json")
		os.WriteFile(f, []byte(corpusSettings(t, "03-already-registered.json")), 0o644)
		reg(t, f, "UserPromptSubmit", "", fakeBin+" hook capture")
		assertFile(t, f, "05-idempotent-reregister")
	})

	t.Run("06-unregister-keeps-sibling", func(t *testing.T) {
		t.Parallel()
		f := filepath.Join(t.TempDir(), "s.json")
		os.WriteFile(f, []byte(corpusSettings(t, "04-grouped-sibling.json")), 0o644)
		if err := UnregisterHook(f, []string{fakeBin + " hook response"}); err != nil {
			t.Fatal(err)
		}
		assertFile(t, f, "06-unregister-keeps-sibling")
	})
}

func TestMalformedSettingsRejected(t *testing.T) {
	t.Parallel()
	f := filepath.Join(t.TempDir(), "s.json")
	os.WriteFile(f, []byte("{broken"), 0o644)
	if err := RegisterHook(f, "Stop", "", "cmd"); err == nil {
		t.Fatal("malformed settings must abort, not overwrite")
	}
	// the broken file must be untouched
	b, _ := os.ReadFile(f)
	if string(b) != "{broken" {
		t.Error("malformed file was rewritten")
	}
}

func TestBlankFileIsEmptyObject(t *testing.T) {
	t.Parallel()
	f := filepath.Join(t.TempDir(), "s.json")
	os.WriteFile(f, []byte("  \n"), 0o644)
	v, err := ReadSettings(f)
	if err != nil || v.Kind != Object || len(v.Obj) != 0 {
		t.Fatalf("blank file: %v %+v", err, v)
	}
}

// Key order must survive a parse->encode round trip on arbitrary nesting.
func TestRoundTripPreservesOrder(t *testing.T) {
	t.Parallel()
	in := "{\n  \"zebra\": 1,\n  \"alpha\": {\n    \"z\": [\n      1,\n      2\n    ],\n    \"a\": null,\n    \"m\": \"café <&> \\\"q\\\"\"\n  },\n  \"mid\": true,\n  \"num\": 3.5,\n  \"empty_o\": {},\n  \"empty_a\": []\n}\n"
	v, err := Parse([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(Encode(v)); got != in {
		t.Errorf("round trip changed bytes:\n got %q\nwant %q", got, in)
	}
}

// If present, round-trip the user's real settings.json shape (read-only).
func TestRoundTripRealSettingsShape(t *testing.T) {
	t.Parallel()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip()
	}
	b, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Skip("no real settings.json on this machine")
	}
	v, err := Parse(b)
	if err != nil {
		t.Skipf("settings.json unparseable: %v", err)
	}
	v2, err := Parse(Encode(v))
	if err != nil {
		t.Fatalf("re-parse failed: %v", err)
	}
	if string(Encode(v)) != string(Encode(v2)) {
		t.Error("encode is not stable across round trips")
	}
}
