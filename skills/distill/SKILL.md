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
DATA="${DEVBRAIN_DATA:-$HOME/Desktop/devbrain-data}"
git -C "$DATA" pull --rebase --autostash --quiet 2>/dev/null || true
LOGDIR="$DATA/projects/$project/log"
BRAINDIR="$DATA/projects/$project/brain"
mkdir -p "$BRAINDIR"
echo "logs: $LOGDIR"; echo "brain: $BRAINDIR"
```

### 2. Read what's new since the last distill
Find log entries newer than the most recently updated brain page (the "since last
distill" marker), else read the whole log for this project.
```bash
last="$(find "$BRAINDIR" -name '*.md' -type f -exec stat -f '%m' {} \; 2>/dev/null | sort -nr | head -1)"
if [ -n "$last" ]; then
  find "$LOGDIR" -name '*.md' -type f -newermt "@$last" 2>/dev/null
else
  find "$LOGDIR" -name '*.md' -type f 2>/dev/null
fi
```
Read those files. Sort entries by their in-file `## HH:MM:SS` timestamps. If
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
gbrain embed --stale >/dev/null 2>&1 || true
```
Link related pages where it helps:
`gbrain link "project/<a>" "project/<b>" --type references`.

The flusher commits + pushes `$DATA` automatically (every 5 min); no manual git
needed. **Report** which pages you wrote/changed (slugs) and end with a one-line
"review with `git -C $DATA diff`" pointer — that's the safety net in place of a gate.

## Notes
- Keep pages small and linked, like the seed `project/devbrain-*` pages.
- Secrets: prompts can contain keys. If the log holds a secret, do NOT copy it
  into a brain page; note "redacted" and flag it. (Redaction at capture time is a
  known open item.)
