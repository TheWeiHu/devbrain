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
	for _, bad := range []string{`{not json`, `{"autonomous_cwd_regex": "("}`} {
		dir := t.TempDir()
		writeFile(t, RulebookPath(dir), bad)
		rb := LoadRulebook(dir)
		if rb.PayloadMinWords != def.PayloadMinWords || rb.AutonomousCwdRegex != def.AutonomousCwdRegex {
			t.Fatalf("corrupt override %q did not fall open to default", bad)
		}
	}
}

func TestSeedRulebook(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	wrote, err := SeedRulebook(dir)
	if err != nil || !wrote {
		t.Fatalf("first seed: wrote=%v err=%v", wrote, err)
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

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
