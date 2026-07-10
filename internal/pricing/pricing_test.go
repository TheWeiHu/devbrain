package pricing

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The /api/pricing payload must match the reviewed, key-sort-normalized golden.
func TestAPIPayloadGolden(t *testing.T) {
	t.Parallel()
	golden := filepath.Join("..", "..", "testdata", "golden", "api", "pricing.json")
	ours, err := json.MarshalIndent(APIPayload(), "", " ")
	if err != nil {
		t.Fatal(err)
	}
	ours = append(ours, '\n')
	if os.Getenv("DEVBRAIN_GEN_GOLDEN") != "" {
		if err := os.WriteFile(golden, ours, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	var want, got any
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(ours, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("payload mismatch\ngot  %s\nwant %s", ours, raw)
	}
}

// Ports of scripts/test-model-pricing.py.
func TestBillingRates(t *testing.T) {
	t.Parallel()
	cases := []struct {
		model string
		want  Rates
	}{
		{"claude-opus-4-8", Rates{5.0, 25.0, 6.25, 0.5, 10.0}},
		{"claude-opus-4-8-20260115", Rates{5.0, 25.0, 6.25, 0.5, 10.0}}, // dated id -> opus tier
		{"claude-sonnet-4-6", Rates{3.0, 15.0, 3.75, 0.3, 6.0}},
		{"claude-haiku-4-5-20251001", Rates{1.0, 5.0, 1.25, 0.1, 2.0}},
		{"claude-fable-5-20260601", Rates{10.0, 50.0, 12.5, 1.0, 20.0}},
		{"gpt-5.6-sol", Rates{5.0, 30.0, 6.25, 0.5, 6.25}},
		{"gpt-5.6-terra", Rates{2.5, 15.0, 3.125, 0.25, 3.125}},
		{"gpt-5.6-luna", Rates{1.0, 6.0, 1.25, 0.1, 1.25}},
		{"gpt-5.5", Rates{5.0, 30.0, 0.0, 0.5, 0.0}},
		{"gpt-5.4-mini-20260601", Rates{0.75, 4.5, 0.0, 0.075, 0.0}},
		{"gpt-5.2-codex", Rates{1.75, 14.0, 0.0, 0.175, 0.0}},
		{"gpt-5.1-codex", Rates{1.25, 10.0, 0.0, 0.125, 0.0}},
		{"claude-3-opus", Rates{0, 0, 0, 0, 0}},         // old family must not inherit current Opus pricing
		{"totally-unknown-model", Rates{0, 0, 0, 0, 0}}, // unknown -> $0, not Opus
		{"<synthetic>", Rates{0, 0, 0, 0, 0}},           // local, no real bill
		{"", Rates{0, 0, 0, 0, 0}},
	}
	for _, c := range cases {
		if got := BillingRates(c.model); got != c.want {
			t.Errorf("BillingRates(%q) = %v, want %v", c.model, got, c.want)
		}
	}
}

func TestRate(t *testing.T) {
	t.Parallel()
	in, out := Rate("claude-sonnet-4-6")
	if in != 3.0 || out != 15.0 {
		t.Errorf("Rate = %v/%v, want 3/15", in, out)
	}
}

func TestCostUSD(t *testing.T) {
	t.Parallel()
	// 1M in + 1M out + 1M cc + 1M cr on opus: 5 + 25 + 6.25 + 0.5
	got := CostUSD(map[string][]float64{"claude-opus-4-8": {1e6, 1e6, 1e6, 1e6}})
	if got != 36.75 {
		t.Errorf("full-row cost = %v, want 36.75", got)
	}
	// The fifth count is the 1-hour subset of aggregate cache writes. One
	// million Opus 1h writes costs $10, not the 5m estimate of $6.25.
	got = CostUSD(map[string][]float64{"claude-opus-4-8": {0, 0, 1e6, 0, 1e6}})
	if got != 10.0 {
		t.Errorf("1h cache cost = %v, want 10", got)
	}
	// legacy 2-element row tolerated (no cache columns)
	got = CostUSD(map[string][]float64{"claude-opus-4-8": {1e6, 1e6}})
	if got != 30.0 {
		t.Errorf("legacy-row cost = %v, want 30", got)
	}
	// round(…, 4): 123 in-tokens on opus = 0.000615
	got = CostUSD(map[string][]float64{"claude-opus-4-8": {123, 0}})
	if got != 0.0006 {
		t.Errorf("rounded cost = %v, want 0.0006", got)
	}
	if got := CostUSD(nil); got != 0 {
		t.Errorf("empty cost = %v, want 0", got)
	}
	// multiple models sum
	got = CostUSD(map[string][]float64{
		"claude-opus-4-8":   {1e6, 0},
		"claude-sonnet-4-6": {0, 1e6},
	})
	if got != 20.0 {
		t.Errorf("multi-model cost = %v, want 20", got)
	}
}

func TestIsPriced(t *testing.T) {
	t.Parallel()
	for _, model := range []string{"gpt-5.6-sol", "gpt-5.4-mini-20260601", "gpt-5.1-codex", "claude-opus-4-8"} {
		if !IsPriced(model) {
			t.Errorf("IsPriced(%q) = false", model)
		}
	}
	for _, model := range []string{"", "<synthetic>", "future-unknown"} {
		if IsPriced(model) {
			t.Errorf("IsPriced(%q) = true", model)
		}
	}
}
