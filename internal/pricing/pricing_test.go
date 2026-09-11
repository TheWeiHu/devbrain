package pricing

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
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
		{"gpt-6-astra", Rates{10, 50, 12.5, 1}},
		{"gpt-6-astra-2026-09-01", Rates{10, 50, 12.5, 1}},
		{"claude-fable-5-1", Rates{10, 50, 12.5, 0.25}},
		{"claude-fable-5-1-20260901", Rates{10, 50, 12.5, 0.25}},
		{"claude-mythos-5-1", Rates{10, 50, 12.5, 0.25}},
		{"claude-opus-5", Rates{5, 25, 6.25, 0.5}},
		{"claude-sonnet-5", Rates{2, 10, 2.5, 0.2}},
		{"claude-sonnet-5-20260901", Rates{2, 10, 2.5, 0.2}},
		{"gpt-5.6-terra", Rates{2, 12, 2.5, 0.2}},
		{"claude-opus-4-8", Rates{5.0, 25.0, 6.25, 0.5}},
		{"claude-opus-4-8-20260115", Rates{5.0, 25.0, 6.25, 0.5}}, // dated id -> opus tier
		{"claude-sonnet-4-6", Rates{3.0, 15.0, 3.75, 0.3}},
		{"claude-haiku-4-5-20251001", Rates{1.0, 5.0, 1.25, 0.1}},
		{"claude-fable-5-20260601", Rates{10.0, 50.0, 12.5, 1.0}},
		{"gpt-5.5", Rates{5.0, 30.0, 0.0, 0.5}}, // legacy model has no cache-write rate
		{"gpt-5.4-mini", Rates{0.75, 4.5, 0.0, 0.075}},
		{"gpt-5-codex", Rates{1.25, 10.0, 0.0, 0.125}},
		{"gpt-5.6-sol", Rates{4.0, 20.0, 5.0, 0.4}},
		{"gpt-5.6", Rates{4.0, 20.0, 5.0, 0.4}},                  // official alias for Sol
		{"gpt-5.6-luna-2026-06-01", Rates{0.2, 1.2, 0.25, 0.02}}, // dated id -> luna tier
		{"gpt-5.6-sol-2026-06-01", Rates{4.0, 20.0, 5.0, 0.4}},   // dated id -> sol tier, not bare 5.6
		{"gpt-5.5-pro-2026-01-01", Rates{30.0, 180.0, 0.0, 0.0}}, // dated pro -> pro tier, not family
		{"gpt-5-pro-2026-01-01", Rates{15.0, 120.0, 0.0, 0.0}},   // dated pro of a family with no bare tier
		{"gpt-6-preview", Rates{0, 0, 0, 0}},                     // unknown future model stays $0
		{"totally-unknown-model", Rates{0, 0, 0, 0}},             // unknown -> $0, not Opus
		{"<synthetic>", Rates{0, 0, 0, 0}},                       // local, no real bill
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

func TestCacheWriteCost(t *testing.T) {
	for _, c := range []struct {
		name, model string
		counts      []float64
		want        float64
	}{
		{"fable mixed TTL", "claude-fable-5-1", []float64{1e6, 1e6, 2e6, 1e6, 1e6}, 92.75},
		{"fable legacy TTL", "claude-fable-5-1", []float64{1e6, 1e6, 2e6, 1e6}, 85.25},
		{"one-hour subset clamped", "claude-fable-5-1", []float64{0, 0, 1e6, 0, 2e6}, 20},
		{"negative subset ignored", "claude-fable-5-1", []float64{0, 0, 1e6, 0, -1e6}, 12.5},
		{"astra cache writes", "gpt-6-astra", []float64{1e6, 1e6, 1e6, 1e6}, 73.5},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := CostUSD(map[string][]float64{c.model: c.counts}); got != c.want {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestDashboardCostParity(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	source, err := os.ReadFile(filepath.Join("..", "..", "assets", "dashboard.js"))
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(source), "let PRICE=")
	end := strings.Index(string(source), "function tokTotal(")
	payload, _ := json.Marshal(APIPayload())
	script := string(source[start:end]) + "\nconst p=" + string(payload) + ";PRICE=p.models;PTIERS=p.tiers;PDEF=p.default;\n"
	for _, model := range []string{"claude-fable-5-1", "claude-sonnet-5", "gpt-6-astra", "unknown"} {
		for _, oneHour := range []float64{0, 1e6, 3e6, -1e6} {
			row, _ := json.Marshal(map[string]any{"model": model, "in": 1e6, "out": 1e6, "cc": 2e6, "cr": 1e6, "cc1h": oneHour})
			want := TokenCostUSD(model, 1e6, 1e6, 2e6, 1e6, oneHour)
			script += fmt.Sprintf("if(Math.abs(tokCost(%s)-%g)>1e-9)throw Error('cost mismatch');\n", row, want)
		}
	}
	if out, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
}
