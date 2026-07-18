---
name: distill
description: |
  devbrain curation step (Stage B — Brain) — the explicit "save this now" path. This
  is the design's "/checkpoint" role, named /distill to avoid Claude Code's native
  /checkpoint rewind alias. Reads new raw prompt-log entries for the current
  project, distills them into brain pages, and extracts actionable open items into
  the project's TODO queue (the queue's only source). Writes directly (no approval
  gate — review by git diff). /continue runs this same fold-in automatically on
  resume; use /distill in Claude Code or $distill in Codex to checkpoint deliberately mid-session. Use when asked to
  "distill", "checkpoint the brain", "update the brain", or "save what we learned".
---

# /distill / $distill — turn new log into brain pages (just do it)

Distill writes directly — **no confirmation, no approval gate.** This is safe by
construction: the raw log is the source of truth, brain pages are a rebuildable
projection, and everything lands in a git repo. A wrong page is a `git revert`, and
the log is never touched. Group by **topic**, not by session. Append knowledge;
read a page before extending it — never clobber.

## Steps

### 1. Resolve identity + locate the log
**Curator-only.** Run `devbrain role` first: if it prints `satellite`, say
"satellite machine — skipping distill; the curator folds these logs" and STOP.
A satellite (an AWS box, a second machine) only captures and flushes — its log
shards merge conflict-free, but a second concurrent curator rewriting the
ledger, pages, and preferences conflicts in git and strands the flusher.
```bash
cwd="$(pwd)"
DATA="${DEVBRAIN_DATA:-$HOME/devbrain-data}"
# Resolve identity via the shared OFFLINE resolver so this matches the folder
# capture wrote to (projects/<owner>__<repo>). The `devbrain` binary is on PATH.
project="$(devbrain project-key "$cwd")"
git -C "$DATA" pull --rebase --autostash --quiet 2>/dev/null || true
LOGDIR="$DATA/projects/$project/log"
BRAINDIR="$DATA/projects/$project/brain"
MEMDIR="$DATA/projects/$project/memory"          # mirrored Claude Code memory store (Stage A)
LEDGER="$DATA/projects/$project/distilled.md"   # the cursor: what's already folded in
mkdir -p "$BRAINDIR"
echo "logs: $LOGDIR"; echo "brain: $BRAINDIR"; echo "memory: $MEMDIR"; echo "ledger: $LEDGER"
```

### 2. Find what's new — the ledger cursor
`$LEDGER` (`projects/<project>/distilled.md`) is a plain-markdown record of which
log entries are already folded in — one line per session-log file with the last
entry timestamp processed. It lives in the data repo (committed by the flusher), so
it's durable across machines and **immune to git-pull mtime resets and brain edits**
— unlike a filesystem-mtime guess.

Print the ledger, then let bash compute **exactly which files are new** — don't
hand-roll a ledger parser. The cursor lines contain an em-dash (`—`); splitting on it
is the classic breakage. Instead key off
the **filename** and pull the trailing `HH:MM:SS` / `cksum`, which is em-dash-safe:
<!-- golden:cursor-detect — this block is pinned by internal/skilltest; keep it self-contained (LOGDIR/LEDGER/MEMDIR only) -->
```bash
echo "=== ledger (already distilled) ==="
[ -f "$LEDGER" ] && cat "$LEDGER" || echo "(no ledger yet — first distill: everything is new)"

echo "=== LOG files with NEW entries — read ONLY these ==="
find "$LOGDIR" -name '*.md' -type f 2>/dev/null | sort | while IFS= read -r f; do
  rel="${f#"$LOGDIR"/}"; day="$(basename "$(dirname "$f")")"
  # -a everywhere: one non-UTF8 byte makes grep call the file binary and drop every match.
  newest="$(grep -a -oE '^## [0-9]{2}:[0-9]{2}:[0-9]{2}' "$f" | tail -1 | sed 's/^## //')"
  [ -n "$newest" ] || continue
  rec="$(grep -a -F "$rel" "$LEDGER" 2>/dev/null | grep -a -oE '[0-9]{2}:[0-9]{2}:[0-9]{2}' | tail -1)"  # this file's cursor, em-dash-free
  # new = no cursor, OR newest > cursor. Compare via sort (portable across sh/bash/zsh —
  # `[ a \> b ]` is NOT, it errors under zsh). Timestamps are equal-width so sort = chrono.
  if [ -z "$rec" ] || { [ "$newest" != "$rec" ] && [ "$(printf '%s\n%s\n' "$newest" "$rec" | sort | tail -1)" = "$newest" ]; }; then
    echo "$rel  →  $day $newest (after ${rec:-START})"
  fi
done

echo "=== MEMORY files NEW or CHANGED since last distill — fold ONLY these ==="
if [ -d "$MEMDIR" ]; then
  find "$MEMDIR" -name '*.md' ! -name 'MEMORY.md' -type f 2>/dev/null | sort | while IFS= read -r m; do
    rel="memory/$(basename "$m")"; h="$(cksum "$m" | awk '{print $1}')"
    rec="$(grep -a -F "$rel" "$LEDGER" 2>/dev/null | grep -a -oE 'cksum [0-9]+' | awk '{print $2}' | tail -1)"
    [ "$h" = "$rec" ] || echo "$rel  (cksum $h${rec:+, was $rec})"
  done
else echo "(no memory store)"; fi
```
A log file is **new** when it has no ledger line or its newest entry is later than its
cursor; a memory file is **new/changed** when its `cksum` differs from the ledger
(the memory store gets the same cursor treatment as the log — without it, every
distill re-reads and re-dedupes *all* memory, a flat tax that grows with the store).
Read only the files the snippet listed, and fold in **only the log entries after the
cursor timestamp** (each entry's datetime = its `## HH:MM:SS` + the file's
`<YYYY-MM-DD>/` dir, **both in UTC** since 2026-06-15 — capture writes UTC so the
ledger stays unambiguous across timezone changes; older logs are local, internally
consistent per file; they sort lexically). If nothing is listed, say so and stop —
don't write empty pages.

### 3. Fold the new log into the brain and the queue
The new log turns into two things: **brain pages** (what happened) and **queue tasks**
(what's next). Write both directly — **no confirmation, no approval gate.**

**Fold in ONE fresh, clean-context sub-agent — its inputs are the Stage-A files Step 2
listed, and NOTHING else.** Do NOT fold in *this* session: whatever you happened to read
this turn (an Obsidian vault note, another project's source, a file you were debugging) is
ambient context that bleeds into the pages — a fact absent from the log gets written from it,
and the fold stops being a reproducible projection of log + memory. (Proven: a person's name
that lived only in a vault transcript, never in the prompt log, once entered a brain page from
a session that happened to have that transcript in context.) So hand the fold to a single
sub-agent whose ENTIRE world is the Step-2 inputs — the new post-cursor log entries and the
changed memory files — plus the brain pages it appends to.

This is **not** the forbidden pattern. What backfired before (and stays banned) is a *per-file
/ per-day* fan-out into *background* sub-agents that the turn then idle-**polls** for minutes,
each re-reading the same pages and re-deduping the same queue (≈Nx waste). This is the opposite:
**ONE** sub-agent, **foreground and blocking** (a single dispatch you await once), no per-file
split, no poll loop. The point is clean context, not parallelism.

Dispatch it with a **self-contained** prompt (Claude Code: the Task tool; Codex: the equivalent
one-shot sub-agent). It must NOT inherit this session and must read ONLY the files you name —
in particular **never** an Obsidian vault note (`1-TALKS.md`, `1-JOURNAL.md`, …) or any file not
in the input list. Pass it the exact `rel → day newest (after cursor)` log lines and
`memory/… (cksum …)` lines from Step 2, `$BRAINDIR`, and this instruction:

    You are folding new devbrain Stage-A input into brain pages + queue tasks. Your ENTIRE
    world is the files listed below — read ONLY these, plus the brain pages you append to and
    (for the folding rules) the installed distill SKILL.md Step 3 (under ~/.claude/skills or ~/.agents/skills). Do NOT read any other
    file: no vault notes (1-TALKS.md, 1-JOURNAL.md), no repo source, nothing from a parent
    session. A fact you cannot trace to one of these inputs must NOT enter a page or a task.
    Apply the "Brain pages", "Memory store", and "Queue tasks" rules from Step 3 to ONLY these
    inputs. When done, report: pages written/changed (slugs), queue task ids added, and the
    input files you actually folded (so the ledger can advance).

The **Brain pages / Memory store / Queue tasks** rules below are what that sub-agent applies.
If the backlog is genuinely large, tell it to process **newest-first** and cap to the most
recent files — the ledger leaves the rest marked new, so the next distill picks them up.

**Brain pages.** Extract durable knowledge — tasks, requirements, assumptions, decisions,
gotchas. Group by **topic**. For each topic, write a **new page**
`$BRAINDIR/<topic-slug>.md` or **append** to an existing page (read it first). Carry a
provenance pointer (log file + timestamp) into the page. Don't pause for approval — write
the files now.

**The global `preferences` page is special — and is NOT maintained here.** The user's
durable, repeated steers live in one load-bearing page OUTSIDE the per-project brain
(`$DATA/preferences/global.md`), which Claude Code `@import`s verbatim into user memory.
Because it must capture only steers that **recur** (which needs a span of history, not one
resume) and because `/distill` can run often (every `/continue`, every nightshift tick),
maintaining it on every distill would churn it. So it's refreshed in the **daily
maintenance window (Step 8)** — the same once-a-day pass that runs `/reconcile` — not in
this per-distill fold-in. Do NOT fold preferences into per-project brain pages here — leave
them for Step 8 and move on.

**Memory store.** Also fold in `$MEMDIR` — the mirrored Claude Code memory store, if
present. *Why this lives in distill:* Claude maintains its own `memory/` notes under
`~/.claude/projects/<slug>/memory/*.md`, and devbrain mirrors those into the data repo
as raw Stage-A input (the `capture-memory.sh` SessionEnd hook live, and `devbrain import`
for backfill) — exactly like prompts and responses. Distill is the curation step (Stage
B), so it must turn that raw memory into brain pages too, or the highest-value source
never reaches the brain. It's worth doing because these are the user's **own curated,
highest-fidelity** durable facts (name / why / how-to-apply) and they **outlive raw
transcripts** (which Claude Code prunes after a few weeks) — so memory is often the only
surviving record of older work. Read each **new-or-changed** memory file (the ones
Step 2 listed by `cksum` — skip the rest; unchanged memory is already folded in),
dedupe against existing pages, and fold genuinely-new facts into the relevant topic
page (or an `operational-memory-recovered.md` page). Skip `MEMORY.md` (just an index).

**Queue tasks.** The brain records *what happened*; the queue records *what's next*. As
you read the new log, also pull out **actionable open items** — anything phrased as work
still to do: "still open", "TODO", "we should…", "next step", a bug noted but not fixed, a
follow-up the user asked for and you haven't done. Turn each into a queue task. This is the
queue's **only source** — tasks are born here (and `/continue` runs this same fold-in, so
it refreshes the queue on resume).
```bash
devbrain todo list   # see what's already queued — DEDUPE against this before adding
```
For each genuinely new open item:
```bash
devbrain todo add "<imperative one-line task>" -p <0-100> -b "<why / acceptance criteria / log provenance>"
```
- **Priority (0–100):** user-asked-for & blocking → 80–100; clear improvement → 40–70;
  nice-to-have → 0–30.
- **Acceptance line:** include one line `Acceptance: <what makes the result good>` in the
  body — MANDATORY when quality depends on taste or judgment (essays, grading, design, UX
  copy), encouraged elsewhere. This is the task-specific bar a delegated worker builds to
  and restates in its PR body; without it, workers fall back to the generic protocol.
- **Dedupe is mandatory** — if `list` already has the task (same intent), skip it; do
  not re-add. Don't queue vague aspirations, done work, or things smaller than a
  commit.
- Creating tasks is the job here; **closing** merged ones is Step 4.

### 4. Reconcile the queue against merged PRs
Three checks that sync task state with what actually merged. "Merged" always comes from
GitHub via `gh` (distill runs from `$cwd`, the working repo), so every check below no-ops
the same way offline — no `gh`, skip silently. See [[<owner>__<repo>/todo-queue]].

**Close merged review-tasks (confirmation-gated).** A task in `review` has an open PR; it
becomes `done` only when that PR **merges**. Infer that here so the queue self-heals:
```bash
devbrain todo list review        # tasks parked awaiting merge (shows the pr: column)
gh pr view "<pr>" --json state -q '.state' 2>/dev/null   # MERGED | OPEN | CLOSED — per review task
```
- **MERGED → propose closing in ONE batched prompt.** Collect *all* merged review-tasks
  first, then confirm **once** — never per-task (with many parallel PRs, per-task prompts are
  the noise this guards against). Show the whole list (id + PR + title) in a single
  confirmation (Claude Code: one `AskUserQuestion`) with three choices: **Close all** →
  `devbrain todo done "<id>"` for each; **Skip** → leave them in `review`; **Don't ask again
  this session** → close all now, and skip this prompt for the rest of the session. If that
  snooze was already granted by an earlier `/distill` this session, close the merged ones
  silently and just report what closed (like self-heal below). This is the one place distill
  does NOT write silently by default — closing on inferred state is higher-stakes than
  appending a page — but the confirmation-before-close invariant still holds: nothing closes
  on inferred merge without an explicit yes, the session snooze being that yes granted once,
  so a `/loop`/resume isn't re-interrupted every tick.
- **CLOSED (not merged) → leave it**, but flag it (the PR was abandoned; the task may need
  re-opening with `devbrain todo release "<id>"`).
- **OPEN → leave it** in `review`; it is still in flight.

**Auto-heal open/taken zombies (quiet, no confirmation).** The review-task close above is
gated because those tasks were deliberately parked. But a task left `open` or `taken` while
its recorded PR has already **merged** is an unambiguous zombie (a manual merge, or any path
that bypassed `todo done`), so heal it silently — only report when it closes something:
```bash
healed="$(devbrain todo self-heal 2>/dev/null | grep '^self-heal: closed' || true)"
[ -n "$healed" ] && printf '%s\n' "$healed"   # silent when the backlog is already clean
```
`self-heal` scans `open taken`, checks each task's `pr:` with `gh`, and closes the merged ones.

**Retro-ledger merges that never had a task (judgment).** The two checks above heal tasks
whose PR merged. The mirror gap is a PR that **merged with no task at all** — a hotfix
branch, a hand-merged PR, work that bypassed the queue — which leaves the
brain/retro/dashboard under-counting what shipped. Deciding which of those merges deserve a
backfilled task is a **judgment call**, not a mechanical sweep: a blind "one done task per
untasked merged PR" would mint noise — release PRs, trivial chores, the whole pre-queue
history. So do this by hand, selectively. List recent merges and the PR numbers already on
a task, then eyeball the gap:
```bash
gh pr list --state merged --limit 30 --json number,title,mergedAt \
  -q '.[] | "#\(.number)  \(.mergedAt[:10])  \(.title)"' 2>/dev/null
# tasks live under $DATA/projects/$project/todo (the dir `devbrain todo` reads); pull every pr: number off them
known="$(grep -hoE 'pull/[0-9]+' "$DATA/projects/$project/todo"/*.md 2>/dev/null | grep -oE '[0-9]+' | sort -u)"
```
For each merged PR **not** in `known` that represents real shipped work worth recording
(skip releases, chores, anything predating the queue), mint a closed task:
```bash
id="$(devbrain todo add "<PR title>")"
devbrain todo review "$id" "<pr-url>" && devbrain todo done "$id"   # open -> review (records pr) -> done
```
`todo done` stamps `done_at` as now (when you ledgered it); if the merge date matters for
the cost/retro timeline, set `done_at:` in the task file to the PR's `mergedAt` by hand.
When in doubt, ledger only the recent, meaningful gaps rather than backfilling history.

### 5. Load into gbrain (optional — the pages are already saved on disk)
The pages you wrote to `$BRAINDIR` above ARE the durable brain; gbrain is just a
per-machine search index over them. So this whole step is a no-op when gbrain isn't
installed — skip it cleanly and the pages stay fully searchable offline via
`devbrain brain search/get`. When gbrain IS present, (re)index so ranked/semantic
search sees the new pages.

Slug pages under a **per-project namespace** `<project>/<topic>` (NOT the shared
`project/` prefix — that flat namespace let same-named pages from different projects
collide in the one shared DB and overwrite each other). The topic drops a redundant
leading `<project>-` from the filename, so `devbrain-install.md` → `devbrain/install`.
Tag with the project too, so identity is reliable in both the slug and the tag.
```bash
if command -v gbrain >/dev/null 2>&1; then    # index only — pages already persisted on disk
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
fi
```
Link related pages where it helps (same namespace, only when gbrain is installed):
`gbrain link "$project/<a>" "$project/<b>" --type references`.

### 6. Advance the ledger
Record what the fold sub-agent folded in so the next distill skips it — advance a line
**only for a file the sub-agent reported folding** (if it capped the backlog, the rest
stay marked new). Rewrite `$LEDGER` with **one line per log file** (set to that file's
**newest** entry timestamp — the `$day $newest` from Step 2) **and one line per memory
file folded** (set to its `cksum` from Step 2). Keep lines for files you didn't touch as
they were; add/update lines for files that were folded. Format:
```markdown
# distilled — /distill cursor for <project>

Last log entry folded into the brain, per session file (and cksum per memory file).
/distill reads this to find new entries. Durable + readable (git resets mtimes; this
survives). To re-distill a file, lower/delete its line (or change its cksum) by hand.

- 2026-06-14/edmonton.<sid>.md — through 16:19:02
- 2026-06-15/edmonton.<sid>.md — through 14:28:44
- memory/linux-smoke-box.md — cksum 1840293847
```
This is the only state distill keeps; it lives at the project root (not under
`brain/`, so it's never loaded as a page).

### 7. Flush now — make the checkpoint durable immediately
Don't wait up to 15 min for the scheduled commit; commit + push the data repo now. The flusher
pulls-rebases, commits, and pushes **only if a remote exists** (`git push` is a no-op
otherwise), so this is safe whether or not the data repo is backed up off-machine:
```bash
DEVBRAIN_DATA="$DATA" devbrain flush distill 2>/dev/null || true
```
**Report** which pages/tasks you wrote/changed (slugs + new task ids) and end with a
one-line "review with `git -C "$DATA" diff`" pointer — that's the safety net in place
of a gate. (`/continue` runs this whole protocol on resume, so it inherits the flush.)

### 8. Daily maintenance — reconcile + audit + refresh preferences (auto)
At most **once a day**, run the slow, cross-history upkeep so drift gets caught without a
manual command. This window governs the brain reconcile, the run audit, AND the global
preferences refresh — gated by **different scopes**:
- the brain reconcile and the run audit are **per-project**, gated by
  `$DATA/projects/$project/reconciled.md` and `…/audited.md`;
- the preferences page is **global** (one shared `$DATA/preferences/global.md`), gated by the
  date of the newest `· distill` entry in its edit history `$DATA/preferences/edits.md` — so
  distilling in N projects in one day still refreshes the shared page at most once (no separate
  stamp file: the history that records *what* you changed also records *when*).
The gates and their cursor files (`swept.md`, `reconciled.md`, `audited.md`, `archived.md`,
and the preferences `edits.md`) are all read by one tested verb — no shell date math. It
prints the space-separated names of the passes due now (`sweep`, `reconcile`, `audit`,
`preferences`, `archive`), and `maintenance stamp` writes a pass's cursor when you finish it:
```bash
due="$(DEVBRAIN_DATA="$DATA" devbrain maintenance due "$project")"    # e.g. "reconcile preferences" (empty = nothing due)
sweep_due=0; recon_due=0; audit_due=0; pref_due=0; arch_due=0
case " $due " in *" sweep "*) sweep_due=1;; esac
case " $due " in *" reconcile "*) recon_due=1;; esac
case " $due " in *" audit "*) audit_due=1;; esac
case " $due " in *" preferences "*) pref_due=1;; esac
case " $due " in *" archive "*) arch_due=1;; esac
echo "due: ${due:-(nothing)}"
```
If `$due` is empty, skip this whole step silently. Otherwise:

**(a0) Sweep transcripts** — only if `sweep_due` is 1: run `devbrain sweep --force`, then
`devbrain maintenance stamp "$project" sweep`. This is the backstop for machines where the
one-minute flusher isn't running — capture is sweep-based (no capture hooks), so a machine
with no flusher would otherwise only capture when something runs the sweep.

**(a) Reconcile the brain** — only if `recon_due` is 1 and there are brain pages: **run the
`/reconcile` protocol now** (the installed reconcile SKILL.md); it is mark-only and safe
to run unattended.

**(b) Refresh the global preferences page — only if `pref_due` is 1 — so it CONVERGES; the user's hand-edits win.**
`$DATA/preferences/global.md` is `@import`ed into **every** session, so it must stay a tight,
bounded set of durable steers — it **converges, it does not grow unbounded**. The user also
edits it directly (by hand, or via the dashboard), so you **consolidate, never clobber**: you
may reword only bullets *you* added, never the user's.

One readable log carries everything you need: `$DATA/preferences/edits.md`, the **diff history**
of the page. Each entry is a timestamped, sourced diff — `· you` is a hand-edit (dashboard or by
hand), `· distill` is a refresh you wrote. The diff is **context-free** (zero unchanged lines):
**every** line is a real change — a line starting `-` was removed, `+` was added, and the rest of
the line is the content verbatim. A removed bullet `- Foo` therefore appears as `-- Foo` (the
first `-` is the diff marker, `- Foo` is the line). Do not read it as two dashes of content:
````
## 2026-06-29T20:51:34 · you

```diff
-- No warm colors. Dark, high-contrast…
+- Prefer teal accents.
```
````
**Snapshot the page before you touch it**, so you can record your own diff at the end:
```bash
cp "$DATA/preferences/global.md" /tmp/pref.before 2>/dev/null || : > /tmp/pref.before
```
Then:

1. **Read the history** (`edits.md`, end to end — it's small). It tells you three things, with no
   parallel key ledger to keep in sync:
   - **What the user removed** — any `-` line in a `· you` entry. NEVER re-add a steer the user
     deleted; the removal is deliberate. (You just *see* it now, instead of matching keys.)
   - **Which current bullets are yours** — ones you introduced in a `· distill` entry that the user
     hasn't edited or removed since. Only these may you reword or collapse; treat every other
     bullet as the user's and leave it untouched.
   - **Whether the user edited since your last write** — if the newest `· you` entry is newer than
     your newest `· distill` entry, the page is the user's this run: stay **strictly additive**,
     make NO in-place edits (no rewording, no collapsing).
2. **Preserve the user's lines verbatim.** Never reword, reorder, or delete a line the user wrote.
3. **Mine genuinely-recurring steers across ALL projects**, not just the one you're distilling
   in. This page is global, but `/distill` runs inside one project's session with only that
   project's log in context; a steer you repeat once-per-repo across several repos IS a standing
   default, yet looks like a one-off if you read only the current project. So mine the
   **cross-project** corpus, not your loaded context:
   ```bash
   # Recurring steers are global — read the recent prompt log across EVERY project, not only
   # $project. Date dirs are YYYY-MM-DD, so a string compare bounds the window (last 14 days).
   SINCE="$(date -v-14d +%F 2>/dev/null || date -d '14 days ago' +%F)"
   find "$DATA"/projects/*/log -type d -name '20*' 2>/dev/null \
     | awk -F/ -v s="$SINCE" '$NF >= s' | sort      # read the prompt blocks in these dirs
   ```
   A steer qualifies only if it recurs (**given more than once**, counting across projects) and
   is a standing work-style default — design taste, scope/simplicity, "don't regress", process
   (plan-before-code, staging-not-prod, verify-before-done, commit+push), cost/infra. A one-off
   ask is queue/brain material, not a standing default. You only need the user-prompt blocks
   (the `## HH:MM:SS` headers and the text beneath), not the response samples — keeps it cheap.
4. **Converge, don't grow — consolidate-or-add, never append a duplicate.** Append-only is
   wrong: it makes the page grow without bound. The page has a **hard cap of 8192 bytes** (8 KB —
   the dashboard's Global Preferences meter shows size/cap and turns red over it; the value is
   `PrefsCapBytes` in `internal/config/config.go`). If your edits would leave the page **over
   the cap**, you MUST consolidate **your own** bullets — merge overlaps, tighten wording, drop
   the weakest of your additions — until it's back under, *before* you write. Never trim, reword,
   or drop a **user** line to make room (that violates step 2); if only user lines remain and it's
   still over, leave it over and note it. For each qualifying steer, do exactly **one** of:
   - **Skip** — if the page already expresses it (even loosely, in different words). Default and
     always safe; never append a second bullet for a steer already covered.
   - **Sharpen in place** — only if it refines a bullet **you** added (you can see yourself adding
     it in the history, step 1) **and** the user hasn't hand-edited since your last write: rewrite
     THAT bullet to the sharper wording. Net zero new lines. Never touch a bullet you can't see
     yourself adding — treat it as the user's and leave it untouched.
   - **Append** — only a steer no existing bullet covers, and that the history does **not** show
     the user having removed before (a deliberate deletion is final — never re-add it).
   - In the same uncontested case (no hand-edit since your last write), also **collapse any two of
     your OWN bullets that now say the same thing** into one.
   Structure: a global section first, then per-project `## <project>` subsections. Keep it
   imperative and CLAUDE.md-shaped — Claude Code `@import`s it verbatim, so it IS instructions.
5. **Record your change as a diff entry**, so the next run can see what you did and its date gates
   the next refresh. You snapshotted to `/tmp/pref.before`; append the diff only if you changed
   anything:
   ````bash
   if ! diff -q /tmp/pref.before "$DATA/preferences/global.md" >/dev/null 2>&1; then
     { printf '## %s · distill\n\n```diff\n' "$(date +%FT%T)"
       diff -U0 /tmp/pref.before "$DATA/preferences/global.md" | tail -n +3 | grep -vE '^@@'
       printf '```\n\n'; } >> "$DATA/preferences/edits.md"
   fi
   ````

Distill does **not** wire the `@import` into user memory — that one-time linking is owned by
`devbrain install` (the `claude-md` component), and it's genuinely one-time: the import points
at a *path*, so page-content edits never need re-linking. Curating the page here is enough; the
dashboard reads `global.md` directly and Claude Code re-reads it every session. Leaving the wire
to install means a deliberate `devbrain link-preferences --unlink` (or `--without claude-md`)
actually sticks instead of being re-added on the next distill.

**(c) Nudge a release if the working repo has drifted past its last tag** — judgment-only, never
forced. Releases stay manual (an auto-release-on-merge Action was rejected as over-engineering);
this is just the periodic reminder the user agreed to. It piggybacks on this daily window, so it
surfaces at most ~once a day, and stays silent in repos that don't tag releases:
```bash
# Only when THIS repo actually cuts tags; count commits on the main line since the last one.
if last_tag="$(git -C "$cwd" describe --tags --abbrev=0 2>/dev/null)" && [ -n "$last_tag" ]; then
  ref="$(git -C "$cwd" symbolic-ref -q --short refs/remotes/origin/HEAD 2>/dev/null)"
  [ -n "$ref" ] || ref="$(git -C "$cwd" rev-parse -q --verify origin/main >/dev/null 2>&1 && echo origin/main || echo HEAD)"
  n="$(git -C "$cwd" rev-list --count "$last_tag..$ref" 2>/dev/null || echo 0)"
  [ "${n:-0}" -gt 0 ] && echo "↪ $n commit(s) on $ref since $last_tag — consider cutting a release when it's a coherent batch (judgment call, not required)."
fi
```

**(d) Archive aged-out done tasks — at most ~monthly** so finished cards don't pile up on the
board. `todo archive` moves every `done` task whose `done_at` is >30 days old into
`todo/archive/`, which the dashboard's board hides (the queue globs `todo/*.md` non-recursively).
Gated by its own 30-day cursor (the `archive` pass in `maintenance due`) so it fires far less
often than this daily window:
```bash
if [ "$arch_due" = 1 ]; then
  devbrain todo archive 2>/dev/null | tail -1        # prints "archive: N task(s) archived"
  DEVBRAIN_DATA="$DATA" devbrain maintenance stamp "$project" archive
fi
```

**(e) Audit recent delegated runs** — only if `audit_due` is 1 and the project has finished
tasks: **run the `/audit` protocol now** (the installed audit SKILL.md). It is
evidence-only and report-only, safe to run unattended; drift it flags becomes queue tasks
or a note to the user, never a silent fix.

**Then stamp ONLY the passes that actually ran** — a pass that was due but skipped for its
precondition must stay due so tomorrow's distill retries it, not be marked done. So re-check
each precondition (the same one that gated running it in (a)/(e)): reconcile ran iff there are
brain pages, audit ran iff the project has finished tasks. The preferences pass needs no stamp —
the `· distill` entry you appended in step (b)5 *is* its cursor.
```bash
# reconcile: due AND brain pages exist (matches (a)'s run gate)
if [ "$recon_due" = 1 ] && [ -n "$(find "$DATA/projects/$project/brain" -name '*.md' -type f 2>/dev/null | head -1)" ]; then
  DEVBRAIN_DATA="$DATA" devbrain maintenance stamp "$project" reconcile
fi
# audit: due AND at least one finished (done) task exists (matches (e)'s run gate)
if [ "$audit_due" = 1 ] && DEVBRAIN_DATA="$DATA" devbrain todo list done 2>/dev/null | grep -q '^  \['; then
  DEVBRAIN_DATA="$DATA" devbrain maintenance stamp "$project" audit
fi
DEVBRAIN_DATA="$DATA" devbrain flush reconcile 2>/dev/null || true
```
`/continue` runs `/distill`, so it inherits this cadence — there is no separate scheduler.
(The cursor files live outside `brain/`, so they're never loaded as pages. The preferences gate is global
— derived from the newest `· distill` entry in `$DATA/preferences/edits.md` — so the shared page
refreshes at most daily no matter how many projects you distill in.)

## Notes
- Keep pages small and linked, like the seed `devbrain/*` pages.
- Secrets: prompts can contain keys. If the log holds a secret, do NOT copy it
  into a brain page; note "redacted" and flag it. (Redaction at capture time is a
  known open item.)
