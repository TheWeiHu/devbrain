// Package pricing is devbrain's source of truth for standard-API USD
// equivalents and published Codex credit rates. It is served at /api/pricing
// and used by Nightshift. API dollars, Codex credits, and subscription charges
// remain separate accounting bases.
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

// Rates preserves the original first five columns and appends long-context
// multipliers: (input, output, cache-write-5m, cache-read, cache-write-1h,
// long-input-multiplier, long-output-multiplier). Existing API consumers that
// read the original columns remain compatible.
type Rates [7]float64

func standardRates(input, output, cacheWrite, cacheRead, cacheWrite1H float64) Rates {
	return Rates{input, output, cacheWrite, cacheRead, cacheWrite1H, 1, 1}
}

func longContextRates(input, output, cacheWrite, cacheRead, cacheWrite1H float64) Rates {
	return Rates{input, output, cacheWrite, cacheRead, cacheWrite1H, 2, 1.5}
}

// Models maps exact model ids to rates verified against the official OpenAI
// and Anthropic standard API pricing pages on AsOf.
var Models = map[string]Rates{
	"claude-fable-5":    standardRates(10, 50, 12.5, 1, 20),
	"claude-mythos-5":   standardRates(10, 50, 12.5, 1, 20),
	"claude-sonnet-5":   standardRates(2, 10, 2.5, 0.2, 4), // introductory through 2026-08-31
	"claude-opus-4-8":   standardRates(5, 25, 6.25, 0.5, 10),
	"claude-opus-4-7":   standardRates(5, 25, 6.25, 0.5, 10),
	"claude-opus-4-6":   standardRates(5, 25, 6.25, 0.5, 10),
	"claude-opus-4-5":   standardRates(5, 25, 6.25, 0.5, 10),
	"claude-sonnet-4-6": standardRates(3, 15, 3.75, 0.3, 6),
	"claude-sonnet-4-5": standardRates(3, 15, 3.75, 0.3, 6),
	"claude-haiku-4-5":  standardRates(1, 5, 1.25, 0.1, 2),

	"gpt-5.6":       longContextRates(5, 30, 6.25, 0.5, 6.25), // official alias -> Sol
	"gpt-5.6-sol":   longContextRates(5, 30, 6.25, 0.5, 6.25),
	"gpt-5.6-terra": longContextRates(2.5, 15, 3.125, 0.25, 3.125),
	"gpt-5.6-luna":  longContextRates(1, 6, 1.25, 0.1, 1.25),
	"gpt-5.5-pro":   longContextRates(30, 180, 0, 0, 0),
	"gpt-5.5":       longContextRates(5, 30, 0, 0.5, 0),
	"gpt-5.4-pro":   longContextRates(30, 180, 0, 0, 0),
	"gpt-5.4-mini":  standardRates(0.75, 4.5, 0, 0.075, 0),
	"gpt-5.4-nano":  standardRates(0.2, 1.25, 0, 0.02, 0),
	"gpt-5.4":       longContextRates(2.5, 15, 0, 0.25, 0),
	"gpt-5.3-codex": standardRates(1.75, 14, 0, 0.175, 0),
	"gpt-5.2-pro":   standardRates(21, 168, 0, 0, 0),
	"gpt-5.2-codex": standardRates(1.75, 14, 0, 0.175, 0),
	"gpt-5.2":       standardRates(1.75, 14, 0, 0.175, 0),
	"gpt-5.1-codex": standardRates(1.25, 10, 0, 0.125, 0),
	"gpt-5.1":       standardRates(1.25, 10, 0, 0.125, 0),
	"gpt-5-mini":    standardRates(0.25, 2, 0, 0.025, 0),
	"gpt-5-nano":    standardRates(0.05, 0.4, 0, 0.005, 0),
	"gpt-5-pro":     standardRates(15, 120, 0, 0, 0),
	"gpt-5":         standardRates(1.25, 10, 0, 0.125, 0),

	"gpt-4.1-mini":      standardRates(0.4, 1.6, 0, 0.1, 0),
	"gpt-4.1-nano":      standardRates(0.1, 0.4, 0, 0.025, 0),
	"gpt-4.1":           standardRates(2, 8, 0, 0.5, 0),
	"gpt-4o-2024-05-13": standardRates(5, 15, 0, 0, 0),
	"gpt-4o-mini":       standardRates(0.15, 0.6, 0, 0.075, 0),
	"gpt-4o":            standardRates(2.5, 10, 0, 1.25, 0),
	"o1-pro":            standardRates(150, 600, 0, 0, 0),
	"o1-mini":           standardRates(1.1, 4.4, 0, 0.55, 0),
	"o1":                standardRates(15, 60, 0, 7.5, 0),
	"o3-pro":            standardRates(20, 80, 0, 0, 0),
	"o3-mini":           standardRates(1.1, 4.4, 0, 0.55, 0),
	"o3":                standardRates(2, 8, 0, 0.5, 0),
	"o4-mini":           standardRates(1.1, 4.4, 0, 0.275, 0),
}

// Tier is one model-family prefix fallback for dated snapshots and aliases.
type Tier struct {
	Prefix string
}

// Tiers are fallback rates for dated model snapshots, checked in order.
var Tiers = []Tier{
	{"claude-haiku-4-5"},
	{"claude-sonnet-4-6"},
	{"claude-sonnet-4-5"},
	{"claude-fable-5"},
	{"claude-mythos-5"},
	{"claude-sonnet-5"},
	{"claude-opus-4-8"},
	{"claude-opus-4-7"},
	{"claude-opus-4-6"},
	{"claude-opus-4-5"},
	{"gpt-5.6-sol"},
	{"gpt-5.6-terra"},
	{"gpt-5.6-luna"},
	{"gpt-5.5-pro"},
	{"gpt-5.5"},
	{"gpt-5.4-pro"},
	{"gpt-5.4-mini"},
	{"gpt-5.4-nano"},
	{"gpt-5.4"},
	{"gpt-5.3-codex"},
	{"gpt-5.2-pro"},
	{"gpt-5.2-codex"},
	{"gpt-5.2"},
	{"gpt-5.1-codex"},
	{"gpt-5.1"},
	{"gpt-5-mini"},
	{"gpt-5-nano"},
	{"gpt-5-pro"},
	{"gpt-5"},
	{"gpt-4.1-mini"},
	{"gpt-4.1-nano"},
	{"gpt-4.1"},
	{"gpt-4o-mini"},
	{"gpt-4o"},
	{"o1-pro"},
	{"o1-mini"},
	{"o1"},
	{"o3-pro"},
	{"o3-mini"},
	{"o3"},
	{"o4-mini"},
}

// Default is the fallback for unknown and non-billable models (e.g.
// <synthetic>, which carries no real API bill): zero rates, so synthetic/local
// turns and unrecognized ids don't over-count at real model prices.
var Default = standardRates(0, 0, 0, 0, 0)

// KnownUnpriced lists current Codex model ids that intentionally have no
// published standard API or credit rate. Keeping them explicit distinguishes
// a research preview from an accidentally stale pricing table.
var KnownUnpriced = map[string]string{
	"gpt-5.3-codex-spark": "research preview; no published API or credit rate",
}

// CreditRates is the published standard-speed Codex rate card in credits per
// 1M (input, cached input, output) tokens. It is separate from API USD rates:
// included plan usage, purchased credits, and API invoices are different
// accounting systems.
type CreditRates [3]float64

var CreditModels = map[string]CreditRates{
	"gpt-5.6":       {125, 12.5, 750}, // official alias -> Sol
	"gpt-5.6-sol":   {125, 12.5, 750},
	"gpt-5.6-terra": {62.5, 6.25, 375},
	"gpt-5.6-luna":  {25, 2.5, 150},
	"gpt-5.5":       {125, 12.5, 750},
	"gpt-5.4":       {62.5, 6.25, 375},
	"gpt-5.4-mini":  {18.75, 1.875, 113},
}

var CreditTiers = []string{
	"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna",
	"gpt-5.5", "gpt-5.4-mini", "gpt-5.4",
}

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
		if isDatedSnapshot(model, t.Prefix) {
			return Models[t.Prefix], true
		}
	}
	return Default, false
}

// HasLongContextPricing reports whether requests above the model's published
// context-price threshold need a per-request surcharge.
func HasLongContextPricing(model string) bool {
	r, ok := lookup(model)
	return ok && (r[5] != 1 || r[6] != 1)
}

// UnpricedReason explains a known model that intentionally lacks a numeric
// rate. Unknown model ids return no reason so callers can distinguish a stale
// catalog from a documented research preview.
func UnpricedReason(model string) (string, bool) {
	reason, ok := KnownUnpriced[model]
	return reason, ok
}

// CodexCreditRates returns the published standard-speed credit rates for a
// model. Models that are API-only or have non-numeric preview pricing return
// false instead of being guessed at another family's rate.
func CodexCreditRates(model string) (CreditRates, bool) {
	if r, ok := CreditModels[model]; ok {
		return r, true
	}
	for _, family := range CreditTiers {
		if isDatedSnapshot(model, family) {
			return CreditModels[family], true
		}
	}
	return CreditRates{}, false
}

func isDatedSnapshot(model, family string) bool {
	suffix := strings.TrimPrefix(model, family+"-")
	if suffix == model {
		return false
	}
	digits := 0
	for _, r := range suffix {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case r != '-':
			return false
		}
	}
	return digits >= 8
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
		tiers = append(tiers, []any{t.Prefix, pyRates(Models[t.Prefix])})
	}
	creditModels := make(map[string]any, len(CreditModels))
	for model, rates := range CreditModels {
		creditModels[model] = pyCreditRates(rates)
	}
	creditTiers := make([]any, 0, len(CreditTiers))
	for _, family := range CreditTiers {
		creditTiers = append(creditTiers, []any{family, pyCreditRates(CreditModels[family])})
	}
	return map[string]any{
		"basis": Basis, "as_of": AsOf,
		"models": models, "tiers": tiers, "default": pyRates(Default),
		"known_unpriced": KnownUnpriced,
		"codex_credits": map[string]any{
			"basis":  "standard-speed-credits-per-million",
			"models": creditModels, "tiers": creditTiers,
		},
	}
}

// pyRates renders a rate row the way Python json.dumps renders floats
// (float repr keeps a trailing .0 on integral values).
func pyRates(r Rates) []json.Number {
	return pyNumbers(r[:])
}

func pyCreditRates(r CreditRates) []json.Number {
	return pyNumbers(r[:])
}

func pyNumbers(r []float64) []json.Number {
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

// Usage is one model's token counts. Long-context fields are subsets of their
// corresponding totals, classified per request before turns are aggregated.
type Usage struct {
	Input, Output, CacheCreate, CacheRead, CacheCreate1H                     float64
	LongInput, LongOutput, LongCacheCreate, LongCacheRead, LongCacheCreate1H float64
}

// UsageCost computes one model's standard-API USD equivalent, including
// cache TTL and request-level long-context pricing.
func UsageCost(model string, usage Usage) float64 {
	r := BillingRates(model)
	clamp := func(value, total float64) float64 {
		return min(max(value, 0), max(total, 0))
	}
	usage.CacheCreate1H = clamp(usage.CacheCreate1H, usage.CacheCreate)
	usage.LongInput = clamp(usage.LongInput, usage.Input)
	usage.LongOutput = clamp(usage.LongOutput, usage.Output)
	usage.LongCacheCreate = clamp(usage.LongCacheCreate, usage.CacheCreate)
	usage.LongCacheRead = clamp(usage.LongCacheRead, usage.CacheRead)
	usage.LongCacheCreate1H = min(
		clamp(usage.LongCacheCreate1H, usage.LongCacheCreate), usage.CacheCreate1H,
	)

	base := usage.Input*r[0] + usage.Output*r[1] + usage.CacheCreate*r[2] +
		usage.CacheRead*r[3] + usage.CacheCreate1H*(r[4]-r[2])
	longInput := usage.LongInput*r[0] + usage.LongCacheCreate*r[2] +
		usage.LongCacheRead*r[3] + usage.LongCacheCreate1H*(r[4]-r[2])
	longOutput := usage.LongOutput * r[1]
	return (base + longInput*(r[5]-1) + longOutput*(r[6]-1)) / 1e6
}

// Cost is the backwards-compatible aggregate calculator. Without a
// request-level split it intentionally applies no long-context surcharge.
func Cost(model string, input, output, cacheCreate, cacheRead, cacheCreate1H float64) float64 {
	return UsageCost(model, Usage{
		Input: input, Output: output, CacheCreate: cacheCreate,
		CacheRead: cacheRead, CacheCreate1H: cacheCreate1H,
	})
}

// CreditCost computes the standard-speed Codex credit estimate. It returns
// false when the model has no published numeric credit rate.
func CreditCost(model string, input, output, cacheRead float64) (float64, bool) {
	rates, ok := CodexCreditRates(model)
	if !ok {
		return 0, false
	}
	return (input*rates[0] + cacheRead*rates[1] + output*rates[2]) / 1e6, true
}

// CostUSD returns the standard-API USD equivalent across
// {model: [input, output, cache_create, cache_read, cache_create_1h,
// long_input, long_output, long_cache_create, long_cache_read,
// long_cache_create_1h]} counts, rounded like Python round(total, 4). Legacy
// shorter rows are tolerated.
func CostUSD(tokensByModel map[string][]float64) float64 {
	total := 0.0
	for model, counts := range tokensByModel {
		get := func(i int) float64 {
			if i < len(counts) {
				return counts[i]
			}
			return 0
		}
		total += UsageCost(model, Usage{
			Input: get(0), Output: get(1), CacheCreate: get(2), CacheRead: get(3), CacheCreate1H: get(4),
			LongInput: get(5), LongOutput: get(6), LongCacheCreate: get(7),
			LongCacheRead: get(8), LongCacheCreate1H: get(9),
		})
	}
	return math.RoundToEven(total*1e4) / 1e4 // Python round() is half-to-even
}
