---
name: distill
description: |
  devbrain curation step (Stage B — Brain) — the explicit "save this now" path. This
  is the design's "/checkpoint" role, named /distill to avoid Claude Code's native
  /checkpoint rewind alias. Reads new raw prompt-log entries for the current
  project, distills them into brain pages, and extracts actionable open items into
  the project's TODO queue (the queue's only source). Writes directly (no approval
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
DATA="${DEVBRAIN_DATA:-$HOME/devbrain-data}"
# Resolve identity via the shared OFFLINE resolver so this matches the folder
# capture wrote to (projects/<owner>__<repo>).
PK="$HOME/.claude/hooks/devbrain-project-key.sh"; [ -f "$PK" ] || PK="$cwd/hooks/project-key.sh"
. "$PK"; project="$(devbrain_project_key "$cwd" "$DATA")"
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
`<YYYY-MM-DD>/` dir, **both in UTC** since 2026-06-15 — capture writes UTC so the
ledger stays unambiguous across timezone changes; older logs are local, internally
consistent per file; they sort lexically). Skip files already at their newest. If
nothing is new, say so and stop — don't write empty pages.

### 3. Distill and write directly
From the new log, extract durable knowledge — tasks, requirements, assumptions,
decisions, gotchas. Group by **topic**. For each topic, write a **new page**
`$BRAINDIR/<topic-slug>.md` or **append** to an existing page (read it first).
Carry a provenance pointer (log file + timestamp) into the page. Do **not** pause
for approval — write the files now.

### 3b. Extract open items → the TODO queue
The brain records *what happened*; the queue records *what's next*. As you read the
new log, also pull out **actionable open items** — anything phrased as work still to
do: "still open", "TODO", "we should…", "next step", a bug noted but not fixed, a
follow-up the user asked for and you haven't done. Turn each into a queue task. This
is the queue's **only source** — tasks are born here (and `/continue` runs this same
fold-in, so it refreshes the queue on resume).

```bash
TODO="$HOME/.claude/hooks/devbrain-todo.sh"; [ -x "$TODO" ] || TODO="$cwd/scripts/todo.sh"
"$TODO" list   # see what's already queued — DEDUPE against this before adding
```
For each genuinely new open item:
```bash
"$TODO" add "<imperative one-line task>" -p <0-100> -b "<why / acceptance criteria / log provenance>"
```
- **Priority (0–100):** user-asked-for & blocking → 80–100; clear improvement → 40–70;
  nice-to-have → 0–30.
- **Dedupe is mandatory** — if `list` already has the task (same intent), skip it; do
  not re-add. Don't queue vague aspirations, done work, or things smaller than a
  commit. A handful of sharp tasks beats a wall of noise.
- Creating tasks is the main job here; closing merged ones is step 3c below.

### 3c. Close merged review-tasks (confirmation-gated)
A task in `review` has an open PR (see [[theweihu__devbrain/todo-queue]]); it becomes
`done` only when that PR **merges**. Infer that here so the queue self-heals:
```bash
"$TODO" list review        # tasks parked awaiting merge (shows the pr: column)
```
For each review task with a `pr:`, check whether it merged:
```bash
gh pr view "<pr>" --json state,title -q '.state' 2>/dev/null   # MERGED | OPEN | CLOSED
```
- **MERGED → propose closing.** Collect all merged ones, show the user the list
  (task id + PR + title), and **ask for confirmation before marking any done** — this
  is the one place distill does NOT write silently, because closing someone's task on
  inferred state is higher-stakes than appending a page. On a yes: `"$TODO" done "<id>"`
  for each confirmed.
- **CLOSED (not merged) → leave it**, but flag it to the user (the PR was abandoned;
  the task may need re-opening with `"$TODO" release "<id>"`).
- **OPEN → leave it** in `review`; it is still in flight.
- No `gh`, or `pr:` empty → skip silently (offline / manual close still works).

### 4. Load into gbrain
Slug pages under a **per-project namespace** `<project>/<topic>` (NOT the shared
`project/` prefix — that flat namespace let same-named pages from different projects
collide in the one shared DB and overwrite each other). The topic drops a redundant
leading `<project>-` from the filename, so `devbrain-install.md` → `devbrain/install`.
Tag with the project too, so identity is reliable in both the slug and the tag.
```bash
for f in "$BRAINDIR"/*.md; do
  [ -e "$f" ] || continue
  base="$(basename "$f" .md)"
  slug="$project/${base#"$project"-}"        # e.g. devbrain/install ; redlens/roadmap-in-queue
  gbrain put "$slug" < "$f" >/dev/null 2>&1
  gbrain tag "$slug" "$project" >/dev/null 2>&1 || true
done
# Embeddings are an OpenAI-backed enhancement — only attempt them when a key is
# configured. Without one, pages are still fully usable via keyword search; the
# embed is just skipped (no error, no cost). Harmless if it runs keyless, but
# gating keeps keyless installs clean.
[ -n "$OPENAI_API_KEY" ] && gbrain embed --stale >/dev/null 2>&1 || true
```
Link related pages where it helps (same namespace):
`gbrain link "$project/<a>" "$project/<b>" --type references`.

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
`brain/`, so it's never loaded as a page).

### 6. Flush now — make the checkpoint durable immediately
Don't wait up to 5 min for the timer; commit + push the data repo now. The flusher
pulls-rebases, commits, and pushes **only if a remote exists** (`git push` is a no-op
otherwise), so this is safe whether or not the data repo is backed up off-machine:
```bash
FLUSH="$HOME/.claude/hooks/devbrain-flush.sh"; [ -x "$FLUSH" ] || FLUSH="$cwd/scripts/flush.sh"
DEVBRAIN_DATA="$DATA" "$FLUSH" distill 2>/dev/null || true
```
**Report** which pages/tasks you wrote/changed (slugs + new task ids) and end with a
one-line "review with `git -C "$DATA" diff`" pointer — that's the safety net in place
of a gate. (`/continue` runs this whole protocol on resume, so it inherits the flush.)

### 7. Weekly brain reconcile (mark-only, auto)
At most **once a week**, run a brain consistency pass so drift gets caught without a
manual `/reconcile`. Check the stamp file and decide if it is due:
```bash
RECON="$DATA/projects/$project/reconciled.md"
last="$(sed -n 's/^last reconcile: //p' "$RECON" 2>/dev/null | head -1)"
due=1
if [ -n "$last" ]; then
  last_s="$(date -j -f %Y-%m-%d "$last" +%s 2>/dev/null || date -d "$last" +%s 2>/dev/null || echo 0)"
  [ $(( ( $(date +%s) - last_s ) / 86400 )) -ge 7 ] || due=0
fi
echo "reconcile due: $due (last: ${last:-never})"
```
If `due` is 1 **and** there are brain pages, **run the `/reconcile` protocol now**
(`~/.claude/skills/reconcile/SKILL.md`) — it is mark-only and safe to run unattended.
Then stamp the date so it does not re-run for another week:
```bash
printf '# reconciled — /reconcile cursor for %s\n\nlast reconcile: %s\n' "$project" "$(date +%F)" > "$RECON"
DEVBRAIN_DATA="$DATA" "$FLUSH" reconcile 2>/dev/null || true
```
If not due, skip silently. `/continue` runs `/distill`, so it inherits this cadence —
there is no separate scheduler. (The stamp lives at the project root, not under
`brain/`, so it is never loaded as a page — like the distill ledger.)

## Notes
- Keep pages small and linked, like the seed `devbrain/*` pages.
- Secrets: prompts can contain keys. If the log holds a secret, do NOT copy it
  into a brain page; note "redacted" and flag it. (Redaction at capture time is a
  known open item.)
