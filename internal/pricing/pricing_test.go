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
		{"claude-opus-4-8", standardRates(5, 25, 6.25, 0.5, 10)},
		{"claude-opus-4-8-20260115", standardRates(5, 25, 6.25, 0.5, 10)}, // dated snapshot
		{"claude-sonnet-4-6", standardRates(3, 15, 3.75, 0.3, 6)},
		{"claude-haiku-4-5-20251001", standardRates(1, 5, 1.25, 0.1, 2)},
		{"claude-fable-5-20260601", standardRates(10, 50, 12.5, 1, 20)},
		{"claude-mythos-5", standardRates(10, 50, 12.5, 1, 20)},
		{"claude-sonnet-5", standardRates(2, 10, 2.5, 0.2, 4)},
		{"gpt-5.6", longContextRates(5, 30, 6.25, 0.5, 6.25)},
		{"gpt-5.6-sol", longContextRates(5, 30, 6.25, 0.5, 6.25)},
		{"gpt-5.6-terra", longContextRates(2.5, 15, 3.125, 0.25, 3.125)},
		{"gpt-5.6-luna", longContextRates(1, 6, 1.25, 0.1, 1.25)},
		{"gpt-5.5-pro-20260701", longContextRates(30, 180, 0, 0, 0)},
		{"gpt-5.5", longContextRates(5, 30, 0, 0.5, 0)},
		{"gpt-5.4-mini-20260601", standardRates(0.75, 4.5, 0, 0.075, 0)},
		{"gpt-5.2-pro", standardRates(21, 168, 0, 0, 0)},
		{"gpt-5.2-codex", standardRates(1.75, 14, 0, 0.175, 0)},
		{"gpt-5.1-codex", standardRates(1.25, 10, 0, 0.125, 0)},
		{"gpt-5-mini", standardRates(0.25, 2, 0, 0.025, 0)},
		{"gpt-4.1-mini", standardRates(0.4, 1.6, 0, 0.1, 0)},
		{"o4-mini", standardRates(1.1, 4.4, 0, 0.275, 0)},
		{"claude-3-opus", Default}, // old family must not inherit current Opus pricing
		{"gpt-5.5-ultra", Default}, // unknown named variant must not inherit base pricing
		{"totally-unknown-model", Default},
		{"<synthetic>", Default},
		{"", Default},
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

	// GPT-5.6 requests over 272K input are 2x input/cache and 1.5x
	// output for the full request: base 35.5 + surcharge 20.5 = 56.
	got = CostUSD(map[string][]float64{
		"gpt-5.6-sol": {1e6, 1e6, 0, 1e6, 0, 1e6, 1e6, 0, 1e6},
	})
	if got != 56.0 {
		t.Errorf("long-context cost = %v, want 56", got)
	}
	if got := Cost("gpt-5.6-sol", 1e6, 1e6, 0, 1e6, 0); got != 35.5 {
		t.Errorf("legacy aggregate cost = %v, want 35.5", got)
	}
}

func TestIsPriced(t *testing.T) {
	t.Parallel()
	for _, model := range []string{"gpt-5.6", "gpt-5.6-sol", "gpt-5.5-pro", "gpt-5.4-mini-20260601", "gpt-5.1-codex", "o3", "claude-opus-4-8"} {
		if !IsPriced(model) {
			t.Errorf("IsPriced(%q) = false", model)
		}
	}
	for _, model := range []string{"", "<synthetic>", "future-unknown", "gpt-5.5-ultra", "not-gpt-5.5"} {
		if IsPriced(model) {
			t.Errorf("IsPriced(%q) = true", model)
		}
	}
}

func TestLongContextAndCreditMetadata(t *testing.T) {
	t.Parallel()
	for _, model := range []string{"gpt-5.6", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.5", "gpt-5.4-pro"} {
		if !HasLongContextPricing(model) {
			t.Errorf("HasLongContextPricing(%q) = false", model)
		}
	}
	for _, model := range []string{"gpt-5.4-mini", "gpt-5.2", "claude-opus-4-8"} {
		if HasLongContextPricing(model) {
			t.Errorf("HasLongContextPricing(%q) = true", model)
		}
	}

	creditCases := map[string]CreditRates{
		"gpt-5.6": {125, 12.5, 750}, "gpt-5.6-sol": {125, 12.5, 750},
		"gpt-5.6-terra": {62.5, 6.25, 375}, "gpt-5.6-luna": {25, 2.5, 150},
		"gpt-5.5": {125, 12.5, 750}, "gpt-5.4": {62.5, 6.25, 375},
		"gpt-5.4-mini": {18.75, 1.875, 113},
	}
	for model, want := range creditCases {
		if got, ok := CodexCreditRates(model); !ok || got != want {
			t.Errorf("CodexCreditRates(%q) = %v, %v; want %v, true", model, got, ok, want)
		}
	}

	credits, ok := CreditCost("gpt-5.6-sol", 1e6, 1e6, 1e6)
	if !ok || credits != 887.5 {
		t.Errorf("Sol credits = %v, %v; want 887.5, true", credits, ok)
	}
	if _, ok := CreditCost("gpt-5.3-codex-spark", 1, 1, 1); ok {
		t.Error("research preview must not receive a guessed credit rate")
	}
	if reason, ok := UnpricedReason("gpt-5.3-codex-spark"); !ok || reason == "" {
		t.Errorf("Spark reason = %q, %v", reason, ok)
	}
}

func TestSnapshotFallbacksReferenceCanonicalCatalog(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for _, tier := range Tiers {
		if seen[tier.Prefix] {
			t.Errorf("duplicate snapshot family %q", tier.Prefix)
		}
		seen[tier.Prefix] = true
		if _, ok := Models[tier.Prefix]; !ok {
			t.Errorf("snapshot family %q has no canonical API rate", tier.Prefix)
		}
	}
	for _, family := range CreditTiers {
		if _, ok := CreditModels[family]; !ok {
			t.Errorf("credit snapshot family %q has no canonical rate", family)
		}
	}
}
