#!/usr/bin/env python3
"""Claude model token pricing — the single source of truth for turning token counts
into dollars. Imported by nightshift-status.py for the Σ-cost headline. The dashboard's
Profile card carries a parallel copy in JS (scripts/dashboard.html, the `PRICE` table);
keep the two in sync. Rates are USD per 1M tokens, as (input_rate, output_rate)."""

# USD per 1M tokens: (input_rate, output_rate).
MODEL_PRICING = {
    "claude-fable-5":    (10.0, 50.0),
    "claude-opus-4-8":   (5.0, 25.0),
    "claude-opus-4-7":   (5.0, 25.0),
    "claude-opus-4-6":   (5.0, 25.0),
    "claude-opus-4-5":   (5.0, 25.0),
    "claude-sonnet-4-6": (3.0, 15.0),
    "claude-sonnet-4-5": (3.0, 15.0),
    "claude-haiku-4-5":  (1.0, 5.0),
}

# Fallback rates by model-family substring, for ids not in MODEL_PRICING.
TIER_PRICING = (
    ("haiku",  (1.0, 5.0)),
    ("sonnet", (3.0, 15.0)),
    ("fable",  (10.0, 50.0)),
    ("opus",   (5.0, 25.0)),
)
DEFAULT_PRICING = (5.0, 25.0)            # unknown model → Opus rates

# Cache is billed relative to the model's base input rate.
CACHE_WRITE_MULTIPLIER = 1.25
CACHE_READ_MULTIPLIER = 0.10


def rate(model):
    """(input_rate, output_rate) per 1M tokens for a model id, with tier fallback."""
    if model in MODEL_PRICING:
        return MODEL_PRICING[model]
    for tier, tier_rate in TIER_PRICING:
        if tier in (model or ""):
            return tier_rate
    return DEFAULT_PRICING


def cost_usd(tokens_by_model):
    """True billed $ across {model: [input, output, cache_create, cache_read]} —
    output + input + cache (write 1.25× input, read 0.1× input). Tolerates legacy
    2-element rows (input/output only)."""
    total = 0.0
    for model, counts in tokens_by_model.items():
        input_rate, output_rate = rate(model)
        input_tokens = counts[0]
        output_tokens = counts[1]
        cache_create = counts[2] if len(counts) > 2 else 0
        cache_read = counts[3] if len(counts) > 3 else 0
        total += (input_tokens / 1e6 * input_rate
                  + output_tokens / 1e6 * output_rate
                  + cache_create / 1e6 * input_rate * CACHE_WRITE_MULTIPLIER
                  + cache_read / 1e6 * input_rate * CACHE_READ_MULTIPLIER)
    return round(total, 4)
