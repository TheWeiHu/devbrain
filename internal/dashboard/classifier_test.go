package dashboard

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadClassifierDefault(t *testing.T) {
	t.Parallel()
	rb := LoadClassifier(t.TempDir()) // no override file -> pristine default
	if rb.PayloadMinWords != 150 || rb.RepeatMinCopiesShort != 3 || rb.RepeatMinCopiesLong != 2 {
		t.Fatalf("default thresholds wrong: %+v", rb)
	}
	if len(rb.CollapsePrefixes) != 1 || rb.CollapsePrefixes[0] != "PLEASE IMPLEMENT THIS PLAN:" {
		t.Fatalf("default collapse prefixes wrong: %+v", rb.CollapsePrefixes)
	}
	if !containsString(rb.SystemPrefixes, "<ide_opened_file>") || !containsString(rb.SystemPrefixes, "# Context from my IDE setup:") {
		t.Fatalf("default classifier missing wrapper-only IDE envelopes: %+v", rb.SystemPrefixes)
	}
	if rb.Classify("/x", false) != "command" || rb.Classify("hi", true) != "nightshift" {
		t.Fatal("default classify behavior changed")
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestLoadClassifierOverlay(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Override ONE key; every other rule must keep its default.
	writeFile(t, ClassifierPath(dir), `{"payload_min_words": 999}`)
	rb := LoadClassifier(dir)
	if rb.PayloadMinWords != 999 {
		t.Fatalf("override not applied: got %d", rb.PayloadMinWords)
	}
	if rb.RepeatMinCopiesShort != 3 || len(rb.SystemPrefixes) == 0 {
		t.Fatalf("omitted keys did not fall back to default: %+v", rb)
	}
}

func TestLoadClassifierFallsOpen(t *testing.T) {
	t.Parallel()
	def := defaultClassifier()
	// bad JSON, bad regex, and parseable-but-nonsensical numerics all fall open.
	bads := []string{
		`{not json`,
		`{"autonomous_cwd_regex": "("}`,
		`{"repeat_signature_len": -1}`,   // would panic the slicer
		`{"repeat_min_copies_short": 0}`, // would flip every prompt
		`{"payload_cross_project_min": 0}`,
	}
	for _, bad := range bads {
		dir := t.TempDir()
		writeFile(t, ClassifierPath(dir), bad)
		rb := LoadClassifier(dir)
		if rb.PayloadMinWords != def.PayloadMinWords || rb.RepeatSignatureLen != def.RepeatSignatureLen ||
			rb.AutonomousCwdRegex != def.AutonomousCwdRegex {
			t.Fatalf("invalid override %q did not fall open to default: %+v", bad, rb)
		}
	}
}

func TestClearedRegexIsDisabled(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Clearing payload_voice_regex means "off", not "match everything".
	writeFile(t, ClassifierPath(dir), `{"payload_voice_regex": ""}`)
	rb := LoadClassifier(dir)
	if rb.voiceRe.MatchString("You are reviewing a giant pasted rubric") {
		t.Fatal("cleared payload_voice_regex must match nothing, not everything")
	}
}

func TestStripWrapper(t *testing.T) {
	t.Parallel()
	rb := defaultClassifier()
	// Conductor's first-message wrapper is peeled, leaving the real typed prompt.
	got := rb.StripWrapper("<system_instruction>\nYou are inside Conductor.\n</system_instruction>\n\n/distill and release")
	if got != "/distill and release" {
		t.Errorf("wrapped prompt = %q, want the /distill underneath", got)
	}
	// IDE capture wrappers are context, not owner voice. Keep only the request
	// underneath, and support multiple consecutive known wrappers.
	ide := "# Context from my IDE setup:\n\n## Active file: main.go\n\n## My request for Codex:\n\nfix the race"
	if got := rb.StripWrapper(ide); got != "fix the race" {
		t.Errorf("IDE context wrapper = %q, want request only", got)
	}
	opened := "<ide_opened_file>The user opened /tmp/main.go in the IDE.</ide_opened_file>\n\nexplain this function"
	if got := rb.StripWrapper(opened); got != "explain this function" {
		t.Errorf("IDE opened-file wrapper = %q, want request only", got)
	}
	stacked := "<system_instruction>workspace</system_instruction>\n<ide_opened_file>x.go</ide_opened_file>\n\nrun tests"
	if got := rb.StripWrapper(stacked); got != "run tests" {
		t.Errorf("stacked wrappers = %q, want request only", got)
	}
	// No wrapper -> unchanged; a bare open tag (no close) is not a wrapper; a
	// wrapper-only turn (nothing after) is left intact so it still reads as system.
	for _, s := range []string{
		"/distill",
		"<system_instruction>no close tag here",
		"<system_instruction>\nonly the block\n</system_instruction>\n",
	} {
		if rb.StripWrapper(s) != s {
			t.Errorf("StripWrapper(%q) mutated a non-payload/wrapper-only turn", s)
		}
	}
	// Cleared to "" means off: nothing is stripped.
	dir := t.TempDir()
	writeFile(t, ClassifierPath(dir), `{"wrapper_strip_regex": ""}`)
	off := LoadClassifier(dir)
	in := "<system_instruction>\nx\n</system_instruction>\n\n/distill"
	if off.StripWrapper(in) != in {
		t.Error("cleared wrapper_strip_regex must strip nothing")
	}
}

func TestNormalizePrompt(t *testing.T) {
	t.Parallel()
	rb := defaultClassifier()
	cases := map[string]string{
		// Claude Code slash-command expansion -> the bare /command.
		"<command-message>continue</command-message>\n<command-name>/continue</command-name>": "/continue",
		"<command-name>/x</command-name>": "/x",
		// Conductor wrapper still peeled (composition with StripWrapper).
		"<system_instruction>\ncwd\n</system_instruction>\n\n/distill": "/distill",
		// Non-command prose and a bare command pass through untouched.
		"how do we fix this?": "how do we fix this?",
		"/ship it":            "/ship it",
		// A quoted command-name mid-prose is NOT at the start -> left alone.
		"see <command-name>/continue</command-name> above": "see <command-name>/continue</command-name> above",
	}
	for in, want := range cases {
		if got := rb.NormalizePrompt(in); got != want {
			t.Errorf("NormalizePrompt(%q) = %q, want %q", in, got, want)
		}
	}
	// Cleared to "" means off: the command block is left as-is (classifies system).
	dir := t.TempDir()
	writeFile(t, ClassifierPath(dir), `{"command_extract_regex": ""}`)
	off := LoadClassifier(dir)
	in := "<command-name>/continue</command-name>"
	if off.NormalizePrompt(in) != in {
		t.Error("cleared command_extract_regex must rewrite nothing")
	}
}

func TestCollapsePrompt(t *testing.T) {
	t.Parallel()
	rb := defaultClassifier()
	cases := map[string]string{
		"PLEASE IMPLEMENT THIS PLAN:\n\n# Giant generated plan\n1. do the work": "PLEASE IMPLEMENT THIS PLAN:",
		"please quote PLEASE IMPLEMENT THIS PLAN: in the docs":                  "please quote PLEASE IMPLEMENT THIS PLAN: in the docs",
		"PLEASE IMPLEMENT THIS PLAN:":                                           "PLEASE IMPLEMENT THIS PLAN:",
	}
	for in, want := range cases {
		if got := rb.CollapsePrompt(in); got != want {
			t.Errorf("CollapsePrompt(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSeedClassifier(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	wrote, err := SeedClassifier(dir)
	if err != nil || !wrote {
		t.Fatalf("first seed: wrote=%v err=%v", wrote, err)
	}
	// The seeded copy is an empty delta: it must override nothing, so a fresh
	// install behaves exactly like the shipped default (and tracks it on upgrade).
	seeded, def := LoadClassifier(dir), defaultClassifier()
	if seeded.PayloadMinWords != def.PayloadMinWords || seeded.PayloadVoiceRegex != def.PayloadVoiceRegex {
		t.Fatalf("seeded copy is not an empty delta: %+v", seeded)
	}
	// Hand-edit, then re-seed: must NOT clobber.
	writeFile(t, ClassifierPath(dir), `{"payload_min_words": 7}`)
	wrote, err = SeedClassifier(dir)
	if err != nil || wrote {
		t.Fatalf("second seed clobbered edits: wrote=%v err=%v", wrote, err)
	}
	if LoadClassifier(dir).PayloadMinWords != 7 {
		t.Fatal("re-seed overwrote the user's classifier config")
	}
}

// Both former homes migrate to preferences/prompt-classifier.json: the earlier
// preferences/rulebook.json name, and the pre-preferences/ top-level rulebook.json.
func TestSeedClassifierMigratesLegacy(t *testing.T) {
	t.Parallel()
	for _, legacyRel := range []string{"rulebook.json", "preferences/rulebook.json"} {
		t.Run(legacyRel, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			legacy := filepath.Join(dir, filepath.FromSlash(legacyRel))
			writeFile(t, legacy, `{"payload_min_words": 42}`)
			wrote, err := SeedClassifier(dir)
			if err != nil || !wrote {
				t.Fatalf("migrate seed: wrote=%v err=%v", wrote, err)
			}
			if _, err := os.Stat(legacy); !os.IsNotExist(err) {
				t.Fatalf("legacy %s was not moved to the new name", legacyRel)
			}
			// The override survives the move under its new prompt-classifier.json home.
			if LoadClassifier(dir).PayloadMinWords != 42 {
				t.Fatal("migrated override did not carry to preferences/prompt-classifier.json")
			}
			// The new copy is never clobbered by a stray legacy file left behind.
			writeFile(t, legacy, `{"payload_min_words": 7}`)
			wrote, err = SeedClassifier(dir)
			if err != nil || wrote {
				t.Fatalf("re-seed clobbered the new copy: wrote=%v err=%v", wrote, err)
			}
			if LoadClassifier(dir).PayloadMinWords != 42 {
				t.Fatal("existing preferences/prompt-classifier.json was overwritten by legacy file")
			}
		})
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
