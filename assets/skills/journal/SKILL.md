---
name: journal
description: |
  Render a dated, bullet-journal recap of what happened across ALL your projects — one
  bold YYYYMMDD heading per day, terse human bullets collapsing that day's turns (each
  prefixed with its project), plus the TODOs opened and closed that day. Source is the
  same raw prompt-log /distill folds into the brain: each turn's one-sentence Stop-hook
  recap. Rendered days are cached under $DEVBRAIN_DATA/journal/ and reused on re-runs
  (only today and uncached days re-render; `fresh` forces a full re-render).
  `/journal 14` widens the window; `/journal <project>` narrows to one project.
  Use when asked to "journal", "what happened this week", "daily recap", or "show me
  the last N days".
---

# /journal — daily journal from logs + TODOs

Turns the prompt log's per-turn recap lines (`↳ HH:MM:SS — <recap>`) plus the TODO
queue's open/close dates into a dated recap — **one bold `YYYYMMDD` heading per day, a
few terse bullets under it.** Scope is **all projects** by default; an argument narrows
to one. Rendered days are **cached** under `$DATA/journal/` (Steps 2 and 4) so re-runs
and `/brain-retro` reuse them instead of re-deriving; that cache is the only thing this
skill writes.

### 1. Parse args + select projects
Args, in any order: a number = day window (`/journal 14`, `/journal 3d`; default 7), a
word = project filter matched as a suffix of the `projects/<dir>` name (`/journal devbrain`
→ `theweihu__devbrain`).
Enumerate with `find`, never shell globs (zsh errors on an unmatched glob before any
guard runs), and iterate the newline-separated `$projects` with `while read` — never
`for p in $projects`, which does not word-split under zsh. The filter is a plain word;
the exact short-name match (`(^|__)<filter>$`, so `devbrain` doesn't also grab
`devbrain-data`) is preferred, falling back to a literal substring match.
```bash
DATA="${DEVBRAIN_DATA:-$HOME/devbrain-data}"
git -C "$DATA" pull --rebase --autostash --quiet 2>/dev/null || true
days=7; filter=""; fresh=""
# Only a purely numeric arg (optional d suffix) is a window — a project name
# CONTAINING a digit (pseo2, gpt4-eval) must stay a filter, not become days=2.
for a in "$@"; do case "$a" in fresh) fresh=1;; *[!0-9d]*|d*) filter="$a";; *[0-9]*) days="${a%d}";; *) filter="$a";; esac; done
# Dates are UTC everywhere in devbrain (log dirs, cache keys) — never local.
SINCE="$(date -u -v-"${days}"d +%F 2>/dev/null || date -u -d "${days} days ago" +%F)"
projects="$(find "$DATA/projects" -mindepth 1 -maxdepth 1 -type d 2>/dev/null -exec basename {} \;)"
if [ -n "$filter" ]; then
  filter="$(printf '%s' "$filter" | tr -cd 'a-zA-Z0-9._-')"          # dir-name charset only
  [ -n "$filter" ] || { echo "invalid project filter"; exit 0; }     # all-junk filter must not match everything
  fesc="$(printf '%s' "$filter" | sed 's/\./\\./g')"                 # literal dots in the regex
  exact="$(printf '%s\n' "$projects" | grep -iE -- "(^|__)${fesc}$")"
  projects="${exact:-$(printf '%s\n' "$projects" | grep -iF -- "$filter")}"
fi
[ -n "$projects" ] || { echo "no project matches '$filter'"; exit 0; }
```

### 2. Reuse the day cache — render only what's missing
Each rendered day lives at `$DATA/journal/<YYYY-MM-DD>.md` (top-level, cross-project —
the merged, project-prefixed form). A day two-or-more days old no longer changes, so its
cached entry is final; **today** is still accruing turns and **yesterday** may have
gained turns after its last render (a cache written mid-day is a snapshot, not a close),
so both always re-render.

**Filtered runs NEVER write the cache.** The cache holds the merged all-projects form
only; a `/journal <project>` run gathers a single project's slice, and writing that
slice to `$DATA/journal/<day>.md` would poison every later merged run and retro for
that date. With a filter active: reuse cached days (filtering their bullets by prefix),
render any uncached days ad hoc for output only, and skip Step 4's cache writes.
```bash
mkdir -p "$DATA/journal"; TODAY="$(date -u +%F)"
YDAY="$(date -u -v-1d +%F 2>/dev/null || date -u -d 'yesterday' +%F)"
d="$SINCE"; todo=""
while [ "$d" \< "$TODAY" ] || [ "$d" = "$TODAY" ]; do
  { [ -n "$fresh" ] || [ "$d" = "$TODAY" ] || [ "$d" = "$YDAY" ] || [ ! -s "$DATA/journal/$d.md" ]; } && todo="$todo $d"
  d="$(date -v+1d -j -f %F "$d" +%F 2>/dev/null || date -d "$d + 1 day" +%F)"
done
echo "days to render:${todo:- (none — all cached)}"
```
Note `[ "$d" \< "$TODAY" ]` is fine here (escaped `<` under `test` works in bash AND zsh;
it's the unescaped form that breaks). Only the listed days go through Steps 3–4; every
other day is read back from its cache file verbatim. `/journal fresh …` (the word `fresh`
as an arg) ignores the cache and re-renders the whole window — use after a backfill
import rewrites history.

### 3. Gather recaps + TODO deltas — ONLY for the days being rendered
Date dirs are `YYYY-MM-DD`, so a lexical `>=` compare bounds the window (fixed-width dates
sort chronologically) — and it sidesteps the shell's non-portable `[ a \> b ]` (errors
under zsh). Each recap/TODO line carries its project so the render can prefix bullets.
Both gathers are scoped to `$todo` (the days actually being rendered, from Step 2) —
a fully-cached run must not re-scan every log/todo file just to render from cache.
```bash
case "$todo" in *[0-9]*) : ;; *) echo "(all days cached — skipping gather)";; esac

echo "=== RECAPS per day (newest first) ==="
printf '%s\n' "$projects" | while IFS= read -r p; do
  find "$DATA/projects/$p/log" -type d -name '20*' 2>/dev/null | awk -F/ -v keep="$todo" 'index(keep, $NF)'
done | awk -F/ '{print $NF" "$0}' | sort -r | cut -d' ' -f2- | while IFS= read -r d; do   # newest DATE first across projects
  proj="$(basename "$(dirname "$(dirname "$d")")")"
  recaps="$(grep -rhoE '^↳ [0-9:]+ — .*' "$d" 2>/dev/null | sed -E 's/^↳ [0-9:]+ — //')"
  [ -n "$recaps" ] && { echo "── $(basename "$d") · $proj"; printf '%s\n' "$recaps"; }
done

echo "=== TODO opened / closed per day ==="
printf '%s\n' "$projects" | while IFS= read -r p; do
  find "$DATA/projects/$p/todo" -name '*.md' -type f -print0 2>/dev/null | xargs -0 awk '
    FNR==1 { if (title!="") emit(); title=""; cd=""; dd=""; file=FILENAME }
    /^# /        && title=="" { title=substr($0,3) }
    /^created: / && cd==""    { cd=substr($0,10,10) }
    /^done_at: / && dd==""    { dd=substr($0,10,10) }
    function emit(){ if(cd!="")print "opened\t"cd"\t"proj"\t"title; if(dd!="")print "closed\t"dd"\t"proj"\t"title }
    END { if (title!="") emit() }' proj="$p"
done | awk -F'\t' -v keep="$todo" 'index(keep, $2)' | sort -k2 -r
```
(The awk pass replaces a per-file sed/head/cut pipeline — one process per project
instead of ~8 forks per task file.)

### 4. Render, cache, output
Collapse each rendered day's recaps into **a few short bullets — scannable at a glance**:
2–5 per day, each ONE line (~15 words max), leading with the concrete result (shipped /
fixed / found / broke). Merge near-duplicates, drop mechanical noise (branch cleanup,
"let me check…") and drop detail that doesn't change what the reader would do — the raw
log keeps the detail; the journal is the skim layer. Fold the day's TODOs into an
`opened:` / `shipped:` bullet. Bullets carry the short project name (the `projects/<dir>`
name minus `<owner>__`) as their prefix.

**Write each newly rendered day to its cache file** `$DATA/journal/<YYYY-MM-DD>.md` —
but ONLY on unfiltered runs (the cache is always the merged all-projects form, prefixes
included; a filtered run's partial view must never land there — see Step 2). Then
assemble the output newest-day-first from cache + fresh days:

```markdown
**20260704**
- devbrain: shipped auto-release fence holds; traced a silent capture stall to a stale hook path.
- redlens: cut competitor-mention false positives ~30%.

**20260703**
- devbrain: forever-mode fleets no longer collapse to 1 worker on a momentary drain.
- devbrain opened: golden-transcript test for /distill.
```

With a single-project filter, output only that project's bullets (match the cached
bullets by their prefix — the cache always stores the merged form) and strip the prefix.

If neither recaps nor TODO deltas fall in the window, say so and stop — don't invent
days. A day with only TODO activity still renders (as its `opened:`/`shipped:` bullets).
