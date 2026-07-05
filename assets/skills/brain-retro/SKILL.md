---
name: brain-retro
description: |
  Generate a monthly retro: ONE self-contained HTML page in the user's approved
  retro style (GitHub-dark #0d1117 palette, saturated cool project colors), whose
  core is the FULL /journal narrative — every day in the window — plus a
  period-stats strip and a few data-grounded suggestions. It does NOT repeat the
  dashboard's live analytics charts. Saved to
  $DEVBRAIN_DATA/retro/ (top level, outside projects/) and opened in the browser.
  Named brain-retro to avoid gstack's /retro. Use when asked for a "retro",
  "monthly report", "month in review", or "how was my month".
---

# /brain-retro — the journal, archived as a styled monthly page

The retro is the **journal made durable**: the dashboard already shows live analytics
(spend, models, heatmaps, skills), so the retro must NOT re-render those charts. Its job
is the narrative record — every day's journal entry for the period — framed by a small
stats strip and suggestions. It parses nothing new and writes only the report; everything
is a rebuildable projection of the log. Content is journal-first; the LOOK is the user's
approved retro style (Step 2), which is deliberately NOT the queue dashboard's theme.

### 1. Window + inputs
Default the last 30 days (`/brain-retro 60` overrides), all projects.
```bash
DATA="${DEVBRAIN_DATA:-$HOME/devbrain-data}"
days="$(printf '%s' "${1:-30}" | grep -oE '[0-9]+' | head -1)"; days="${days:-30}"
SINCE="$(date -v-"${days}"d +%F 2>/dev/null || date -d "${days} days ago" +%F)"
OUT="$DATA/retro/$(date +%F).html"; mkdir -p "$DATA/retro"   # top-level retro/, never under projects/
```
Inputs (skip what a project lacks — not every project has every file):
- **Narrative (the core):** the `/journal` **day cache** — `$DATA/journal/<YYYY-MM-DD>.md`,
  one merged project-prefixed entry per day. Read cached days as-is; for days in the
  window with no cache file, run the `/journal` skill's protocol (installed alongside
  this skill) to render + cache them — do NOT re-implement log parsing here, and do NOT
  re-render days that are already cached. **Every day with activity gets its entry in
  full** — no weekly summarizing-away of days; collapse *within* a day (journal rules),
  never *across* days.
- **Stats strip only** (small numbers, one inline python3/awk pass, no script file left
  behind): prompts, sessions, tasks opened/shipped, total spend (`tokens.jsonl` with the
  dashboard's billing weights — `cache_create·1.25·in_rate`, `cache_read·0.1·in_rate`,
  `<synthetic>`/unknown = $0), gbrain hit rate (`gbrain-queries.log` rows with `hits>0`).
  These frame the narrative; anything chart-shaped stays in the dashboard.

### 2. Write ONE self-contained HTML page
`$OUT` gets everything inline (one `<style>` block, no JS libraries, no CDN, no external
fonts). **The look is the user's explicitly-approved retro style** (screenshot-approved
2026-07-05; the queue dashboard's `#1C1C1E` Apple-ish theme was tried and rejected for
this page). Its rules:
- **Palette (GitHub-dark family):** background `#0d1117`, panels `#161b22`, borders
  `#30363d` (hairlines `#21262d`), text `#e6edf3`/`#c9d1d9`, muted `#8b949e`.
- **Project colors are saturated and cool** — one per project, reused everywhere it
  appears: devbrain `#58a6ff`, chess-equity `#a371f7`, llm-as-judge `#2dd4bf`,
  redlens `#3fb950`, miscellaneous `#8b949e`, then cyans/indigos/teals for the rest.
  No warm colors.
- **Type:** `font:14px/1.5 -apple-system, system-ui, sans-serif`; section headers are
  small uppercase letter-spaced muted labels (`13px, .08em`); stat numbers are the
  loudest thing on the page (~22px, 600).
- **Boxes:** the stat strip is a grid of bordered panel boxes (`#161b22`, 1px `#30363d`
  border, 8px radius, roomy padding), big left-aligned number over a small muted label.
  Day entries are the same box style.
- **Project prefix is colored bold text, not a pill:** each bullet starts
  `<span style="color:<project-color>;font-weight:600">project:</span>` — the style the
  user pointed at. Keep the page airy; no width-change-on-hover.

Layout, top to bottom:
1. **Header** — "devbrain retro" + period `<since> → <today>` + muted generated/project
   counts.
2. **Stats strip** — prompts · sessions · tasks shipped/opened · total spend · brain hit
   rate, as the bordered boxes above. Small; this frames, it doesn't chart.
3. **The journal** (under an uppercase section label) — one box per day, newest first:
   `YYYYMMDD` heading with the weekday muted beside it, then the bullets. **Readable at
   a glance beats complete**: each bullet is ONE short line (~15 words), 2–5 bullets per
   day, colored-bold project prefix. ALL days with activity appear; the detail lives in
   the log, not here.
4. **Suggestions** — 2–4 short observations grounded in the period's data (cost outliers,
   stale open tasks, low hit-rate stretches), as accent-left-border callouts. No filler.

### 3. Open + point
```bash
open "$OUT" 2>/dev/null || xdg-open "$OUT" 2>/dev/null || echo "$OUT"
```
Report the file path and the suggestions in chat. Reports live in `$DATA/retro/` at the
data-repo top level — deliberately outside any `projects/<p>/` folder, because a retro
spans all projects. Regenerating is cheap and always allowed to overwrite the same day's
file; never edit a past day's report in place.
