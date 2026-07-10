---
name: work
description: |
  Lean queue drainer — /continue's "work the top task" half WITHOUT the resume
  ceremony. Reads bounded, task-relevant brain context but does NOT
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
gain on a build turn), and there's no human to brief. `/work` still retrieves a
project convention or prior decision when the selected task exposes a concrete gap,
but it does that just in time instead of front-loading a fixed page quota.

**What `/work` does NOT do** (vs `/continue`):
- **No `/distill` fold-in.** It does not scan new log entries or rewrite brain pages.
  Brain/objective upkeep is the planning turn's job (and the morning `/distill`); a
  build turn rarely adds human prompt-log worth distilling. If you *know* there's new
  log to capture, run `/distill` or `/continue` explicitly.
- **No live-world recap or human briefing.** No broad `git status`/`log` + `gh
  issue`/`pr` refresh and no user-facing "where you are" summary. (`/work` still
  reads task-relevant brain context when needed — see Step 4 — it just doesn't
  report a status.)
- **No follow-up Q&A.** Unattended, there's no one to answer; queue follow-ups as
  TODOs (or append to `.nightshift/followups.md`) instead of asking.
- **Nothing user-facing but the recap.** The steps below persist state (the `todo
  context` brief, the branch/PR, the `todo review` move) but never *show*, *brief*,
  *remind*, or *ask* — the single human-facing output of a `/work` turn is the final
  one-sentence recap. That's the resume ceremony this skill exists to strip.

**Self-contained.** The steps below carry their own commands and rules — they do NOT
defer to `/continue`'s numbered steps, so renumbering `/continue` can't drift `/work`.
The setup, task-claim, context-read, and branch mechanics *mirror* `/continue`/`/distill` by design;
if you change the identity resolver or the stash-safety rule there, mirror it here too.

## Steps

1. **Setup — identity, TODO CLI, data sync.** Identity must agree with capture and the
   queue, so resolve it through the shared offline resolver:
   ```bash
   cwd="$(pwd)"
   DATA="${DEVBRAIN_DATA:-$HOME/devbrain-data}"
   project="$(devbrain project-key "$cwd")"   # shared identity resolver (devbrain on PATH)
   branch="$(git -C "$cwd" branch --show-current 2>/dev/null)"
   BRAINDIR="$DATA/projects/$project/brain"
   # The TODO queue is `devbrain todo …`; the offline BRAIN reader (greps on-disk
   # pages, used only when gbrain is absent) is `devbrain brain …`.
   git -C "$DATA" pull --rebase --autostash --quiet 2>/dev/null || true   # sync data repo
   echo "project=$project branch=$branch"
   ```

2. **Atomically claim the top eligible task before retrieving more context.**
   ```bash
   policy="${DEVBRAIN_TODO_TASK_POLICY:-shadow}"
   id="$(devbrain todo claim-next --policy "$policy")" # selected + claimed under one queue lock
   ```
   **No eligible task?** Nothing to do — say so and stop (this also ends a
   `/loop`/nightshift turn; don't invent work). The command already claimed the task,
   so parallel workspaces and overlapping contract keys cannot race:
   ```bash
   devbrain todo show "$id"           # H1 = goal, body = why / acceptance criteria
   ```
   Treat an `Acceptance:` line as the task-specific bar and restate it in the PR
   body (Step 7). A body may also provide `Outcome:`, `Evidence:`, `Scope:`,
   `Non-goals:`, `Verify:`, `Depends on:`, `Conflict key:`, and `Budget:`. If the
   labels are absent but the title, body, and repository make one testable outcome
   unambiguous, proceed. If completing the contract requires product judgment, hold
   the task with the exact missing decision instead of inventing scope.

3. **Localize the change in the live repository.** Before searching the brain, inspect
   the likely files, symbols, callers, and nearest tests. Reduce the task to four facts:
   one observable outcome, the smallest allowed scope, deterministic acceptance
   evidence, and the validation command. The repository is authoritative; a context
   brief is orientation, not permission to override current code.

4. **Retrieve brain context just in time.** Search only when the TODO and repository
   leave a named unresolved decision, convention, or prior-work question. Call
   **`gbrain` literally** when installed so the PostToolUse hook records the query;
   only when it is absent use `devbrain brain`. Run at most two focused queries and
   stop as soon as a query adds no new constraint. There is no result or page-count
   quota. Read only pages that directly answer the named gap; do not scan raw prompt
   logs or follow links speculatively.
   ```bash
   q="<the concrete unresolved question>"
   if command -v gbrain >/dev/null 2>&1; then
     if [ -n "$OPENAI_API_KEY" ]; then gbrain query "$q"; else gbrain search "$q"; fi
   else
     devbrain brain search "$q"
   fi
   # Read only a directly relevant hit, using its exact <owner>__<repo>/<page> slug:
   # gbrain get "<owner>__<repo>/<page>" --fuzzy
   ```
   Never hide `get` errors with `2>/dev/null`; near-miss hints are useful. A bounded
   brief injected by nightshift may already resolve every gap, in which case skip
   search entirely.

5. **Attach a compact work packet to the TODO.** Persist the localized contract so a
   retry or parallel worker inherits it. Keep the packet at **250 words or fewer**,
   with no minimum and no padding. Include exact file pointers, acceptance and
   validation, and only durable brain constraints actually learned; never paste pages.
   ```bash
   devbrain todo context "$id" <<'CTX'
   **Work packet**
   - Outcome: <single observable result>
   - Scope: <exact files/symbols; explicit non-goals>
   - Acceptance: <deterministic evidence>
   - Verify: `<targeted command>`
   - Brain constraint, if any: <decision> — `<owner>__<repo>/<page>`
   CTX
   ```
   Attach and move straight on; do not print the packet back. The task file is the
   persistence boundary.

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
