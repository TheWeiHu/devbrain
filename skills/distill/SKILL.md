---
name: distill
description: |
  devbrain curation step (Stage B — Brain) — the explicit "save this now" path. This
  is the design's "/checkpoint" role, named /distill to avoid Claude Code's native
  /checkpoint rewind alias. Reads new raw prompt-log entries for the current
  project, distills them into brain pages, and writes them directly (no approval
  gate — review by git diff). /continue runs this same fold-in automatically on
  resume; use /distill to checkpoint deliberately mid-session. Use when asked to
  "distill", "checkpoint the brain", "update the brain", or "save what we learned".
---

# /distill — turn new log into brain pages (just do it)

Distill writes directly — **no confirmation, no approval gate.** This is safe by
construction: the raw log is the source of truth, brain pages are a rebuildable
projection, and everything lands in a git repo. A wrong page is a `git revert`, and
the log is never touched. Group by **topic**, not by session. Append knowledge;
read a page before extending it — never clobber.

## Steps

### 1. Resolve identity + locate the log
```bash
cwd="$(pwd)"
remote="$(git -C "$cwd" remote get-url origin 2>/dev/null)"
if [ -n "$remote" ]; then project="$(basename "${remote%.git}")"; else project="$(basename "$cwd")"; fi
project="$(printf '%s' "$project" | tr '[:upper:] ' '[:lower:]-' | tr -cd '[:alnum:]._-')"
DATA="${DEVBRAIN_DATA:-$HOME/devbrain-data}"
git -C "$DATA" pull --rebase --autostash --quiet 2>/dev/null || true
LOGDIR="$DATA/projects/$project/log"
BRAINDIR="$DATA/projects/$project/brain"
LEDGER="$DATA/projects/$project/distilled.md"   # the cursor: what's already folded in
mkdir -p "$BRAINDIR"
echo "logs: $LOGDIR"; echo "brain: $BRAINDIR"; echo "ledger: $LEDGER"
```

### 2. Find what's new — the ledger cursor
`$LEDGER` (`projects/<project>/distilled.md`) is a plain-markdown record of which
log entries are already folded in — one line per session-log file with the last
entry timestamp processed. It lives in the data repo (committed by the flusher), so
it's durable across machines and **immune to git-pull mtime resets and brain edits**
— unlike a filesystem-mtime guess.

Print the ledger, then each log file's newest entry timestamp (deterministic via
`grep` — no eyeballing):
```bash
echo "=== ledger (already distilled) ==="
[ -f "$LEDGER" ] && cat "$LEDGER" || echo "(no ledger yet — first distill: everything is new)"
echo "=== each log file's NEWEST entry ==="
find "$LOGDIR" -name '*.md' -type f 2>/dev/null | sort | while IFS= read -r f; do
  rel="${f#"$LOGDIR"/}"; day="$(basename "$(dirname "$f")")"
  newest="$(grep -oE '^## [0-9]{2}:[0-9]{2}:[0-9]{2}' "$f" | tail -1 | sed 's/^## //')"
  echo "$rel  →  $day $newest"
done
```
A log file has **new** entries when it has no ledger line, or its newest entry is
later than its ledger timestamp. Read those files and fold in **only the entries
after the ledger timestamp** (each entry's datetime = its `## HH:MM:SS` + the file's
`<YYYY-MM-DD>/` dir; they sort lexically). Skip files already at their newest. If
nothing is new, say so and stop — don't write empty pages.

### 3. Distill and write directly
From the new log, extract durable knowledge — tasks, requirements, assumptions,
decisions, gotchas. Group by **topic**. For each topic, write a **new page**
`$BRAINDIR/<topic-slug>.md` or **append** to an existing page (read it first).
Carry a provenance pointer (log file + timestamp) into the page. Do **not** pause
for approval — write the files now.

### 4. Load into gbrain
```bash
for f in "$BRAINDIR"/*.md; do
  [ -e "$f" ] || continue
  slug="project/$(basename "$f" .md)"
  gbrain put "$slug" < "$f" >/dev/null 2>&1
  gbrain tag "$slug" "$project" >/dev/null 2>&1 || true
done
# Embeddings are an OpenAI-backed enhancement — only attempt them when a key is
# configured. Without one, pages are still fully usable via keyword search; the
# embed is just skipped (no error, no cost). Harmless if it runs keyless, but
# gating keeps keyless installs clean.
[ -n "$OPENAI_API_KEY" ] && gbrain embed --stale >/dev/null 2>&1 || true
```
Link related pages where it helps:
`gbrain link "project/<a>" "project/<b>" --type references`.

### 5. Advance the ledger
Record what you just folded in so the next distill skips it. Rewrite `$LEDGER` with
**one line per log file**, each set to that file's **newest** entry timestamp (the
`$day $newest` you printed in Step 2). Keep lines for files you didn't touch as they
were; add lines for files you processed. Format:
```markdown
# distilled — /distill cursor for <project>

Last log entry folded into the brain, per session file. /distill reads this to find
new entries. Durable + readable (git resets mtimes; this survives). To re-distill a
file, lower or delete its line by hand.

- 2026-06-14/edmonton.<sid>.md — through 16:19:02
- 2026-06-15/edmonton.<sid>.md — through 14:28:44
```
This is the only state distill keeps; it lives at the project root (not under
`brain/`, so it's never loaded as a page). Then the flusher commits + pushes `$DATA`
automatically (every 5 min); no manual git needed. **Report** which pages you
wrote/changed (slugs) and end with a one-line "review with `git -C "$DATA" diff`"
pointer — that's the safety net in place of a gate.

## Notes
- Keep pages small and linked, like the seed `project/devbrain-*` pages.
- Secrets: prompts can contain keys. If the log holds a secret, do NOT copy it
  into a brain page; note "redacted" and flag it. (Redaction at capture time is a
  known open item.)
