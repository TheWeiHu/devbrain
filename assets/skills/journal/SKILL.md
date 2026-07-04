---
name: journal
description: |
  Render a dated, bullet-journal recap of what happened on this project — one bold
  YYYYMMDD heading per day, terse human bullets collapsing that day's turns, plus the
  TODOs opened and closed that day. Source is the same raw prompt-log /distill folds
  into the brain: each turn's one-sentence Stop-hook recap. Use when asked to "journal",
  "what happened this week", "daily recap", or "show me the last N days".
---

# /journal — daily journal from logs + TODOs

Read-only. Turns the prompt log's per-turn recap lines (`↳ HH:MM:SS — <recap>`) plus the
TODO queue's open/close dates into a dated recap — **one bold `YYYYMMDD` heading per day,
a few terse bullets under it.** Writes nothing. Scope is **this project**.

### 1. Locate the log
Mirror `/distill` Step 1 so the folder matches what capture wrote.
```bash
DATA="${DEVBRAIN_DATA:-$HOME/devbrain-data}"
project="$(devbrain project-key "$(pwd)")"        # shared identity resolver (devbrain on PATH)
git -C "$DATA" pull --rebase --autostash --quiet 2>/dev/null || true
LOGDIR="$DATA/projects/$project/log"; TODODIR="$DATA/projects/$project/todo"
```

### 2. Gather recaps + TODO deltas per day
Default the last 7 days; an argument overrides it (`/journal 14`, `/journal 3d`). Date dirs
are `YYYY-MM-DD`, so a lexical `>=` compare bounds the window (fixed-width dates sort
chronologically) — and it sidesteps the shell's non-portable `[ a \> b ]` (errors under zsh).
```bash
days="$(printf '%s' "${1:-7}" | grep -oE '[0-9]+' | head -1)"; days="${days:-7}"
SINCE="$(date -v-"${days}"d +%F 2>/dev/null || date -d "${days} days ago" +%F)"

echo "=== RECAPS per day (newest first) ==="
find "$LOGDIR" -type d -name '20*' 2>/dev/null | awk -F/ -v s="$SINCE" '$NF >= s' | sort -r |
while IFS= read -r d; do
  recaps="$(grep -rhoE '^↳ [0-9:]+ — .*' "$d" 2>/dev/null | sed -E 's/^↳ [0-9:]+ — //')"
  [ -n "$recaps" ] && { echo "── $(basename "$d")"; printf '%s\n' "$recaps"; }
done

echo "=== TODO opened / closed per day ==="
for f in "$TODODIR"/*.md; do
  [ -e "$f" ] || continue
  title="$(sed -n 's/^# //p' "$f" | head -1)"
  cd="$(sed -n 's/^created: //p' "$f" | head -1 | cut -c1-10)"
  dd="$(sed -n 's/^done_at: //p' "$f" | head -1 | cut -c1-10)"
  [ -n "$cd" ] && printf 'opened\t%s\t%s\n' "$cd" "$title"
  [ -n "$dd" ] && printf 'closed\t%s\t%s\n' "$dd" "$title"
done | awk -F'\t' -v s="$SINCE" '$2 >= s' | sort -k2 -r
```

### 3. Render
Collapse each day's recaps into **a few terse human bullets** — not one per turn. Merge
near-duplicates, drop mechanical noise (branch cleanup, "let me check…"), keep the concrete
result (shipped / prototyped / broke). Fold the day's TODOs into an `opened:` / `shipped:`
bullet. Newest day first, bold `YYYYMMDD` heading, no times:

```markdown
**20260704**
- Merged the deadlock fix; traced a silent capture stall to a stale hook path.
- shipped: auto-release fence holds from dead checkouts.

**20260703**
- Prototyped forever-mode fleet sizing so a momentary queue drain doesn't collapse to 1.
- opened: golden-transcript test for /distill.
```

If no recaps fall in the window, say so and stop — don't invent days.
