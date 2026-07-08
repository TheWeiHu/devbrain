package dashboard

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRulebookDefault(t *testing.T) {
	t.Parallel()
	rb := LoadRulebook(t.TempDir()) // no override file -> pristine default
	if rb.PayloadMinWords != 150 || rb.RepeatMinCopiesShort != 3 || rb.RepeatMinCopiesLong != 2 {
		t.Fatalf("default thresholds wrong: %+v", rb)
	}
	if rb.Classify("/x", false) != "command" || rb.Classify("hi", true) != "nightshift" {
		t.Fatal("default classify behavior changed")
	}
}

func TestLoadRulebookOverlay(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Override ONE key; every other rule must keep its default.
	writeFile(t, RulebookPath(dir), `{"payload_min_words": 999}`)
	rb := LoadRulebook(dir)
	if rb.PayloadMinWords != 999 {
		t.Fatalf("override not applied: got %d", rb.PayloadMinWords)
	}
	if rb.RepeatMinCopiesShort != 3 || len(rb.SystemPrefixes) == 0 {
		t.Fatalf("omitted keys did not fall back to default: %+v", rb)
	}
}

func TestLoadRulebookFallsOpen(t *testing.T) {
	t.Parallel()
	def := defaultRulebook()
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
		writeFile(t, RulebookPath(dir), bad)
		rb := LoadRulebook(dir)
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
	writeFile(t, RulebookPath(dir), `{"payload_voice_regex": ""}`)
	rb := LoadRulebook(dir)
	if rb.voiceRe.MatchString("You are reviewing a giant pasted rubric") {
		t.Fatal("cleared payload_voice_regex must match nothing, not everything")
	}
}

func TestStripWrapper(t *testing.T) {
	t.Parallel()
	rb := defaultRulebook()
	// Conductor's first-message wrapper is peeled, leaving the real typed prompt.
	got := rb.StripWrapper("<system_instruction>\nYou are inside Conductor.\n</system_instruction>\n\n/distill and release")
	if got != "/distill and release" {
		t.Errorf("wrapped prompt = %q, want the /distill underneath", got)
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
	writeFile(t, RulebookPath(dir), `{"wrapper_strip_regex": ""}`)
	off := LoadRulebook(dir)
	in := "<system_instruction>\nx\n</system_instruction>\n\n/distill"
	if off.StripWrapper(in) != in {
		t.Error("cleared wrapper_strip_regex must strip nothing")
	}
}

func TestNormalizePrompt(t *testing.T) {
	t.Parallel()
	rb := defaultRulebook()
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
	writeFile(t, RulebookPath(dir), `{"command_extract_regex": ""}`)
	off := LoadRulebook(dir)
	in := "<command-name>/continue</command-name>"
	if off.NormalizePrompt(in) != in {
		t.Error("cleared command_extract_regex must rewrite nothing")
	}
}

func TestSeedRulebook(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	wrote, err := SeedRulebook(dir)
	if err != nil || !wrote {
		t.Fatalf("first seed: wrote=%v err=%v", wrote, err)
	}
	// The seeded copy is an empty delta: it must override nothing, so a fresh
	// install behaves exactly like the shipped default (and tracks it on upgrade).
	seeded, def := LoadRulebook(dir), defaultRulebook()
	if seeded.PayloadMinWords != def.PayloadMinWords || seeded.PayloadVoiceRegex != def.PayloadVoiceRegex {
		t.Fatalf("seeded copy is not an empty delta: %+v", seeded)
	}
	// Hand-edit, then re-seed: must NOT clobber.
	writeFile(t, RulebookPath(dir), `{"payload_min_words": 7}`)
	wrote, err = SeedRulebook(dir)
	if err != nil || wrote {
		t.Fatalf("second seed clobbered edits: wrote=%v err=%v", wrote, err)
	}
	if LoadRulebook(dir).PayloadMinWords != 7 {
		t.Fatal("re-seed overwrote the user's rulebook")
	}
}

func TestSeedRulebookMigratesLegacy(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// A pre-preferences/ install left rulebook.json at the top level.
	legacy := filepath.Join(dir, "rulebook.json")
	writeFile(t, legacy, `{"payload_min_words": 42}`)
	wrote, err := SeedRulebook(dir)
	if err != nil || !wrote {
		t.Fatalf("migrate seed: wrote=%v err=%v", wrote, err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatal("legacy rulebook.json was not moved out of the top level")
	}
	// The override survives the move under its new preferences/ home.
	if LoadRulebook(dir).PayloadMinWords != 42 {
		t.Fatal("migrated override did not carry to preferences/rulebook.json")
	}
	// A preferences/ copy is never clobbered by a stray legacy file.
	writeFile(t, legacy, `{"payload_min_words": 7}`)
	wrote, err = SeedRulebook(dir)
	if err != nil || wrote {
		t.Fatalf("re-seed clobbered preferences/ copy: wrote=%v err=%v", wrote, err)
	}
	if LoadRulebook(dir).PayloadMinWords != 42 {
		t.Fatal("existing preferences/rulebook.json was overwritten by legacy file")
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
