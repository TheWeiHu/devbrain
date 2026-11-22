// Package pricing is the Go port of scripts/model_pricing.py — the single
// source of truth for turning token counts into dollars. Served to the
// dashboard by the queue server at /api/pricing (pinned by
// testdata/golden/api/pricing.json) and used by the dashboard for the Σ-cost
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

// Models uses standard short-context USD rates, checked 2026-09-11 (the
// Claude 5.2/5.5 rows and the fast-mode table, 2026-11-21).
// Sources: https://developers.openai.com/api/docs/pricing
// https://platform.claude.com/docs/en/about-claude/pricing
var Models = map[string]Rates{
	"claude-fable-5-2":  {8.0, 40.0, 10.0, 0.2},
	"claude-mythos-5-2": {8.0, 40.0, 10.0, 0.2},
	"claude-opus-5-5":   {4.0, 20.0, 5.0, 0.2},
	"claude-sonnet-5-5": {2.0, 10.0, 2.5, 0.1},
	"claude-fable-5-1":  {10.0, 50.0, 12.5, 0.25},
	"claude-mythos-5-1": {10.0, 50.0, 12.5, 0.25},
	"claude-mythos-5":   {10.0, 50.0, 12.5, 1.0},
	"claude-opus-5":     {5.0, 25.0, 6.25, 0.5},
	"claude-sonnet-5":   {2.0, 10.0, 2.5, 0.2},
	"claude-fable-5":    {10.0, 50.0, 12.5, 1.0},
	"claude-opus-4-8":   {5.0, 25.0, 6.25, 0.5},
	"claude-opus-4-7":   {5.0, 25.0, 6.25, 0.5},
	"claude-opus-4-6":   {5.0, 25.0, 6.25, 0.5},
	"claude-opus-4-5":   {5.0, 25.0, 6.25, 0.5},
	"claude-sonnet-4-6": {3.0, 15.0, 3.75, 0.3},
	"claude-sonnet-4-5": {3.0, 15.0, 3.75, 0.3},
	"claude-haiku-4-5":  {1.0, 5.0, 1.25, 0.1},
	// GPT-5.6 and GPT-6 report billable cache writes separately.
	"gpt-6-astra":   {10.0, 50.0, 12.5, 1.0},
	"gpt-5.6":       {4.0, 20.0, 5.0, 0.4}, // official alias for Sol
	"gpt-5.6-sol":   {4.0, 20.0, 5.0, 0.4},
	"gpt-5.6-terra": {2.0, 12.0, 2.5, 0.2},
	"gpt-5.6-luna":  {0.2, 1.2, 0.25, 0.02},
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
	{"claude-fable-5-2", Rates{8.0, 40.0, 10.0, 0.2}},
	{"claude-mythos-5-2", Rates{8.0, 40.0, 10.0, 0.2}},
	{"claude-opus-5-5", Rates{4.0, 20.0, 5.0, 0.2}},
	{"claude-sonnet-5-5", Rates{2.0, 10.0, 2.5, 0.1}}, // before claude-sonnet-5, which it contains
	{"claude-fable-5-1", Rates{10.0, 50.0, 12.5, 0.25}},
	{"claude-mythos-5-1", Rates{10.0, 50.0, 12.5, 0.25}},
	{"claude-mythos-5", Rates{10.0, 50.0, 12.5, 1.0}},
	{"claude-sonnet-5", Rates{2.0, 10.0, 2.5, 0.2}},
	{"gpt-6-astra", Rates{10.0, 50.0, 12.5, 1.0}},
	{"haiku", Rates{1.0, 5.0, 1.25, 0.1}},
	{"sonnet", Rates{3.0, 15.0, 3.75, 0.3}},
	{"fable", Rates{10.0, 50.0, 12.5, 1.0}},
	{"opus", Rates{5.0, 25.0, 6.25, 0.5}},
	// Most specific first: a dated/suffixed id must hit its exact variant
	// (pro/mini/nano) before the broader family row. No bare "gpt-5"/"gpt"
	// tier — unknown future models stay at $0.
	{"gpt-5.6-sol", Rates{4.0, 20.0, 5.0, 0.4}},
	{"gpt-5.6-terra", Rates{2.0, 12.0, 2.5, 0.2}},
	{"gpt-5.6-luna", Rates{0.2, 1.2, 0.25, 0.02}},
	{"gpt-5.6", Rates{4.0, 20.0, 5.0, 0.4}},
	{"gpt-5.5-pro", Rates{30.0, 180.0, 0.0, 0.0}},
	{"gpt-5.5", Rates{5.0, 30.0, 0.0, 0.5}},
	{"gpt-5.4-pro", Rates{30.0, 180.0, 0.0, 0.0}},
	{"gpt-5.4-mini", Rates{0.75, 4.5, 0.0, 0.075}},
	{"gpt-5.4-nano", Rates{0.2, 1.25, 0.0, 0.02}},
	{"gpt-5.4", Rates{2.5, 15.0, 0.0, 0.25}},
	{"gpt-5.3", Rates{1.75, 14.0, 0.0, 0.175}},
	{"gpt-5.2-pro", Rates{21.0, 168.0, 0.0, 0.0}},
	{"gpt-5.2", Rates{1.75, 14.0, 0.0, 0.175}},
	{"gpt-5.1", Rates{1.25, 10.0, 0.0, 0.125}},
	{"gpt-5-codex", Rates{1.25, 10.0, 0.0, 0.125}},
	{"gpt-5-pro", Rates{15.0, 120.0, 0.0, 0.0}},
	{"gpt-5-mini", Rates{0.25, 2.0, 0.0, 0.025}},
	{"gpt-5-nano", Rates{0.05, 0.4, 0.0, 0.005}},
}

// Default is the fallback for unknown and non-billable models (e.g.
// <synthetic>, which carries no real API bill): zero rates, so synthetic/local
// turns and unrecognized ids don't over-count at real model prices.
var Default = Rates{0, 0, 0, 0}

// FastTier is one model-family substring and the multiplier fast mode puts
// on every one of its rates (input, output, cache writes and cache reads:
// the caching multipliers stack on top of the fast price).
type FastTier struct {
	Substr     string
	Multiplier float64
}

// FastTiers lists the models that have a fast-mode price, checked in order.
// A model that is not listed has no fast price, so its fast tokens cost the
// standard rate.
var FastTiers = []FastTier{
	{"claude-fable-5-2", 2.0},
	{"claude-mythos-5-2", 2.0},
	{"claude-opus-5-5", 2.0},
	{"claude-opus-5", 2.0},
	{"claude-opus-4-8", 2.0},
}

// FastMultiplier returns what fast mode multiplies a model's rates by (1 for
// a model with no fast price).
func FastMultiplier(model string) float64 {
	for _, t := range FastTiers {
		if tierMatch(model, t.Substr) {
			return t.Multiplier
		}
	}
	return 1
}

// BillingRates returns the full per-1M-token rates for a model id, with tier
// fallback (billing_rates).
func BillingRates(model string) Rates {
	if r, ok := Models[model]; ok {
		return r
	}
	for _, t := range Tiers {
		if tierMatch(model, t.Substr) {
			return t.Rates
		}
	}
	return Default
}

// tierMatch reports whether model contains substr as a whole version: the
// match may not run straight into another digit. Without that, the dated
// Fable 5 id "claude-fable-5-20260601" would count as "claude-fable-5-2".
func tierMatch(model, substr string) bool {
	for from := 0; ; {
		i := strings.Index(model[from:], substr)
		if i < 0 {
			return false
		}
		end := from + i + len(substr)
		if end == len(model) || model[end] < '0' || model[end] > '9' {
			return true
		}
		from += i + 1
	}
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
	fast := make([]any, 0, len(FastTiers))
	for _, t := range FastTiers {
		fast = append(fast, []any{t.Substr, pyFloat(t.Multiplier)})
	}
	return map[string]any{"models": models, "tiers": tiers, "default": pyRates(Default), "fast": fast}
}

// pyRates renders a rate row the way Python json.dumps renders floats
// (float repr keeps a trailing .0 on integral values).
func pyRates(r Rates) []json.Number {
	out := make([]json.Number, len(r))
	for i, f := range r {
		out[i] = pyFloat(f)
	}
	return out
}

func pyFloat(f float64) json.Number {
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return json.Number(s)
}

// Rate returns (input_rate, output_rate) for backwards-compatible callers.
func Rate(model string) (float64, float64) {
	r := BillingRates(model)
	return r[0], r[1]
}

// TokenCostUSD estimates standard-rate cost. cacheCreate1h is a subset of
// cacheCreate; Claude's one-hour writes cost 2x input instead of 1.25x.
func TokenCostUSD(model string, input, output, cacheCreate, cacheRead, cacheCreate1h float64) float64 {
	r := BillingRates(model)
	oneHour := math.Max(0, math.Min(cacheCreate, cacheCreate1h))
	return (input*r[0] + output*r[1] + cacheCreate*r[2] + cacheRead*r[3] + oneHour*r[2]*0.6) / 1e6
}

// FastSurchargeUSD is what fast mode adds on top of TokenCostUSD. fast holds
// the turn's [input, output, cache_create, cache_read, cache_create_1h]
// tokens that were billed in fast mode: subsets of the counts TokenCostUSD
// already prices at the standard rate.
func FastSurchargeUSD(model string, fast [5]float64) float64 {
	extra := FastMultiplier(model) - 1
	if extra == 0 {
		return 0
	}
	return extra * TokenCostUSD(model, fast[0], fast[1], fast[2], fast[3], fast[4])
}

// CostUSD sums [input, output, cache_create, cache_read, cache_create_1h]
// counts and rounds to four decimals. Older two/four-column rows still work.
func CostUSD(tokensByModel map[string][]float64) float64 {
	total := 0.0
	for model, counts := range tokensByModel {
		get := func(i int) float64 {
			if i < len(counts) {
				return counts[i]
			}
			return 0
		}
		total += TokenCostUSD(model, get(0), get(1), get(2), get(3), get(4))
	}
	return math.RoundToEven(total*1e4) / 1e4 // Python round() is half-to-even
}
