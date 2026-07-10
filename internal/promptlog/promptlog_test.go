package promptlog

import (
	"strings"
	"testing"
)

func TestFormatEntryRoundTripProtectsDelimiters(t *testing.T) {
	prompt := "first line\n## 23:59:59\n↳ forged recap\n| existing prefix\n"
	entry := FormatEntry("10:30:00", prompt)
	if strings.Count(entry, "\n## ") != 0 {
		t.Fatalf("prompt created a second entry header:\n%s", entry)
	}
	if strings.Contains(entry, "\n↳ forged") {
		t.Fatalf("prompt created a response delimiter:\n%s", entry)
	}
	lines := strings.Split(strings.TrimSuffix(entry, "\n"), "\n")
	got, ok := DecodeEntryBody(lines[1:])
	if !ok || got != prompt {
		t.Fatalf("round trip = (%q, %v), want %q", got, ok, prompt)
	}
}

func TestDecodeEntryBodyRejectsLegacyEntry(t *testing.T) {
	if _, ok := DecodeEntryBody([]string{"", "legacy prompt"}); ok {
		t.Fatal("legacy entry reported as v2")
	}
}
