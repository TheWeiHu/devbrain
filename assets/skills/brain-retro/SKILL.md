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
fonts). **The page must read as another devbrain-dashboard view, not a themed cousin —
COPY the component CSS from `assets/dashboard.css`, don't approximate it.** The pieces to
lift verbatim (rules included):
- the `:root` tokens and `body` font;
- the sticky blurred `header`;
- `.psec` section rules (mono 700 11px, letter-spacing .12em, uppercase, bottom border);
- `.pcol` panels with a `.pch` header (8px colored dot + 12px 600 title + right-aligned
  muted `.claim`) and `.pbody`;
- `.chip` pills (10px 600, radius 999, tinted translucent background + matching border;
  `.chip.proj` mono) for the project tags.
The stats row is the one deliberate deviation (user-approved look): a grid of bordered
panel **boxes**, each with a big left-aligned number (~22px, 600) over a small muted
label — roomier than the dashboard's compact centered `.stat`. Keep the page airy:
generous padding, muted labels, numbers as the loudest element. One tint per project,
reused consistently; no warm colors outside the dashboard's status palette; no
width-change-on-hover.

Layout, top to bottom:
1. **Sticky header** — "devbrain retro" + period `<since> → <today>` + muted counts
   (projects · prompts · sessions).
2. **`.statbar`** — tasks shipped/opened · total spend · brain hit rate. Small; this
   frames, it doesn't chart.
3. **The journal** (under a `.psec` rule) — one `.pcol` per day, newest first: `.pch`
   holds the date + muted weekday `.claim`; `.pbody` holds the bullets. **Readable at a
   glance beats complete**: each bullet is ONE short line (~15 words), 2–5 bullets per
   day, project as a leading `.chip.proj` tag rather than inline bold prose. ALL days
   with activity appear; the detail lives in the log, not here.
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
