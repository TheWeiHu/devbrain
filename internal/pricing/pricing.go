// Package pricing is devbrain's single source of truth for standard-API USD
// equivalents. It is served at /api/pricing and used by Nightshift. Rates are
// USD per 1M tokens; subscription charges and Codex credits are intentionally
// outside this estimate.
package pricing

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

const (
	Basis = "standard-api-usd"
	AsOf  = "2026-07-10"
)

// Rates preserves the original first four columns and appends the 1-hour
// cache-write rate: (input, output, cache-write-5m, cache-read,
// cache-write-1h). Existing API consumers that read four columns remain
// compatible.
type Rates [5]float64

// Models maps exact model ids to rates verified against the official OpenAI
// and Anthropic standard API pricing pages on AsOf.
var Models = map[string]Rates{
	"claude-fable-5":    {10.0, 50.0, 12.5, 1.0, 20.0},
	"claude-opus-4-8":   {5.0, 25.0, 6.25, 0.5, 10.0},
	"claude-opus-4-7":   {5.0, 25.0, 6.25, 0.5, 10.0},
	"claude-opus-4-6":   {5.0, 25.0, 6.25, 0.5, 10.0},
	"claude-opus-4-5":   {5.0, 25.0, 6.25, 0.5, 10.0},
	"claude-sonnet-4-6": {3.0, 15.0, 3.75, 0.3, 6.0},
	"claude-sonnet-4-5": {3.0, 15.0, 3.75, 0.3, 6.0},
	"claude-haiku-4-5":  {1.0, 5.0, 1.25, 0.1, 2.0},
	"gpt-5.6-sol":       {5.0, 30.0, 6.25, 0.5, 6.25},
	"gpt-5.6-terra":     {2.5, 15.0, 3.125, 0.25, 3.125},
	"gpt-5.6-luna":      {1.0, 6.0, 1.25, 0.1, 1.25},
	"gpt-5.5":           {5.0, 30.0, 0.0, 0.5, 0.0},
	"gpt-5.4":           {2.5, 15.0, 0.0, 0.25, 0.0},
	"gpt-5.4-mini":      {0.75, 4.5, 0.0, 0.075, 0.0},
	"gpt-5.4-nano":      {0.2, 1.25, 0.0, 0.02, 0.0},
	"gpt-5.3-codex":     {1.75, 14.0, 0.0, 0.175, 0.0},
	"gpt-5.2-codex":     {1.75, 14.0, 0.0, 0.175, 0.0},
	"gpt-5.2":           {1.75, 14.0, 0.0, 0.175, 0.0},
	"gpt-5.1":           {1.25, 10.0, 0.0, 0.125, 0.0},
	"gpt-5":             {1.25, 10.0, 0.0, 0.125, 0.0},
}

// Tier is one model-family substring fallback (TIER_PRICING row).
type Tier struct {
	Substr string
	Rates  Rates
}

// Tiers are the fallback rates by model-family substring, checked in order.
var Tiers = []Tier{
	{"claude-haiku-4-5", Rates{1.0, 5.0, 1.25, 0.1, 2.0}},
	{"claude-sonnet-4-", Rates{3.0, 15.0, 3.75, 0.3, 6.0}},
	{"claude-fable-5", Rates{10.0, 50.0, 12.5, 1.0, 20.0}},
	{"claude-opus-4-", Rates{5.0, 25.0, 6.25, 0.5, 10.0}},
	{"gpt-5.6-sol", Rates{5.0, 30.0, 6.25, 0.5, 6.25}},
	{"gpt-5.6-terra", Rates{2.5, 15.0, 3.125, 0.25, 3.125}},
	{"gpt-5.6-luna", Rates{1.0, 6.0, 1.25, 0.1, 1.25}},
	{"gpt-5.5", Rates{5.0, 30.0, 0.0, 0.5, 0.0}},
	{"gpt-5.4-mini", Rates{0.75, 4.5, 0.0, 0.075, 0.0}},
	{"gpt-5.4-nano", Rates{0.2, 1.25, 0.0, 0.02, 0.0}},
	{"gpt-5.4", Rates{2.5, 15.0, 0.0, 0.25, 0.0}},
	{"gpt-5.3-codex", Rates{1.75, 14.0, 0.0, 0.175, 0.0}},
	{"gpt-5.2", Rates{1.75, 14.0, 0.0, 0.175, 0.0}},
	{"gpt-5.1", Rates{1.25, 10.0, 0.0, 0.125, 0.0}},
}

// Default is the fallback for unknown and non-billable models (e.g.
// <synthetic>, which carries no real API bill): zero rates, so synthetic/local
// turns and unrecognized ids don't over-count at real model prices.
var Default = Rates{0, 0, 0, 0, 0}

// BillingRates returns the full per-1M-token rates for a model id, with tier
// fallback (billing_rates).
func BillingRates(model string) Rates {
	rates, _ := lookup(model)
	return rates
}

// IsPriced reports whether a model matched an explicit or family rate. A zero
// default is deliberately not a price: callers should surface partial totals
// rather than silently presenting unknown models as free.
func IsPriced(model string) bool {
	_, ok := lookup(model)
	return ok
}

func lookup(model string) (Rates, bool) {
	if r, ok := Models[model]; ok {
		return r, true
	}
	for _, t := range Tiers {
		if strings.Contains(model, t.Substr) {
			return t.Rates, true
		}
	}
	return Default, false
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
	return map[string]any{
		"basis": Basis, "as_of": AsOf,
		"models": models, "tiers": tiers, "default": pyRates(Default),
	}
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

// Cost computes one model's standard-API USD equivalent. cacheCreate is the
// aggregate cache-write count; cacheCreate1H is the subset billed at the 1-hour
// rate. Legacy records pass zero for cacheCreate1H and retain the old 5-minute
// estimate.
func Cost(model string, input, output, cacheCreate, cacheRead, cacheCreate1H float64) float64 {
	r := BillingRates(model)
	cacheCreate1H = min(max(cacheCreate1H, 0), max(cacheCreate, 0))
	return (input*r[0] + output*r[1] + cacheCreate*r[2] + cacheRead*r[3] +
		cacheCreate1H*(r[4]-r[2])) / 1e6
}

// CostUSD returns the standard-API USD equivalent across
// {model: [input, output, cache_create, cache_read, cache_create_1h]} counts,
// rounded like Python round(total, 4). Legacy shorter rows are tolerated.
func CostUSD(tokensByModel map[string][]float64) float64 {
	total := 0.0
	for model, counts := range tokensByModel {
		get := func(i int) float64 {
			if i < len(counts) {
				return counts[i]
			}
			return 0
		}
		total += Cost(model, get(0), get(1), get(2), get(3), get(4))
	}
	return math.RoundToEven(total*1e4) / 1e4 // Python round() is half-to-even
}
