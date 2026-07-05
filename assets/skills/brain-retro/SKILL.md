---
name: brain-retro
description: |
  Generate a monthly retro: ONE self-contained dark-theme HTML page covering all
  projects — headline stats, spend/task/brain charts, the /journal narrative grouped
  by week, and a few data-grounded suggestions. Saved to $DEVBRAIN_DATA/retro/ and
  opened in the browser. Named brain-retro to avoid gstack's /retro. Use when asked
  for a "retro", "monthly report", "month in review", or "how was my month".
---

# /brain-retro — monthly HTML retro over the whole brain

A periodic composition over data that already exists — it parses nothing new and writes
only the report. Narrative comes from the `/journal` protocol; numbers come from the same
files the dashboard reads. Everything is a rebuildable projection of the log.

### 1. Window + inputs
Default the last 30 days (`/brain-retro 60` overrides), all projects.
```bash
DATA="${DEVBRAIN_DATA:-$HOME/devbrain-data}"
days="$(printf '%s' "${1:-30}" | grep -oE '[0-9]+' | head -1)"; days="${days:-30}"
SINCE="$(date -v-"${days}"d +%F 2>/dev/null || date -d "${days} days ago" +%F)"
OUT="$DATA/retro/$(date +%F).html"; mkdir -p "$DATA/retro"
```
Inputs, per `projects/<p>/` (skip what a project lacks — not every project has every file):
- **Narrative:** run the `/journal` skill's gather+render protocol (Steps 1–3 of
  `journal/SKILL.md`, installed alongside this skill) over the same window — do NOT
  re-implement log parsing here.
- **Spend:** `tokens.jsonl` — one JSON row per turn (`ts`, `model`, `in`/`out`/
  `cache_create`/`cache_read`). Bill with the dashboard's weights: `in·rate + out·rate +
  cache_create·1.25·in_rate + cache_read·0.1·in_rate`; `<synthetic>`/unknown models cost $0.
- **Queue:** `todo/*.md` frontmatter `created:` / `done_at:` dates in window, per project.
- **Brain reads:** `gbrain-queries.log` — JSONL rows with `hits`; hit rate = rows with
  `hits>0` over all rows in window.
- **Volume:** count `## HH:MM:SS` prompt headers and session files under
  `log/<YYYY-MM-DD>/` dirs in window.

### 2. Compute the aggregates
Small numbers, computed once (a short python3 or awk pass is fine — inline, no script file
left behind): total spend + per-project + per-model split; spend by day; prompts + sessions
per project; tasks opened/closed per project; gbrain query count + hit rate. Keep every
number traceable to an input file — no estimates.

### 3. Write ONE self-contained HTML page
`$OUT` gets everything inline (CSS in one `<style>` block, no JS libraries, no CDN, no
external fonts). Layout, top to bottom — data is the hero, copy is minimal:
1. **Header** — "devbrain retro · <since> → <today>", generated date, project count.
2. **Headline stats row** — prompts · sessions · tasks shipped · total spend · brain hit
   rate, as big numbers with small muted labels.
3. **Charts** — pure HTML/CSS horizontal bars (a `div` per row, width = %): spend by
   project, spend by model, tasks closed by project, spend by day (thin column strip).
4. **Narrative** — the `/journal` output grouped into one `<details open>` block per week
   (`## Week of <YYYYMMDD>`), day headings + project-prefixed bullets inside.
5. **Suggestions** — 2–4 short observations grounded in the numbers above (cost outliers,
   projects with stale open tasks, low hit-rate weeks). No filler.

Style constraints (the user's standing design rules — bake them in, don't improvise):
dark background (`#0d1117`-family), high-contrast cool palette, **one color per category**
(project/model keeps its color across every chart), muted-gray reference lines and labels,
no warm colors, alternating row tints on tabular lists, no width-change-on-hover.

### 4. Open + point
```bash
open "$OUT" 2>/dev/null || xdg-open "$OUT" 2>/dev/null || echo "$OUT"
```
Report the file path and the 2–4 suggestions in chat. The report is disposable output —
regenerating is cheap and always allowed to overwrite the same day's file; never edit a
past day's report in place.
