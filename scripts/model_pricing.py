#!/usr/bin/env python3
"""Model token pricing — the single source of truth for turning token counts into
dollars. Imported by nightshift-status.py for the Sigma-cost headline, and served to the
dashboard by queue.py at /api/pricing (the Profile card's cost view builds its `PRICE`
table from that response, so there is no second copy in JS to drift). Rates are USD per
1M tokens, as:

    (input_rate, output_rate, cache_create_rate, cache_read_rate)
"""

# USD per 1M tokens: (input_rate, output_rate, cache_create_rate, cache_read_rate).
MODEL_PRICING = {
    "claude-fable-5":    (10.0, 50.0, 12.5, 1.0),
    "claude-opus-4-8":   (5.0, 25.0, 6.25, 0.5),
    "claude-opus-4-7":   (5.0, 25.0, 6.25, 0.5),
    "claude-opus-4-6":   (5.0, 25.0, 6.25, 0.5),
    "claude-opus-4-5":   (5.0, 25.0, 6.25, 0.5),
    "claude-sonnet-4-6": (3.0, 15.0, 3.75, 0.3),
    "claude-sonnet-4-5": (3.0, 15.0, 3.75, 0.3),
    "claude-haiku-4-5":  (1.0, 5.0, 1.25, 0.1),
    "gpt-5.5":           (5.0, 30.0, 5.0, 0.5),
    "gpt-5.4":           (2.5, 15.0, 2.5, 0.25),
    "gpt-5.4-mini":      (0.75, 4.5, 0.75, 0.075),
    "gpt-5.4-nano":      (0.2, 1.25, 0.2, 0.02),
    "gpt-5.3-codex":     (1.75, 14.0, 1.75, 0.175),
}

# Fallback rates by model-family substring, for ids not in MODEL_PRICING.
TIER_PRICING = (
    ("haiku",  (1.0, 5.0, 1.25, 0.1)),
    ("sonnet", (3.0, 15.0, 3.75, 0.3)),
    ("fable",  (10.0, 50.0, 12.5, 1.0)),
    ("opus",   (5.0, 25.0, 6.25, 0.5)),
    ("gpt-5.5", (5.0, 30.0, 5.0, 0.5)),
    ("gpt-5.4", (2.5, 15.0, 2.5, 0.25)),
)
DEFAULT_PRICING = (5.0, 25.0, 6.25, 0.5)  # unknown model -> Opus rates

# Claude cache is billed relative to the model's base input rate.
CACHE_WRITE_MULTIPLIER = 1.25
CACHE_READ_MULTIPLIER = 0.10


def billing_rates(model):
    """Full per-1M-token rates for a model id, with tier fallback."""
    if model in MODEL_PRICING:
        return MODEL_PRICING[model]
    for tier, tier_rate in TIER_PRICING:
        if tier in (model or ""):
            return tier_rate
    return DEFAULT_PRICING


def rate(model):
    """(input_rate, output_rate) per 1M tokens for backwards-compatible callers."""
    input_rate, output_rate, _cache_create_rate, _cache_read_rate = billing_rates(model)
    return input_rate, output_rate


def cost_usd(tokens_by_model):
    """True billed $ across {model: [input, output, cache_create, cache_read]} —
    output + input + cache. Tolerates legacy 2-element rows (input/output only)."""
    total = 0.0
    for model, counts in tokens_by_model.items():
        input_rate, output_rate, cache_create_rate, cache_read_rate = billing_rates(model)
        input_tokens = counts[0]
        output_tokens = counts[1]
        cache_create = counts[2] if len(counts) > 2 else 0
        cache_read = counts[3] if len(counts) > 3 else 0
        total += (input_tokens / 1e6 * input_rate
                  + output_tokens / 1e6 * output_rate
                  + cache_create / 1e6 * cache_create_rate
                  + cache_read / 1e6 * cache_read_rate)
    return round(total, 4)
