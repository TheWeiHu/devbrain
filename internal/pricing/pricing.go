// Package pricing is the Go port of scripts/model_pricing.py — the single
// source of truth for turning token counts into dollars. Served to the
// dashboard by the queue server at /api/pricing (pinned by
// testdata/golden/api/pricing.json) and used by nightshift for the Σ-cost
// headline. Rates are USD per 1M tokens.
package pricing

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

// Rates is (input_rate, output_rate, cache_create_rate, cache_read_rate).
type Rates [4]float64

// Models maps exact model ids to their rates (MODEL_PRICING).
var Models = map[string]Rates{
	"claude-fable-5":    {10.0, 50.0, 12.5, 1.0},
	"claude-opus-4-8":   {5.0, 25.0, 6.25, 0.5},
	"claude-opus-4-7":   {5.0, 25.0, 6.25, 0.5},
	"claude-opus-4-6":   {5.0, 25.0, 6.25, 0.5},
	"claude-opus-4-5":   {5.0, 25.0, 6.25, 0.5},
	"claude-sonnet-4-6": {3.0, 15.0, 3.75, 0.3},
	"claude-sonnet-4-5": {3.0, 15.0, 3.75, 0.3},
	"claude-haiku-4-5":  {1.0, 5.0, 1.25, 0.1},
	// OpenAI never bills cache writes: cache_create is always 0.
	"gpt-5.6":       {5.0, 30.0, 0.0, 0.5}, // official alias for Sol
	"gpt-5.6-sol":   {5.0, 30.0, 0.0, 0.5},
	"gpt-5.6-terra": {2.5, 15.0, 0.0, 0.25},
	"gpt-5.6-luna":  {1.0, 6.0, 0.0, 0.1},
	"gpt-5.5":       {5.0, 30.0, 0.0, 0.5},
	"gpt-5.5-pro":   {30.0, 180.0, 0.0, 0.0},
	"gpt-5.4":       {2.5, 15.0, 0.0, 0.25},
	"gpt-5.4-pro":   {30.0, 180.0, 0.0, 0.0},
	"gpt-5.4-mini":  {0.75, 4.5, 0.0, 0.075},
	"gpt-5.4-nano":  {0.2, 1.25, 0.0, 0.02},
	"gpt-5.3-codex": {1.75, 14.0, 0.0, 0.175},
	"gpt-5.2":       {1.75, 14.0, 0.0, 0.175},
	"gpt-5.2-codex": {1.75, 14.0, 0.0, 0.175},
	"gpt-5.2-pro":   {21.0, 168.0, 0.0, 0.0},
	"gpt-5.1":       {1.25, 10.0, 0.0, 0.125},
	"gpt-5.1-codex": {1.25, 10.0, 0.0, 0.125},
	"gpt-5":         {1.25, 10.0, 0.0, 0.125},
	"gpt-5-codex":   {1.25, 10.0, 0.0, 0.125},
	"gpt-5-mini":    {0.25, 2.0, 0.0, 0.025},
	"gpt-5-nano":    {0.05, 0.4, 0.0, 0.005},
	"gpt-5-pro":     {15.0, 120.0, 0.0, 0.0},
}

// Tier is one model-family substring fallback (TIER_PRICING row).
type Tier struct {
	Substr string
	Rates  Rates
}

// Tiers are the fallback rates by model-family substring, checked in order.
var Tiers = []Tier{
	{"haiku", Rates{1.0, 5.0, 1.25, 0.1}},
	{"sonnet", Rates{3.0, 15.0, 3.75, 0.3}},
	{"fable", Rates{10.0, 50.0, 12.5, 1.0}},
	{"opus", Rates{5.0, 25.0, 6.25, 0.5}},
	// Most specific first: dated/variant ids hit their family, not a shorter
	// prefix. No bare "gpt-5"/"gpt" tier — unknown future models stay at $0.
	{"gpt-5.6-sol", Rates{5.0, 30.0, 0.0, 0.5}},
	{"gpt-5.6-terra", Rates{2.5, 15.0, 0.0, 0.25}},
	{"gpt-5.6-luna", Rates{1.0, 6.0, 0.0, 0.1}},
	{"gpt-5.6", Rates{5.0, 30.0, 0.0, 0.5}},
	{"gpt-5.5", Rates{5.0, 30.0, 0.0, 0.5}},
	{"gpt-5.4", Rates{2.5, 15.0, 0.0, 0.25}},
	{"gpt-5.3", Rates{1.75, 14.0, 0.0, 0.175}},
	{"gpt-5.2", Rates{1.75, 14.0, 0.0, 0.175}},
	{"gpt-5.1", Rates{1.25, 10.0, 0.0, 0.125}},
	{"gpt-5-codex", Rates{1.25, 10.0, 0.0, 0.125}},
}

// Default is the fallback for unknown and non-billable models (e.g.
// <synthetic>, which carries no real API bill): zero rates, so synthetic/local
// turns and unrecognized ids don't over-count at real model prices.
var Default = Rates{0, 0, 0, 0}

// BillingRates returns the full per-1M-token rates for a model id, with tier
// fallback (billing_rates).
func BillingRates(model string) Rates {
	if r, ok := Models[model]; ok {
		return r
	}
	for _, t := range Tiers {
		if strings.Contains(model, t.Substr) {
			return t.Rates
		}
	}
	return Default
}

// APIPayload is the /api/pricing response body: the one pricing table the
// dashboard fetches instead of carrying a JS copy that drifts. Rates are
// emitted as Python-repr numbers (5.0, not 5) to stay byte-compatible with
// the legacy json.dumps output.
func APIPayload() map[string]any {
	models := make(map[string]any, len(Models))
	for m, r := range Models {
		models[m] = pyRates(r)
	}
	tiers := make([]any, 0, len(Tiers))
	for _, t := range Tiers {
		tiers = append(tiers, []any{t.Substr, pyRates(t.Rates)})
	}
	return map[string]any{"models": models, "tiers": tiers, "default": pyRates(Default)}
}

// pyRates renders a rate row the way Python json.dumps renders floats
// (float repr keeps a trailing .0 on integral values).
func pyRates(r Rates) []json.Number {
	out := make([]json.Number, len(r))
	for i, f := range r {
		s := strconv.FormatFloat(f, 'g', -1, 64)
		if !strings.ContainsAny(s, ".eE") {
			s += ".0"
		}
		out[i] = json.Number(s)
	}
	return out
}

// Rate returns (input_rate, output_rate) for backwards-compatible callers.
func Rate(model string) (float64, float64) {
	r := BillingRates(model)
	return r[0], r[1]
}

// CostUSD is the true billed $ across {model: [input, output, cache_create,
// cache_read]} counts, rounded like Python round(total, 4). Legacy 2-element
// rows (input/output only) are tolerated.
func CostUSD(tokensByModel map[string][]float64) float64 {
	total := 0.0
	for model, counts := range tokensByModel {
		r := BillingRates(model)
		get := func(i int) float64 {
			if i < len(counts) {
				return counts[i]
			}
			return 0
		}
		total += get(0)/1e6*r[0] + get(1)/1e6*r[1] + get(2)/1e6*r[2] + get(3)/1e6*r[3]
	}
	return math.RoundToEven(total*1e4) / 1e4 // Python round() is half-to-even
}
