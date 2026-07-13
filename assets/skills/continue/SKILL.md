---
name: continue
description: |
  devbrain resume cursor (Stage C — Assemble) that also works the queue. First
  folds new prompt-log entries into the project's brain pages AND extracts open
  items into the TODO queue, pulls the brain, refreshes the live world
  (git/issues/CI), and gives a short briefing. Then it picks up the highest-priority
  task, queries gbrain to synthesize that task's context and attaches it to the TODO
  (shown to you), builds a MINIMAL MVP for it, opens a PR for review, and asks
  follow-up questions. Loop it with `/loop /continue` to keep draining the queue. Use when
  asked to "continue", "resume", "where was I", "pick up where I left off", "work
  the next task", or "what's the state of this".
---

# /continue — fold in, then assemble the right amount of context

You are resuming work. devbrain's job here is **subtraction, not stuffing**: first
make sure last session's knowledge is captured, then pull only what's relevant and
hand back a short briefing. The raw log is the source of truth; the brain is a
queryable projection of it — so auto-writing pages here is safe (a bad page is
reverted; the log is never touched).

The skill has two phases: **Phase A (Steps 1-5) orients** — capture last session,
pull the brain, brief the user. **Phase B (Steps 6-11) moves** — pick the top task
and drive it to a reviewable PR. Run them in order.

---

# Phase A — Orient

## Step 1 — Set up: identity, TODO CLI, data sync
```bash
cwd="$(pwd)"
DATA="${DEVBRAIN_DATA:-$HOME/devbrain-data}"
# Resolve identity via the shared OFFLINE resolver so capture, the queue, and the
# skills all agree on the projects/<owner>__<repo> folder. The `devbrain` binary is
# on PATH; its subcommands (`devbrain todo`, `devbrain brain`, …) are the CLI below.
project="$(devbrain project-key "$cwd")"
branch="$(git -C "$cwd" branch --show-current 2>/dev/null)"
LOGDIR="$DATA/projects/$project/log"
BRAINDIR="$DATA/projects/$project/brain"
echo "project=$project branch=$branch"

# Phase B leans on the TODO queue (`devbrain todo …`). The offline BRAIN reader is
# `devbrain brain …` (greps the on-disk pages). The steps below call `gbrain`
# LITERALLY when it's installed — so the PostToolUse hook keeps logging the
# query — and only use `devbrain brain` on the gbrain-absent branch, so the brain
# stays searchable with zero engine.

# Sync the data repo — pull logs/pages other machines pushed.
git -C "$DATA" pull --rebase --autostash --quiet 2>/dev/null || true
```

## Step 2 — Fold in new log (run the /distill protocol)
**Run the `/distill` skill's protocol now** (Steps 2-6 of `~/.claude/skills/distill/SKILL.md`):
find log entries newer than the ledger cursor, distill them into topic pages + queue
tasks, reconcile the queue against merged PRs, load gbrain, and advance the ledger — all
written directly (no gate). `/distill` is the single source of truth for *how* fold-in
works — do not duplicate its logic here; follow it.

`$DATA`, `$project`, `$LOGDIR`, `$BRAINDIR` are already resolved (Step 1), so skip
distill's Step 1 and start from its "read what's new" step. If there are no new log
entries, say so and move on.

## Step 3 — Read the brain (project-biased, not project-walled)
This is the **project orientation** read — the lay of the land. (Phase B does a
second, *task-specific* read in Step 7; this one is broader and shallower.) Two rules:
**(1)** name `$project` in the query so its pages rank up — a bias, not a filter;
**(2)** read the top hits **as-is** — do *not* `grep` to `^<project>/`, so shared
cross-project pages (coding styles, review conventions) still surface. This project's
own pages are always on disk under `$BRAINDIR`, so the query is for ranking/discovery,
not fencing. Call **`gbrain` literally** when it's installed (semantic `query` needs
`OPENAI_API_KEY` too; without it, or on no hits, fall back to keyword `search`). Only when
gbrain is *absent* use the offline `devbrain brain` reader — it greps the on-disk pages.

```bash
Q="$project — ${branch:-$project}: state, recent decisions, open items, conventions"
ranked=""
if command -v gbrain >/dev/null 2>&1; then   # literal gbrain -> the PostToolUse hook logs the query
  [ -n "$OPENAI_API_KEY" ] && ranked="$(gbrain query "$Q" 2>/dev/null)"   # hybrid semantic
  case "$ranked" in ""|*"No results"*) ranked="$(gbrain search "$project" 2>/dev/null)";; esac
else
  ranked="$(devbrain brain search "$project" 2>/dev/null)"   # gbrain absent -> offline grep over on-disk pages
fi
printf '%s\n' "$ranked" | head -20      # read as-is — no <project>/ filter
```
**Reading a page (the slug rules — referenced again in Step 7):** use the **exact slug
from the search output** with `gbrain get "<owner>__<repo>/<page>" --fuzzy` (no gbrain?
`devbrain brain get "<owner>__<repo>/<page>" --fuzzy` reads it off disk). The brain is one
global namespace, so a bare `<page>` (no `<owner>__<repo>/` prefix) is `page_not_found`;
`--fuzzy` resolves a bare or slightly-off slug, or prints `Did you mean: …` with the real
one. **Never pipe `get` through `2>/dev/null`** — that hides those hints and leaves a
failed read looking like an empty page. Here, read the top 1-3 pages; pull cross-project
hits in only when relevant (e.g. shared conventions). Every literal `gbrain` call is logged
automatically by the `PostToolUse(Bash)` hook to `projects/<project>/gbrain-queries.log` —
the offline `devbrain brain` fallback isn't (there's no gbrain call to log).

## Step 4 — Refresh the live world
Status lives in the world, never invented.
```bash
git -C "$cwd" fetch --quiet 2>/dev/null || true
git -C "$cwd" status -sb | head -20
git -C "$cwd" log --oneline -5
command -v gh >/dev/null && gh issue list --limit 10 2>/dev/null || true
command -v gh >/dev/null && gh pr status 2>/dev/null || true
```

## Step 5 — Brief the user (short)
A few lines, then move straight into the work:
- **Folded in:** N new brain pages + M new queue tasks from last session (or
  "nothing new"); "review with `git -C "$DATA" diff`" if anything was written.
- **Where you are:** project, branch, and the task the branch implies.
- **From the brain:** the 2-4 most relevant in-scope facts/decisions/open items
  (with page slug pointers, e.g. `<project>/<topic>`).
- **From the world:** uncommitted changes, ahead/behind, open issues/PRs, CI.
- **Top of the queue:** the highest-priority task you're about to pick up.

Briefing plus pointers — do not dump whole pages. The flusher pushes pages/tasks you
wrote in Step 2 automatically (every minute); no manual git needed.

---

# Phase B — Work the top task

The queue exists to be drained. This is the heart of `/continue` — not just orienting,
but moving: one task, to a reviewable PR. **One task per `/continue`** — drain the rest
with `/loop /continue`, each run repeating Phase B for the next task.

## Step 6 — Pick up the top task
```bash
id="$(devbrain todo next)"          # highest-priority open task id (empty if queue empty)
```
- **Empty queue?** If `id` is empty, there's nothing to do — say so and stop (this
  also ends a `/loop /continue`). Don't invent work.
- **Claim it** so parallel workspaces don't collide:
  ```bash
  devbrain todo claim "$id"        # exit 2 → someone else grabbed it; re-run `next` and try the following one
  devbrain todo show "$id"         # read the full task: H1 = goal, body = why / acceptance criteria
  ```
- **Surface the acceptance bar.** If the body has an `Acceptance:` line, quote it in the
  briefing — it's the task-specific definition of good, and the PR body must answer it
  (Step 10). If the task is **taste-dependent** (writing quality, grading, design, UX
  copy) and has none, ask the user ONE short question — "what makes this one good?" —
  and add their answer to the task file body as an `Acceptance:` line (above any
  `## Context` section) so later workers inherit it. Non-taste tasks proceed without.

## Step 7 — Pull this task's context from gbrain
Step 3 oriented you on the *project*; now gather what's relevant to *this task* so you
don't re-derive a decision already made or miss a convention. Run a FEW focused queries
off the task's goal and keywords — aim for 2-4, and stop early once nothing new surfaces:
```bash
title="$(devbrain todo show "$id" | sed -n 's/^# //p' | head -1)"
qmode=query; [ -n "$OPENAI_API_KEY" ] || qmode=search    # semantic query needs an OpenAI key + gbrain; else keyword search
for q in "$title" "$project conventions" "decisions and prior work related to $title"; do
  echo "── $q"
  if command -v gbrain >/dev/null 2>&1; then            # literal gbrain -> logged by the hook
    gbrain "$qmode" "$q" 2>/dev/null | head -8
  else
    devbrain brain search "$q" 2>/dev/null | head -8           # gbrain absent -> offline reader
  fi
done
```
Read hits with the **same slug rules as Step 3** (full `<owner>__<repo>/<page>` slug,
`--fuzzy`, never `2>/dev/null`), plus two rules specific to building real context:
- **Read the 3-5 most relevant hits IN FULL** — and follow any `[[links]]` on those
  pages to others that clearly bear on the task. A single page is rarely enough.
- **Don't pre-filter the page** with `gbrain get … | grep <keyword>` — grep throws away
  the surrounding decisions/gotchas that are exactly what a fresh worker is missing.
  Synthesize from the full text in Step 8 instead.

Existing decisions, file/naming conventions, and related implementation pages are what
keep the MVP consistent. Prefer this over asking the user for context the brain may
already record.

## Step 8 — Synthesize, attach to the TODO, and show the user
Distill what you read into a context brief for *this* task — not a page dump: the
decisions/conventions that constrain the build, the relevant files, and the page slugs
to read deeper. Write it to the task file so it persists and the next worker (or a
parallel/nightshift run) inherits it:
```bash
devbrain todo context "$id" <<'CTX'
**Relevant from the brain**
- <decision/convention that constrains this task> — `<owner>__<repo>/<page>`
- <related prior work / file to touch> — `<owner>__<repo>/<page>`

**Approach implied:** <one or two lines on how this shapes the MVP>
CTX
```
`todo context` appends (or replaces, on a re-run) a `## Context (synthesized …)`
section in the task body — multi-line, idempotent. **Aim for ~500-1000 words** — that's
roughly what 3-5 fully-read pages distill down to, and it's the floor for actually
carrying the decisions, constraints, file pointers, and prior work a fresh worker needs,
not a one-line gesture. If your draft is well under ~500 words, you probably under-read
in Step 7 — go back and read more pages rather than padding. The one exception: if the
brain genuinely surfaced little, write the little there is and say so explicitly ("brain
had little on this") rather than inventing filler.

Then **show it to the user** — print the brief back (`devbrain todo show "$id"`, whose body now
includes the `## Context` section, or just paste it) so they see what's framing the build
before you start. This is part of the briefing, not a silent step.

## Step 9 — Branch off the base, then build a MINIMAL MVP
Start clean from the target branch (don't pile onto an unrelated WIP branch):
```bash
# Park only TRACKED WIP — never `-u`. `git stash -u` sweeps untracked files into
# git's stash, which lives in the SHARED common dir (one `refs/stash` across all
# worktrees) and which /continue never pops. In a nightshift worktree that buries
# operational untracked files (.nightshift/, a worktree-local .claude/settings.json)
# and they're lost. Tracked changes carry into the new branch on checkout anyway, so
# a fresh worktree makes this a no-op; we keep it only to not pile interactive WIP on.
git -C "$cwd" stash 2>/dev/null || true
git -C "$cwd" fetch --quiet origin
git -C "$cwd" checkout -b "todo/$id" origin/main      # or your base branch
```
Then build the MVP — this is the rule, not an aside. Implement the smallest coherent
slice that delivers the task's core and can be reviewed. Resist gold-plating: no extra
config, no adjacent refactors, no "while I'm here." If the task is big, ship the thinnest
end-to-end version and let the follow-ups grow it. Run whatever tests/build exist for the
touched area.

## Step 10 — Open the PR and move the task to review
```bash
git -C "$cwd" add -A && git -C "$cwd" commit -m "<type>: <task title>

<one line on what this minimal slice does; ends with the devbrain recap rule>"
git -C "$cwd" push -u origin "todo/$id"
pr_url="$(gh pr create --base main --title "<task title>" --body "<what/why · acceptance · scope · what's deferred>")"
```
**Commit subjects are conventional:** prefix the task title with its type —
`feat` / `fix` / `docs` / `test` / `refactor` / `chore` (scope optional: `fix(queue):`) —
so agent-driven history stays scannable. The **PR title stays the plain task title** — no
type prefix, and do **not** append "(MVP)" to either. Build-small is the working philosophy
(Step 9), not a label; note what's deferred in the PR body instead. The PR body also
**restates the task's `Acceptance:` line and says how the slice meets it** (no acceptance
note → state the one-line standard you judged by).

Then move the task to **review**, recording the PR — do NOT mark it done yet:
```bash
devbrain todo review "$id" "$pr_url"   # open->...->review: hidden from next/list, but not done until merge
```
The task is only `done` once its PR **merges** — it stays in `review` until then. It gets
closed one of two ways:
- **Default path (tell the agent):** when the PR merges, the user says so and the agent
  runs `devbrain todo done "$id"`. So **end the run by reminding the user** (Step 11): name the
  open PR + its task and say "tell me when it merges (or just re-run `/continue`) and I'll
  mark it done."
- **Inferred path:** the next `/continue` runs `/distill`'s queue-reconcile step (Step 4),
  which checks review-tasks' PRs with `gh` and proposes closing the merged ones — **after
  asking you to confirm**, never silently.

(If you hit a real blocker mid-task, `devbrain todo release "$id"` and explain — don't leave it
dangling as `taken`.)

## Step 11 — Ask follow-ups, then recap
The MVP is a starting point, not the finish. End your turn by asking the user the 2-4
questions that decide the next iteration: scope to grow, edge cases to handle, choices you
made by judgement that they should confirm. Their answers become the *next* tasks (you or
`/distill` queue them). Include the merge reminder from Step 10 here.

Then the one-sentence recap (devbrain's transcript sweep logs it): name the task + the PR you
opened.
