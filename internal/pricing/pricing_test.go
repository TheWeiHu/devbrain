package pricing

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The /api/pricing payload must match the golden (captured from the legacy
// queue.py, key-sort normalized) value-for-value.
func TestAPIPayloadGolden(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "golden", "api", "pricing.json"))
	if err != nil {
		t.Fatal(err)
	}
	var want, got any
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	ours, err := json.Marshal(APIPayload())
	if err != nil {
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
		{"claude-opus-4-8", Rates{5.0, 25.0, 6.25, 0.5}},
		{"claude-opus-4-8-20260115", Rates{5.0, 25.0, 6.25, 0.5}}, // dated id -> opus tier
		{"claude-sonnet-4-6", Rates{3.0, 15.0, 3.75, 0.3}},
		{"claude-haiku-4-5-20251001", Rates{1.0, 5.0, 1.25, 0.1}},
		{"claude-fable-5-20260601", Rates{10.0, 50.0, 12.5, 1.0}},
		{"gpt-5.5", Rates{5.0, 30.0, 0.0, 0.5}}, // OpenAI never bills cache writes
		{"gpt-5.4-mini", Rates{0.75, 4.5, 0.0, 0.075}},
		{"gpt-5-codex", Rates{1.25, 10.0, 0.0, 0.125}},
		{"gpt-5.6-sol", Rates{5.0, 30.0, 0.0, 0.5}},
		{"gpt-5.6", Rates{5.0, 30.0, 0.0, 0.5}},                // official alias for Sol
		{"gpt-5.6-luna-2026-06-01", Rates{1.0, 6.0, 0.0, 0.1}}, // dated id -> luna tier
		{"gpt-5.6-sol-2026-06-01", Rates{5.0, 30.0, 0.0, 0.5}}, // dated id -> sol tier, not bare 5.6
		{"gpt-6-preview", Rates{0, 0, 0, 0}},                   // unknown future model stays $0
		{"totally-unknown-model", Rates{0, 0, 0, 0}},           // unknown -> $0, not Opus
		{"<synthetic>", Rates{0, 0, 0, 0}},                     // local, no real bill
		{"", Rates{0, 0, 0, 0}},
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
