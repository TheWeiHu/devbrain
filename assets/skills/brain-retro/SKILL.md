---
name: brain-retro
description: |
  Generate a monthly retro: ONE self-contained HTML page, styled like the devbrain
  queue dashboard, whose core is the FULL /journal narrative — every day in the
  window — plus a thin period-stats strip and a few data-grounded suggestions. It
  does NOT repeat the dashboard's live analytics charts. Saved to
  $DEVBRAIN_DATA/retro/ (top level, outside projects/) and opened in the browser.
  Named brain-retro to avoid gstack's /retro. Use when asked for a "retro",
  "monthly report", "month in review", or "how was my month".
---

# /brain-retro — the journal, archived as a dashboard-styled page

The retro is the **journal made durable**: the dashboard already shows live analytics
(spend, models, heatmaps, skills), so the retro must NOT re-render those charts. Its job
is the narrative record — every day's journal entry for the period — framed by a small
stats strip and suggestions. It parses nothing new and writes only the report; everything
is a rebuildable projection of the log.

### 1. Window + inputs
Default the last 30 days (`/brain-retro 60` overrides), all projects.
```bash
DATA="${DEVBRAIN_DATA:-$HOME/devbrain-data}"
days="$(printf '%s' "${1:-30}" | grep -oE '[0-9]+' | head -1)"; days="${days:-30}"
SINCE="$(date -v-"${days}"d +%F 2>/dev/null || date -d "${days} days ago" +%F)"
OUT="$DATA/retro/$(date +%F).html"; mkdir -p "$DATA/retro"   # top-level retro/, never under projects/
```
Inputs (skip what a project lacks — not every project has every file):
- **Narrative (the core):** run the `/journal` skill's gather+render protocol (Steps 1–3
  of `journal/SKILL.md`, installed alongside this skill) over the same window — do NOT
  re-implement log parsing here. **Every day with activity gets its entry rendered in
  full** — no weekly summarizing-away of days; collapse *within* a day (journal rules),
  never *across* days.
- **Stats strip only** (small numbers, one inline python3/awk pass, no script file left
  behind): prompts, sessions, tasks opened/shipped, total spend (`tokens.jsonl` with the
  dashboard's billing weights — `cache_create·1.25·in_rate`, `cache_read·0.1·in_rate`,
  `<synthetic>`/unknown = $0), gbrain hit rate (`gbrain-queries.log` rows with `hits>0`).
  These frame the narrative; anything chart-shaped stays in the dashboard.

### 2. Write ONE self-contained HTML page
`$OUT` gets everything inline (one `<style>` block, no JS libraries, no CDN, no external
fonts). **Match the queue dashboard's look** — same design tokens as `assets/dashboard.css`
(read it if unsure), not a new theme:
`--bg:#1C1C1E --panel:#2C2C2E --panel2:#242426 --line:#38383A --text:#F5F5F7
--muted:#98989D --accent:#0A84FF`, status colors `--open:#0A84FF --taken:#FF9F0A
--review:#BF5AF2 --held:#FF453A --done:#30D158`, `font:13px/1.5 system-ui`, panel cards
with `border:1px solid var(--line); border-radius:10-12px`, and the dashboard's sticky
blurred header (`backdrop-filter: blur`). One accent color per project, reused
consistently; no warm colors outside the status palette; no width-change-on-hover.

Layout, top to bottom:
1. **Sticky header** — "devbrain retro" + period `<since> → <today>` + muted counts
   (projects · prompts · sessions), dashboard-header style.
2. **Stats strip** — one row of small panel cards: tasks shipped/opened · total spend ·
   brain hit rate. Small; this frames, it doesn't chart.
3. **The journal** — every day entry, newest first: a `YYYYMMDD` day heading (weekday
   muted beside it), the day's project-prefixed bullets as a card per day. ALL days with
   activity appear; long months are fine — this page is the archive.
4. **Suggestions** — 2–4 short observations grounded in the period's data (cost outliers,
   stale open tasks, low hit-rate stretches). No filler.

### 3. Open + point
```bash
open "$OUT" 2>/dev/null || xdg-open "$OUT" 2>/dev/null || echo "$OUT"
```
Report the file path and the suggestions in chat. Reports live in `$DATA/retro/` at the
data-repo top level — deliberately outside any `projects/<p>/` folder, because a retro
spans all projects. Regenerating is cheap and always allowed to overwrite the same day's
file; never edit a past day's report in place.
