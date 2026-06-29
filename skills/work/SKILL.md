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

**What it keeps** — the steps below reference `/continue`'s numbered steps; read that
skill for the full detail of each, don't duplicate its logic here.

## Steps

1. **Setup** — run `/continue` **Step 1** (resolve `$project`, `$TODO`, `$BRAIN`,
   sync `$DATA`). Identity must agree with capture and the queue.

2. **Read the brain for orientation** — `/continue` **Step 3**: a project-biased
   `gbrain query`/`search`, then `gbrain get` + read the top 1-3 pages. Pull this
   into your working context so you build on existing conventions — but **skip the
   user-facing briefing**; there's no human to brief.

3. **Pick up the top task** — `/continue` **Step 6**: `id="$("$TODO" next)"`. Empty
   queue → nothing to do; say so and stop (this also ends a `/loop`/nightshift turn —
   don't invent work). Otherwise `claim` it (exit 2 → someone grabbed it; try the
   next), then `show` it.

4. **Pull this task's context** — `/continue` **Step 7**: a few focused `gbrain`
   queries off the task title/keywords; read the 3-5 most relevant pages **in full**
   (follow their `[[links]]`), no pre-`grep`. Together with Step 2 this is the context
   that makes the build correct — skipping either read is a defect, not a speedup.

5. **Synthesize + attach** — `/continue` **Step 8**: `"$TODO" context "$id"` with a
   ~500-1000 word brief (decisions/conventions that constrain the build, files to
   touch, page slugs). It persists on the task so a parallel/later worker inherits it.

6. **Branch + build the MVP** — `/continue` **Step 9**: branch off the base, build the
   *smallest coherent slice*, run only the tests covering what you touched. (Nightshift
   overrides the base to `origin/nightshift` and may direct-merge — its appended rules
   govern that; `/work` itself targets the normal base branch.)

7. **Open the PR, move task to review** — `/continue` **Step 10**: commit (plain task
   title, no "(MVP)"), push, `gh pr create`, then `"$TODO" review "$id" "$pr_url"`.
   The task stays in `review` until its PR merges.

**One task per `/work`.** Loop it (`/loop /work`, or the nightshift fleet) to drain
the queue — each run repeats these steps for the next task. End with the one-sentence
recap (devbrain's Stop hook logs it): name the task + the PR you opened.
