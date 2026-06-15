---
name: distill
description: |
  devbrain curation step (Stage B — Brain) — the MANUAL "save this now" path. This
  is the design's "/checkpoint" role, named /distill to avoid Claude Code's native
  /checkpoint rewind alias. NOTE: /continue auto-folds new log into the brain on
  resume, so you only need /distill for a deliberate mid-session checkpoint with an
  approval gate. Reads new raw prompt-log entries for the current project, distills
  them into proposed brain-page updates, and — only after you approve — writes the
  pages and loads them into gbrain. Use when asked to "distill", "checkpoint the
  brain", "update the brain", or "save what we learned".
---

# /distill — turn new log into brain pages (explicit, no magic)

Curation is **explicit**. You read the raw log, propose page changes, the user
approves, then you write. Never infer silently. Each fact carries provenance back
to the log. Append knowledge; do not rewrite history.

> The common path is `/continue`, which folds new log in automatically (no gate,
> review-by-diff). Use `/distill` when you want to checkpoint deliberately
> mid-session and approve each page before it's written.

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
echo "logs: $LOGDIR"; echo "brain: $BRAINDIR"
```

### 2. Read what's new since the last distill
Find log entries newer than the most recently updated brain page (a reasonable
"since last distill" marker), else read the whole log for this project.
```bash
mkdir -p "$BRAINDIR"
last="$(find "$BRAINDIR" -name '*.md' -type f -exec stat -f '%m' {} \; 2>/dev/null | sort -nr | head -1)"
if [ -n "$last" ]; then
  find "$LOGDIR" -name '*.md' -type f -newermt "@$last" 2>/dev/null
else
  find "$LOGDIR" -name '*.md' -type f 2>/dev/null
fi
```
Read those files. Sort entries by their in-file `## HH:MM:SS` timestamps.

### 3. Distill (propose, do not write yet)
From the new log, extract durable knowledge — tasks, requirements, assumptions,
decisions, gotchas. Group by **topic**, not by session (topic grouping is the
brain's job; logs are sharded by where/when you worked). For each, decide:
- **New page** `project/<topic-slug>`, or
- **Append** to an existing page (read it first; never clobber).

Present the proposal to the user as a short list: for each page, the slug, whether
it's new or an append, and the proposed content. Include a provenance pointer
(log file + timestamp). **Stop and ask for approval.** This is the gate.

### 4. On approval, write + load
For each approved page, write/extend the markdown under `$BRAINDIR`, then:
```bash
gbrain put "project/<slug>" < "$BRAINDIR/<slug>.md" >/dev/null
gbrain tag "project/<slug>" "$project" >/dev/null 2>&1 || true
gbrain embed --stale >/dev/null 2>&1 || true
```
Link related pages where it helps:
`gbrain link "project/<a>" "project/<b>" --type references`.

The flusher commits + pushes `$DATA` automatically (every 5 min); no manual git
needed. Mention which pages changed and their slugs.

## Notes
- Keep pages small and linked, like the seed `project/devbrain-*` pages.
- Secrets: prompts can contain keys. If the log holds a secret, do NOT copy it
  into a brain page; note "redacted" and flag it. (Redaction at capture time is a
  known open item.)
