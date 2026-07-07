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
	"errors"
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

// neverMatchRe matches no input — the compiled form of a rule the user cleared to
// an empty string. (An empty pattern matches EVERYTHING, which would flag every
// prompt; a cleared rule means "off", so it must match nothing instead.)
var neverMatchRe = regexp.MustCompile(`[^\s\S]`)

// compileRule turns a pattern into a matcher; an empty pattern compiles to "off".
func compileRule(pat string) (*regexp.Regexp, error) {
	if pat == "" {
		return neverMatchRe, nil
	}
	return regexp.Compile(pat)
}

func (rb *Rulebook) compile() (err error) {
	if rb.cwdRe, err = compileRule(rb.AutonomousCwdRegex); err != nil {
		return err
	}
	if rb.wtRe, err = compileRule(rb.AutonomousWtRegex); err != nil {
		return err
	}
	rb.voiceRe, err = compileRule(rb.PayloadVoiceRegex)
	return err
}

// valid rejects parseable-but-nonsensical numeric tunables — a negative signature
// length panics the slicer, and zero/negative copy thresholds flip EVERY prompt.
// An override that fails this falls open to the default, same as bad JSON.
func (rb *Rulebook) valid() bool {
	return rb.RepeatSignatureLen > 0 &&
		rb.RepeatLongWords >= 0 &&
		rb.RepeatMinCopiesShort >= 1 &&
		rb.RepeatMinCopiesLong >= 1 &&
		rb.PayloadMinWords >= 0 &&
		rb.PayloadCrossProjMin >= 1
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
	if !rb.valid() {
		return defaultRulebook()
	}
	if err := rb.compile(); err != nil {
		return defaultRulebook()
	}
	return rb
}

// SeedRulebook writes the embedded default to $dataDir/rulebook.json when absent,
// so a fresh install ships an editable copy. The O_EXCL create is atomic — it never
// overwrites (or truncates) an existing file, even under a concurrent install.
// Returns whether it wrote.
func SeedRulebook(dataDir string) (bool, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return false, err
	}
	f, err := os.OpenFile(RulebookPath(dataDir), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return false, nil
		}
		return false, err
	}
	defer f.Close()
	if _, err := f.Write(defaultRulebookJSON); err != nil {
		return false, err
	}
	return true, nil
}
