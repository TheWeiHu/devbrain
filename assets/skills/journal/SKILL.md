---
name: journal
description: |
  Render a dated, bullet-journal-style recap of what happened on this project —
  one bold YYYYMMDD heading per day, terse human bullets under it collapsing that
  day's turns (merges, prototypes, experiments, links), plus the TODOs opened and
  closed that day. Source is the same raw prompt-log /distill folds into the brain:
  each turn's one-sentence Stop-hook recap, grouped by day. Use when asked to
  "journal", "what happened this week", "daily recap", or "show me the last N days".
---

# /journal — daily journal from logs + TODOs

A read-only view. It turns the raw prompt-log's per-turn recap lines (`↳ … — <recap>`)
plus the TODO queue's open/close dates into a dated recap — **one bold `YYYYMMDD`
heading per day, a few terse bullets under it.** It never writes the brain or the queue;
it just reads and renders. Scope is **this project** (single stream); grouping several
projects into one journal is a deferred follow-up.

## Steps

### 1. Resolve identity + locate the log
Mirror `/distill` Step 1 so the folder matches what capture wrote to.
```bash
cwd="$(pwd)"
DATA="${DEVBRAIN_DATA:-$HOME/devbrain-data}"
project="$(devbrain project-key "$cwd")"          # shared identity resolver (devbrain on PATH)
git -C "$DATA" pull --rebase --autostash --quiet 2>/dev/null || true
LOGDIR="$DATA/projects/$project/log"
TODODIR="$DATA/projects/$project/todo"
echo "project=$project  logs=$LOGDIR"
```

### 2. Pick the day window
Default the last **7** days; an argument overrides it — `/journal 14`, `/journal 14d`,
or `/journal 3d` all mean N days. Date dirs are `YYYY-MM-DD`, so a string compare bounds
the window (lexical = chronological for fixed-width dates).
```bash
arg="${1:-7}"; days="$(printf '%s' "$arg" | grep -oE '[0-9]+' | head -1)"; days="${days:-7}"
SINCE="$(date -v-"${days}"d +%F 2>/dev/null || date -d "${days} days ago" +%F)"
echo "since=$SINCE (last $days days)"
```

### 3. Gather the raw material — recaps + TODO deltas, per day
The `↳ HH:MM:SS — <recap>` line is each turn's one-sentence "what I did" (the Stop-hook
recap). Collect them per day dir in the window; also collect any URLs the recaps mention
(worth surfacing as their own bullets). Then pull the TODOs **opened** (`created:`) and
**closed** (`done_at:`) on each day from the task frontmatter.
```bash
echo "=== RECAPS per day (newest first) ==="
find "$LOGDIR" -type d -name '20*' 2>/dev/null | awk -F/ -v s="$SINCE" '$NF >= s' | sort -r |
while IFS= read -r d; do
  day="$(basename "$d")"
  recaps="$(grep -rhoE '^↳ [0-9]{2}:[0-9]{2}:[0-9]{2} — .*' "$d" 2>/dev/null | sed -E 's/^↳ [0-9:]+ — //')"
  [ -n "$recaps" ] || continue
  echo "── $day"
  printf '%s\n' "$recaps"
done

echo "=== TODO opened / closed per day ==="
# awk does the date window compare — YYYY-MM-DD is a string to awk (dashes), so
# `>= s` is a lexical = chronological compare, and it sidesteps the shell's
# non-portable `[ a \> b ]` (errors under zsh).
for f in "$TODODIR"/*.md; do
  [ -e "$f" ] || continue
  title="$(sed -n 's/^# //p' "$f" | head -1)"
  cd="$(sed -n 's/^created: //p' "$f" | head -1 | cut -c1-10)"
  dd="$(sed -n 's/^done_at: //p' "$f" | head -1 | cut -c1-10)"
  [ -n "$cd" ] && printf 'opened\t%s\t%s\n' "$cd" "$title"
  [ -n "$dd" ] && printf 'closed\t%s\t%s\n' "$dd" "$title"
done | awk -F'\t' -v s="$SINCE" '$2 >= s' | sort -k2 -r
```

### 4. Render the journal
Collapse each day's recap lines into **a few terse, human bullets** — not one bullet per
turn. Merge near-duplicate turns, drop mechanical noise (branch cleanup, "let me check…"),
and keep the concrete result (what shipped, what was prototyped, what broke). Surface a
URL a recap mentioned as its own bullet. Fold the day's opened/closed TODOs into a bullet
or two (`opened: …` / `shipped: …`). Newest day first. Format — bold `YYYYMMDD` heading,
plain bullets, no times:

```markdown
**20260704**
- Merged the deadlock fix; investigated a friend's silent capture stall (stale hook path).
- Scoped a `devbrain doctor --fix` to self-heal stale hook wiring.
- shipped: auto-release fence holds from dead checkouts.

**20260703**
- Prototyped forever-mode fleet sizing so a momentary queue drain doesn't collapse to 1.
- opened: golden-transcript test for /distill.
```

Print the journal and stop — **this skill writes nothing.** If no recaps fall in the
window, say the window is empty and stop (don't invent days).
