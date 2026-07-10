---
name: work
description: |
  Lean queue drainer — /continue's "work the top task" half WITHOUT the resume
  ceremony. Reads the brain for context (project + per-task gbrain) but does NOT
  write it (no /distill fold-in) or brief a human (no live-world recap, no follow-up
  Q&A), then builds a MINIMAL MVP and opens a PR. Built for unattended loops
  (nightshift) and `/loop`, where re-folding the log and briefing a human every turn
  is pure overhead. Use when asked to "work the next task", "drain one task", or when
  an automated loop needs to pick up the next queue item fast. For an interactive
  resume (capture last session + brief me), use /continue instead.
---

# /work — drain one queue task to a PR, no resume ceremony

The split is simple: **`/work` reads the brain, but doesn't write it or brief a
human.** `/continue` exists to *resume a human* — it folds last session's log back
into the brain (`/distill`), refreshes the live git/PR world, and hands back a
briefing. In an unattended loop none of the *writing* or *reporting* pays off: N
parallel workers re-folding the same log is wasted work (~15% of turn time for zero
gain on a build turn), and there's no human to brief. But the *reads* — pulling
project conventions and the task's prior decisions out of gbrain — are exactly what
keep the MVP correct, so `/work` keeps both.

**What `/work` does NOT do** (vs `/continue`):
- **No `/distill` fold-in.** It does not scan new log entries or rewrite brain pages.
  Brain/objective upkeep is the planning turn's job (and the morning `/distill`); a
  build turn rarely adds human prompt-log worth distilling. If you *know* there's new
  log to capture, run `/distill` or `/continue` explicitly.
- **No live-world recap or human briefing.** No `git status`/`log` + `gh issue`/`pr`
  refresh, no user-facing "where you are" summary. (`/work` still *reads* the brain
  for context — see Steps 2 and 4 — it just doesn't report a status.)
- **No follow-up Q&A.** Unattended, there's no one to answer; queue follow-ups as
  TODOs (or append to `.nightshift/followups.md`) instead of asking.
- **Nothing user-facing but the recap.** The steps below persist state (the `todo
  context` brief, the branch/PR, the `todo review` move) but never *show*, *brief*,
  *remind*, or *ask* — the single human-facing output of a `/work` turn is the final
  one-sentence recap. That's the resume ceremony this skill exists to strip.

**Self-contained.** The steps below carry their own commands and rules — they do NOT
defer to `/continue`'s numbered steps, so renumbering `/continue` can't drift `/work`.
The setup, brain-read, and branch mechanics *mirror* `/continue`/`/distill` by design;
if you change the identity resolver or the stash-safety rule there, mirror it here too.

## Steps

1. **Setup — identity, TODO CLI, data sync.** Identity must agree with capture and the
   queue, so resolve it through the shared offline resolver:
   ```bash
   cwd="$(pwd)"
   DATA="$(devbrain config data-dir)" || exit 1
   project="$(devbrain project-key "$cwd")"   # shared identity resolver (devbrain on PATH)
   branch="$(git -C "$cwd" branch --show-current 2>/dev/null)"
   BRAINDIR="$DATA/projects/$project/brain"
   # The TODO queue is `devbrain todo …`; the offline BRAIN reader (greps on-disk
   # pages, used only when gbrain is absent) is `devbrain brain …`.
   DEVBRAIN_DATA="$DATA" devbrain flush work-sync || { echo "data sync failed; stop this task"; exit 1; }
   echo "project=$project branch=$branch"
   ```

2. **Read the brain for orientation** — the project lay-of-the-land read. Name
   `$project` in the query so its pages rank up (a bias, not a filter), and read the
   top hits **as-is** — don't `grep` to `^<project>/`, so shared cross-project pages
   (styles, conventions) still surface. Call **`gbrain` literally** when installed (so
   the PostToolUse hook logs the query); only when it's *absent* use the offline reader.
   ```bash
   Q="$project — ${branch:-$project}: state, recent decisions, open items, conventions"
   if command -v gbrain >/dev/null 2>&1; then
     [ -n "$OPENAI_API_KEY" ] && ranked="$(gbrain query "$Q" 2>/dev/null)"   # semantic; needs a key
     case "${ranked:-}" in ""|*"No results"*) ranked="$(gbrain search "$project" 2>/dev/null)";; esac
   else
     ranked="$(devbrain brain search "$project" 2>/dev/null)"   # gbrain absent -> offline grep
   fi
   printf '%s\n' "$ranked" | head -20      # read as-is — no <project>/ filter
   ```
   Read the top 1-3 pages with the **exact slug from the output**:
   `gbrain get "<owner>__<repo>/<page>" --fuzzy` (no gbrain? `devbrain brain get …`). A bare
   `<page>` is `page_not_found` — the brain is one namespace; `--fuzzy` resolves a
   near-miss or prints `Did you mean: …`. **Never pipe `get` through `2>/dev/null`** —
   it hides those hints. Pull this into your working context; **no user-facing briefing**.

3. **Pick up the top task.**
   ```bash
   id="$(devbrain todo next)"          # highest-priority open id (empty if queue empty)
   ```
   **Empty queue?** Nothing to do — say so and stop (this also ends a `/loop`/nightshift
   turn; don't invent work). Otherwise claim it so parallel workspaces don't collide:
   ```bash
   devbrain todo claim "$id"          # exit 2 → someone else grabbed it; re-run `next`, try the following one
   devbrain todo show "$id"           # H1 = goal, body = why / acceptance criteria
   ```
   If the body carries an `Acceptance:` line, that is the task-specific bar — build to it
   and restate it in the PR body (Step 7). There's no one to ask when it's missing:
   proceed, but on a **taste-dependent** task (writing quality, grading, design, UX copy)
   flag the gap in the PR body with the one-line standard you judged by.

4. **Pull this task's context** — now gather what's relevant to *this task* so you don't
   re-derive a made decision or miss a convention. A FEW focused queries (2-4), stopping
   once nothing new surfaces:
   ```bash
   title="$(devbrain todo show "$id" | sed -n 's/^# //p' | head -1)"
   qmode=query; [ -n "$OPENAI_API_KEY" ] || qmode=search
   for q in "$title" "$project conventions" "decisions and prior work related to $title"; do
     echo "── $q"
     if command -v gbrain >/dev/null 2>&1; then gbrain "$qmode" "$q" 2>/dev/null | head -8
     else devbrain brain search "$q" 2>/dev/null | head -8; fi
   done
   ```
   Read the **3-5 most relevant hits IN FULL** (same slug rules as Step 2), follow their
   `[[links]]`, and **don't pre-`grep`** the page — that throws away the surrounding
   decisions/gotchas a fresh worker is missing. Together with Step 2 this is the context
   that makes the build correct.

5. **Synthesize + attach to the TODO.** Distill what you read into a context brief for
   *this* task — not a page dump. Write it to the task so it persists and the next
   worker (parallel or later) inherits it:
   ```bash
   devbrain todo context "$id" <<'CTX'
   **Relevant from the brain**
   - <decision/convention that constrains this task> — `<owner>__<repo>/<page>`
   - <related prior work / file to touch> — `<owner>__<repo>/<page>`

   **Approach implied:** <one or two lines on how this shapes the MVP>
   CTX
   ```
   Aim for **~500-1000 words** — roughly what 3-5 fully-read pages distill to, and the
   floor for actually carrying the constraints/file-pointers/prior-work a fresh worker
   needs. Well under that means you under-read in Step 4 — go back, don't pad. **Attach
   and move straight on** — do not print the brief back; the task file is the only reader.

6. **Branch off the base, then build a MINIMAL MVP.** Start clean from the target branch:
   ```bash
   git -C "$cwd" stash 2>/dev/null || true          # park only TRACKED wip; NEVER `-u`
   git -C "$cwd" fetch --quiet origin
   git -C "$cwd" checkout -b "todo/$id" origin/main  # or your base branch
   ```
   Never add `-u` to that `stash`: it sweeps untracked files into the shared common-dir stash
   (one `refs/stash` across all worktrees) that `/work` never pops — in a nightshift
   worktree that buries operational files (`.nightshift/`, a worktree-local settings).
   Then build the **smallest coherent slice** that delivers the task's core and can be
   reviewed — no gold-plating, no adjacent refactors, no "while I'm here." Run only the
   tests covering what you touched. (Nightshift overrides the base to `origin/nightshift`
   and may direct-merge — its appended rules govern that; `/work` itself targets the
   normal base branch.)

7. **Open the PR, move the task to review.**
   ```bash
   git -C "$cwd" add -A && git -C "$cwd" commit -m "<type>: <task title>

   <one line on what this minimal slice does>"
   git -C "$cwd" push -u origin "todo/$id"
   pr_url="$(gh pr create --base main --title "<task title>" --body "<what/why · acceptance · scope · what's deferred>")"
   devbrain todo review "$id" "$pr_url"   # open->review: hidden from next/list, NOT done until the PR merges
   ```
   **Commit subjects are conventional** — prefix the task title with `feat` / `fix` /
   `docs` / `test` / `refactor` / `chore` (scope optional). The **PR title stays the plain
   task title** — no type prefix, and do **not** append "(MVP)"; note what's deferred in
   the PR body instead. The PR body restates the task's `Acceptance:` line (or the
   standard you judged by) and says how the slice meets it. The task stays in `review` until its PR merges (closed later via `todo
   done` when you're told it landed, or inferred by the next `/distill` reconcile). **No
   PR reminder, no follow-up questions** — queue any follow-ups as TODOs per the rule above.

**One task per `/work`.** Loop it (`/loop /work`, or the nightshift fleet) to drain
the queue — each run repeats these steps for the next task. End with the one-sentence
recap (devbrain's Stop hook logs it): name the task + the PR you opened.
