package transcript

import (
	"path/filepath"
	"strings"
	"testing"
)

// A rate-limit banner is stored as an assistant record with isApiErrorMessage
// set. It is not the model's words, so it must not become the recap.
const rcBanner = `{"type":"user","timestamp":"2026-08-29T19:00:00Z","cwd":"/repo","message":{"content":"how many buyers?"}}
{"type":"assistant","timestamp":"2026-08-29T19:05:00Z","message":{"id":"m1","model":"claude-opus-4-5","usage":{"input_tokens":5,"output_tokens":6},"content":[{"type":"text","text":"Counted 42 buyers across the three cohorts."}]}}
{"type":"assistant","timestamp":"2026-08-29T19:06:52Z","error":"rate_limit","isApiErrorMessage":true,"message":{"id":"m2","model":"<synthetic>","usage":{"input_tokens":0,"output_tokens":0},"content":[{"type":"text","text":"You've hit your monthly spend limit · raise it at claude.ai/settings/usage?from=cc_cli_limit_message · your session limit resets 3:30pm (America/New_York)"}]}}
`

func TestResponseCaptureSkipsHarnessBanner(t *testing.T) {
	path := writeFixture(t, "rc-banner.jsonl", rcBanner)
	sidecar := filepath.Join(t.TempDir(), "tokens.jsonl")
	got := ResponseCapture(path, sidecar, "sess-b", "2026-08-29T19:00:00Z", false, "")
	if !strings.Contains(got, "Counted 42 buyers across the three cohorts.") {
		t.Errorf("recap lost the real closing sentence:\n%s", got)
	}
	if strings.Contains(got, "spend limit") {
		t.Errorf("harness banner leaked into the capture:\n%s", got)
	}
}

func TestRecapIgnoresHarnessBanners(t *testing.T) {
	cases := map[string]string{
		"banner last":   "Shipped the fix.",
		"banner only":   "",
		"usage variant": "Shipped the fix.",
	}
	inputs := map[string][]string{
		"banner last":   {"Shipped the fix.", "You've hit your monthly spend limit. Run /usage-credits to manage your limit."},
		"banner only":   {"You've hit your monthly spend limit · raise it at claude.ai/settings/usage"},
		"usage variant": {"Shipped the fix.", "Claude usage limit reached. Your session limit resets 3:30pm (America/New_York)"},
	}
	for name, want := range cases {
		if got := Recap(inputs[name]); got != want {
			t.Errorf("%s: got %q want %q", name, got, want)
		}
	}
	if IsHarnessBanner("You have limited the scope to three files.") {
		t.Error("ordinary prose misclassified as a banner")
	}
}
