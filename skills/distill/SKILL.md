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
is the classic breakage (it cost minutes of fumbling in profiling). Instead key off
the **filename** and pull the trailing `HH:MM:SS` / `cksum`, which is em-dash-safe:
```bash
echo "=== ledger (already distilled) ==="
[ -f "$LEDGER" ] && cat "$LEDGER" || echo "(no ledger yet — first distill: everything is new)"

echo "=== LOG files with NEW entries — read ONLY these ==="
find "$LOGDIR" -name '*.md' -type f 2>/dev/null | sort | while IFS= read -r f; do
  rel="${f#"$LOGDIR"/}"; day="$(basename "$(dirname "$f")")"
  newest="$(grep -oE '^## [0-9]{2}:[0-9]{2}:[0-9]{2}' "$f" | tail -1 | sed 's/^## //')"
  [ -n "$newest" ] || continue
  rec="$(grep -F "$rel" "$LEDGER" 2>/dev/null | grep -oE '[0-9]{2}:[0-9]{2}:[0-9]{2}' | tail -1)"  # this file's cursor, em-dash-free
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
    rec="$(grep -F "$rel" "$LEDGER" 2>/dev/null | grep -oE 'cksum [0-9]+' | awk '{print $2}' | tail -1)"
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

**Fold inline, in this turn — do NOT fan out into background sub-agents and poll for
them.** That pattern looks like parallelism but backfires: in a headless/`-p` run the
poll loop idles the turn for *minutes* waiting, and per-file/per-day readers each
re-read the same brain pages and re-dedupe the same queue (≈Nx waste — this is the
single biggest blowup seen in profiling). One pass, here, reading each page/queue
once. If the backlog is genuinely large, process **newest-first** and it's fine to
**cap to the most recent files and defer the rest** — the ledger leaves un-folded
files marked new, so the next distill picks them up. Bounded every turn beats a
12-minute fan-out once.

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
# `devbrain-todo.sh` is the back-compat alias of `devbrain todo`; called by ABSOLUTE
# path here because hooks/skills run non-interactively where `devbrain` may not be
# on PATH. By hand, prefer the unified front door: `devbrain todo …`.
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
- Creating tasks is the job here; **closing** merged ones is Step 4.

### 4. Reconcile the queue against merged PRs
Three checks that sync task state with what actually merged. "Merged" always comes from
GitHub via `gh` (distill runs from `$cwd`, the working repo), so every check below no-ops
the same way offline — no `gh`, skip silently. See [[theweihu__devbrain/todo-queue]].

**Close merged review-tasks (confirmation-gated).** A task in `review` has an open PR; it
becomes `done` only when that PR **merges**. Infer that here so the queue self-heals:
```bash
"$TODO" list review        # tasks parked awaiting merge (shows the pr: column)
gh pr view "<pr>" --json state -q '.state' 2>/dev/null   # MERGED | OPEN | CLOSED — per review task
```
- **MERGED → propose closing.** Collect all merged ones, show the user the list (task id +
  PR + title), and **ask for confirmation before marking any done** — this is the one place
  distill does NOT write silently, because closing someone's task on inferred state is
  higher-stakes than appending a page. On a yes: `"$TODO" done "<id>"` for each confirmed.
- **CLOSED (not merged) → leave it**, but flag it (the PR was abandoned; the task may need
  re-opening with `"$TODO" release "<id>"`).
- **OPEN → leave it** in `review`; it is still in flight.

**Auto-heal open/taken zombies (quiet, no confirmation).** The review-task close above is
gated because those tasks were deliberately parked. But a task left `open` or `taken` while
its recorded PR has already **merged** is an unambiguous zombie (a manual merge, or any path
that bypassed `todo done`), so heal it silently — only report when it closes something:
```bash
healed="$("$TODO" self-heal 2>/dev/null | grep '^self-heal: closed' || true)"
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
# tasks live under $DATA/projects/$project/todo (the dir `$TODO` reads); pull every pr: number off them
known="$(grep -hoE 'pull/[0-9]+' "$DATA/projects/$project/todo"/*.md 2>/dev/null | grep -oE '[0-9]+' | sort -u)"
```
For each merged PR **not** in `known` that represents real shipped work worth recording
(skip releases, chores, anything predating the queue), mint a closed task:
```bash
id="$("$TODO" add "<PR title>")"
"$TODO" review "$id" "<pr-url>" && "$TODO" done "$id"   # open -> review (records pr) -> done
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
Record what you just folded in so the next distill skips it. Rewrite `$LEDGER` with
**one line per log file** (set to that file's **newest** entry timestamp — the
`$day $newest` from Step 2) **and one line per memory file you folded** (set to its
`cksum` from Step 2). Keep lines for files you didn't touch as they were; add/update
lines for files you processed. Format:
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

### 8. Daily maintenance — reconcile + refresh preferences (auto)
At most **once a day**, run the slow, cross-history upkeep so drift gets caught without a
manual command. This window governs the brain reconcile AND the global preferences refresh
— but they're gated by **two different stamps**, because they have different scope:
- the brain is **per-project**, gated by `$DATA/projects/$project/reconciled.md`;
- the preferences page is **global** (one shared `$DATA/preferences/global.md`), so it's
  gated by a **global** stamp `$DATA/preferences/.distilled` — otherwise distilling in N
  projects in one day would refresh the shared page N times, not once.
```bash
RECON="$DATA/projects/$project/reconciled.md"   # per-PROJECT: brain reconcile
GPREF="$DATA/preferences/.distilled"            # GLOBAL: shared preferences page
# chk <stampfile> <line-prefix> -> 1 if ≥1 day since the recorded date (or never), else 0
chk(){ local last s; last="$(sed -n "s/^$2//p" "$1" 2>/dev/null | head -1)"
  [ -z "$last" ] && { echo 1; return; }
  s="$(date -j -f %Y-%m-%d "$last" +%s 2>/dev/null || date -d "$last" +%s 2>/dev/null || echo 0)"
  [ $(( ( $(date +%s) - s ) / 86400 )) -ge 1 ] && echo 1 || echo 0; }
recon_due="$(chk "$RECON" 'last reconcile: ')"
pref_due="$(chk "$GPREF" 'last preferences distill: ')"
echo "reconcile due: $recon_due  ·  preferences due: $pref_due"
```
If both are 0, skip this whole step silently. Otherwise:

**(a) Reconcile the brain** — only if `recon_due` is 1 and there are brain pages: **run the
`/reconcile` protocol now** (`~/.claude/skills/reconcile/SKILL.md`); it is mark-only and safe
to run unattended.

**(b) Refresh the global preferences page — only if `pref_due` is 1 — ADDITIVELY; the user's hand-edits win.**
`$DATA/preferences/global.md` is the source of truth, and the user edits it directly (by
hand, or via the dashboard). So you **merge, never clobber**. Steps:

1. **Check provenance.** Read `$DATA/preferences/.edits.log` — append-only, one line per
   save: `<ts>\t<source>\t<hash>\t<note>`, where `source` is `dashboard` (a human hand-edit)
   or `distill` (you). Any `dashboard` line newer than the last `distill` line means the
   user has hand-edited since you last ran — their version is authoritative.
2. **Preserve everything that's there, verbatim.** Read the current page. Do NOT reword,
   reorder, or delete existing lines — especially anything changed since your last `distill`
   entry (those are the user's edits). You may only **ADD**.
3. **Add only genuinely-new recurring steers** (given **more than once** across the recent
   log) that aren't already represented — design taste, scope/simplicity, "don't regress",
   process (plan-before-code, staging-not-prod, verify-before-done, commit+push), cost/infra.
   A one-off ask is queue/brain material, not a standing default. Structure: a global section
   first, then per-project `## <project>` subsections. Keep it imperative and
   CLAUDE.md-shaped — Claude Code `@import`s it verbatim, so it IS instructions.
4. **Never re-add what the user removed.** Keep a `$DATA/preferences/.known-steers` ledger —
   one short key per steer you have EVER added. Before adding a steer, skip it if its key is
   already in the ledger: a key in the ledger but absent from the page means the user
   **deliberately deleted it**, so leave it gone. Append the keys of any steers you add.
5. **Record your write.** Append a provenance line to `.edits.log`:
   `printf '%s\tdistill\t%s\tdaily-refresh\n' "$(date +%FT%T)" "$(shasum -a256 "$DATA/preferences/global.md" | cut -c1-12)" >> "$DATA/preferences/.edits.log"`

Then ensure the user's memory `@import`s it — the
import line lives in **user memory** (home dir, never in a repo, nothing committed; no
reliance on the deprecated `CLAUDE.local.md`); the helper is idempotent and a missing page
is a safe no-op:
```bash
mkdir -p "$DATA/preferences"
LINK="$HOME/.claude/hooks/devbrain-link-preferences.sh"; [ -x "$LINK" ] || LINK="$cwd/scripts/link-preferences.sh"
DEVBRAIN_DATA="$DATA" "$LINK" 2>/dev/null || true
```

**Then stamp whichever pass(es) you actually ran** — each on its own stamp, so they re-run
independently a day later:
```bash
if [ "$recon_due" = 1 ]; then
  printf '# reconciled — /reconcile cursor for %s\n\nlast reconcile: %s\n' "$project" "$(date +%F)" > "$RECON"
fi
if [ "$pref_due" = 1 ]; then
  printf '# preferences — global /distill cursor\n\nlast preferences distill: %s\n' "$(date +%F)" > "$GPREF"
fi
DEVBRAIN_DATA="$DATA" "$FLUSH" reconcile 2>/dev/null || true
```
`/continue` runs `/distill`, so it inherits this cadence — there is no separate scheduler.
(Both stamps live outside `brain/`, so they are never loaded as pages. The preferences stamp
is global — `$DATA/preferences/.distilled` — so the shared page refreshes at most daily no
matter how many projects you distill in.)

## Notes
- Keep pages small and linked, like the seed `devbrain/*` pages.
- Secrets: prompts can contain keys. If the log holds a secret, do NOT copy it
  into a brain page; note "redacted" and flag it. (Redaction at capture time is a
  known open item.)
