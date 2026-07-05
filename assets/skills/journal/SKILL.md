---
name: journal
description: |
  Render a dated, bullet-journal recap of what happened across ALL your projects — one
  bold YYYYMMDD heading per day, terse human bullets collapsing that day's turns (each
  prefixed with its project), plus the TODOs opened and closed that day. Source is the
  same raw prompt-log /distill folds into the brain: each turn's one-sentence Stop-hook
  recap. `/journal 14` widens the window; `/journal <project>` narrows to one project.
  Use when asked to "journal", "what happened this week", "daily recap", or "show me
  the last N days".
---

# /journal — daily journal from logs + TODOs

Read-only. Turns the prompt log's per-turn recap lines (`↳ HH:MM:SS — <recap>`) plus the
TODO queue's open/close dates into a dated recap — **one bold `YYYYMMDD` heading per day,
a few terse bullets under it.** Writes nothing. Scope is **all projects** by default;
an argument narrows to one.

### 1. Parse args + select projects
Args, in any order: a number = day window (`/journal 14`, `/journal 3d`; default 7), a
word = project filter matched as a suffix of the `projects/<dir>` name (`/journal devbrain`
→ `theweihu__devbrain`).
Iterate the newline-separated `$projects` with `while read` — never `for p in $projects`,
which does not word-split under zsh. The filter prefers an exact short-name match
(`(^|__)<filter>$`, so `devbrain` doesn't also grab `devbrain-data`), falling back to
substring only when nothing matches exactly.
```bash
DATA="${DEVBRAIN_DATA:-$HOME/devbrain-data}"
git -C "$DATA" pull --rebase --autostash --quiet 2>/dev/null || true
days=7; filter=""
for a in "$@"; do case "$a" in *[0-9]*) days="$(printf '%s' "$a" | grep -oE '[0-9]+' | head -1)";; *) filter="$a";; esac; done
SINCE="$(date -v-"${days}"d +%F 2>/dev/null || date -d "${days} days ago" +%F)"
projects="$(ls -d "$DATA"/projects/*/ 2>/dev/null | xargs -n1 basename)"
if [ -n "$filter" ]; then
  exact="$(printf '%s\n' "$projects" | grep -iE -- "(^|__)${filter}$")"
  projects="${exact:-$(printf '%s\n' "$projects" | grep -i -- "$filter")}"
fi
[ -n "$projects" ] || { echo "no project matches '$filter'"; exit 0; }
```

### 2. Gather recaps + TODO deltas per day, per project
Date dirs are `YYYY-MM-DD`, so a lexical `>=` compare bounds the window (fixed-width dates
sort chronologically) — and it sidesteps the shell's non-portable `[ a \> b ]` (errors
under zsh). Each recap/TODO line carries its project so the render can prefix bullets.
```bash
echo "=== RECAPS per day (newest first) ==="
printf '%s\n' "$projects" | while IFS= read -r p; do
  find "$DATA/projects/$p/log" -type d -name '20*' 2>/dev/null | awk -F/ -v s="$SINCE" '$NF >= s'
done | awk -F/ '{print $NF" "$0}' | sort -r | cut -d' ' -f2- | while IFS= read -r d; do   # newest DATE first across projects
  proj="$(basename "$(dirname "$(dirname "$d")")")"
  recaps="$(grep -rhoE '^↳ [0-9:]+ — .*' "$d" 2>/dev/null | sed -E 's/^↳ [0-9:]+ — //')"
  [ -n "$recaps" ] && { echo "── $(basename "$d") · $proj"; printf '%s\n' "$recaps"; }
done

echo "=== TODO opened / closed per day ==="
printf '%s\n' "$projects" | while IFS= read -r p; do
  for f in "$DATA/projects/$p/todo"/*.md; do
    [ -e "$f" ] || continue
    title="$(sed -n 's/^# //p' "$f" | head -1)"
    cd="$(sed -n 's/^created: //p' "$f" | head -1 | cut -c1-10)"
    dd="$(sed -n 's/^done_at: //p' "$f" | head -1 | cut -c1-10)"
    [ -n "$cd" ] && printf 'opened\t%s\t%s\t%s\n' "$cd" "$p" "$title"
    [ -n "$dd" ] && printf 'closed\t%s\t%s\t%s\n' "$dd" "$p" "$title"
  done
done | awk -F'\t' -v s="$SINCE" '$2 >= s' | sort -k2 -r
```

### 3. Render
Collapse each day's recaps into **a few terse human bullets** — not one per turn. Merge
near-duplicates, drop mechanical noise (branch cleanup, "let me check…"), keep the concrete
result (shipped / prototyped / broke). Fold the day's TODOs into an `opened:` / `shipped:`
bullet. Newest day first, bold `YYYYMMDD` heading, no times. In the merged (default) view,
prefix each bullet with the short project name — the `projects/<dir>` name minus its
`<owner>__` prefix; with a single-project filter, omit prefixes (renders exactly as the
old per-project journal):

```markdown
**20260704**
- devbrain: merged the deadlock fix; traced a silent capture stall to a stale hook path.
- redlens: cut competitor-mention false positives ~30% with dedup.
- devbrain shipped: auto-release fence holds from dead checkouts.

**20260703**
- devbrain: prototyped forever-mode fleet sizing so a momentary queue drain doesn't collapse to 1.
- devbrain opened: golden-transcript test for /distill.
```

If no recaps fall in the window, say so and stop — don't invent days.
