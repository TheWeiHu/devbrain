---
name: brain-retro
description: |
  Generate the monthly retro page: fill the /journal day cache for any missing
  days, then run the deterministic `devbrain retro` generator — stats, period
  charts, and the full day-by-day journal in the user's approved GitHub-dark
  style, written to $DEVBRAIN_DATA/retro/<date>.html and opened in the browser.
  Named brain-retro to avoid gstack's /retro. Use when asked for a "retro",
  "monthly report", "month in review", or "how was my month".
---

# /brain-retro — fill the journal cache, then run the generator

The retro page itself is **deterministic code**, not model output: `devbrain retro`
(`internal/retro`, template embedded in the binary) computes every number, chart, and
pixel from the data files, so the design cannot drift between generations. The model's
only job here is the one thing code can't do — writing the journal prose — and that
lives in the `/journal` day cache, which this skill tops up before generating.

### 1. Fill the journal day cache for the window
Default 30 days (`/brain-retro 60` overrides). The generator reads
`$DATA/journal/<YYYY-MM-DD>.md`; days without a cache file are omitted from the day
cards entirely, so first run
the `/journal` skill's protocol (installed alongside this skill) for the window — it
reuses every already-cached day and renders + caches only the missing ones. Do NOT
re-render cached days and do NOT re-implement log parsing here.

### 2. Generate
```bash
days="$(printf '%s' "${1:-30}" | grep -oE '[0-9]+' | head -1)"; days="${days:-30}"
devbrain retro --days "$days"     # writes $DATA/retro/<today>.html and opens it
```
Everything else — window math, spend via the pricing table, tasks opened/shipped,
gbrain hit rate, the charts, the day cards, the rule-based suggestions, the approved
GitHub-dark style — is inside the generator. If the page needs a design change, change
`internal/retro/template.html` in the repo (a reviewed code change), never by
hand-writing HTML in a session.

### 3. Report
Report the file path and the page's Suggestions in chat. Reports live in
`$DATA/retro/` at the data-repo top level — deliberately outside any `projects/<p>/`
folder, because a retro spans all projects. Regenerating is cheap and overwrites the
same day's file; never edit a past day's report in place.
