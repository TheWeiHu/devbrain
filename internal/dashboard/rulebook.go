// The prompt-classifier rulebook: the matchers and thresholds that decide a
// prompt's kind, lifted out of scan.go's consts so they can be tuned without a
// rebuild. The embedded rulebook.json is the built-in default; a copy is seeded
// into $DEVBRAIN_DATA/rulebook.json at install time, and any key set there
// overlays the default. Loading falls open to the pristine default on a
// missing/corrupt override — the classifier must never die on bad config.
package dashboard

import (
	_ "embed"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
)

//go:embed rulebook.json
var defaultRulebookJSON []byte

// systemHeadRunes is how far into a prompt SystemHeadContains looks (the pasted
// "Caveat:" banner sits near the top). Not a tunable — it's a scan detail.
const systemHeadRunes = 200

// Rulebook holds every tunable used by Classify + the reclassify passes. String
// fields are matched literally except *_regex, which are compiled once into the
// unexported re fields. The kind taxonomy itself (typedKinds) stays fixed in code.
type Rulebook struct {
	SystemPrefixes       []string `json:"system_prefixes"`
	SystemHeadContains   []string `json:"system_head_contains"`
	TitleGenPrefixes     []string `json:"title_gen_prefixes"`
	NightshiftPrefixes   []string `json:"nightshift_prefixes"`
	CommandPrefix        string   `json:"command_prefix"`
	AutonomousCwdRegex   string   `json:"autonomous_cwd_regex"`
	AutonomousWtRegex    string   `json:"autonomous_worktree_regex"`
	PayloadVoiceRegex    string   `json:"payload_voice_regex"`
	RepeatSignatureLen   int      `json:"repeat_signature_len"`
	RepeatLongWords      int      `json:"repeat_long_words"`
	RepeatMinCopiesShort int      `json:"repeat_min_copies_short"`
	RepeatMinCopiesLong  int      `json:"repeat_min_copies_long"`
	PayloadMinWords      int      `json:"payload_min_words"`
	PayloadCrossProjMin  int      `json:"payload_cross_project_min"`

	cwdRe, wtRe, voiceRe *regexp.Regexp
}

func (rb *Rulebook) compile() (err error) {
	if rb.cwdRe, err = regexp.Compile(rb.AutonomousCwdRegex); err != nil {
		return err
	}
	if rb.wtRe, err = regexp.Compile(rb.AutonomousWtRegex); err != nil {
		return err
	}
	rb.voiceRe, err = regexp.Compile(rb.PayloadVoiceRegex)
	return err
}

// defaultRulebook parses the embedded default. It panics on a bad embed — that's
// a build-time bug in this repo, not a runtime condition.
func defaultRulebook() *Rulebook {
	rb := &Rulebook{}
	if err := json.Unmarshal(defaultRulebookJSON, rb); err != nil {
		panic("dashboard: embedded rulebook.json is invalid: " + err.Error())
	}
	if err := rb.compile(); err != nil {
		panic("dashboard: embedded rulebook regex invalid: " + err.Error())
	}
	return rb
}

// RulebookPath is the override location inside a data repo.
func RulebookPath(dataDir string) string { return filepath.Join(dataDir, "rulebook.json") }

// LoadRulebook returns the default overlaid with $dataDir/rulebook.json when that
// file is present and valid. Keys omitted in the override keep their default (the
// override is unmarshalled onto the populated default). Any failure — missing file,
// bad JSON, bad regex — falls open to the pristine default.
func LoadRulebook(dataDir string) *Rulebook {
	rb := defaultRulebook()
	if dataDir == "" {
		return rb
	}
	b, err := os.ReadFile(RulebookPath(dataDir))
	if err != nil {
		return rb
	}
	if err := json.Unmarshal(b, rb); err != nil {
		return defaultRulebook()
	}
	if err := rb.compile(); err != nil {
		return defaultRulebook()
	}
	return rb
}

// SeedRulebook writes the embedded default to $dataDir/rulebook.json when absent,
// so a fresh install ships an editable copy. It never overwrites an existing file.
// Returns whether it wrote.
func SeedRulebook(dataDir string) (bool, error) {
	p := RulebookPath(dataDir)
	if _, err := os.Stat(p); err == nil {
		return false, nil
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(p, defaultRulebookJSON, 0o644)
}
