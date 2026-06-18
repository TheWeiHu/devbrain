---
name: continue
description: |
  devbrain resume cursor (Stage C — Assemble) that also works the queue. First
  folds new prompt-log entries into the project's brain pages AND extracts open
  items into the TODO queue, pulls the brain, refreshes the live world
  (git/issues/CI), and gives a short briefing. Then it picks up the highest-priority
  task, builds a MINIMAL MVP for it, opens a PR for review, and asks follow-up
  questions. Loop it with `/loop /continue` to keep draining the queue. Use when
  asked to "continue", "resume", "where was I", "pick up where I left off", "work
  the next task", or "what's the state of this".
---

# /continue — fold in, then assemble the right amount of context

You are resuming work. devbrain's job here is **subtraction, not stuffing**: first
make sure last session's knowledge is captured, then pull only what's relevant and
hand back a short briefing. The raw log is the source of truth; the brain is a
queryable projection of it — so auto-writing pages here is safe (a bad page is
reverted; the log is never touched).

## Step 1 — Resolve identity (mechanical, from the working repo)
```bash
cwd="$(pwd)"
DATA="${DEVBRAIN_DATA:-$HOME/devbrain-data}"
# Resolve identity via the shared OFFLINE resolver so capture, todo.sh, and the
# skills all agree on the projects/<owner>__<repo> folder.
PK="$HOME/.claude/hooks/devbrain-project-key.sh"; [ -f "$PK" ] || PK="$cwd/hooks/project-key.sh"
. "$PK"; project="$(devbrain_project_key "$cwd" "$DATA")"
branch="$(git -C "$cwd" branch --show-current 2>/dev/null)"
LOGDIR="$DATA/projects/$project/log"
BRAINDIR="$DATA/projects/$project/brain"
echo "project=$project branch=$branch"
```

## Step 2 — Sync the data repo
Pull logs/pages other machines pushed.
```bash
git -C "$DATA" pull --rebase --autostash --quiet 2>/dev/null || true
```

## Step 3 — Fold in new log (run the /distill protocol)
**Run the `/distill` skill's protocol now** (Steps 2-5 of `~/.claude/skills/distill/SKILL.md`):
find log entries newer than the ledger cursor, distill them into topic pages, write
them directly (no gate), load gbrain, and advance the ledger. `/distill` is the
single source of truth for *how* fold-in works — do not duplicate its logic here;
follow it.

`$DATA`, `$project`, `$LOGDIR`, `$BRAINDIR` are already resolved (Steps 1-2), so
skip distill's Step 1 and start from its "read what's new" step. If there are no
new log entries, say so and move on.

## Step 4 — Read the brain (project-biased, not project-walled)
Two rules: **(1)** name `$project` in the query so its pages rank up — a bias, not a
filter; **(2)** read the top hits **as-is** — do *not* `grep` to `^<project>/`, so
shared cross-project pages (coding styles, review conventions) still surface. This
project's own pages are always on disk under `$BRAINDIR`, so the query is for
ranking/discovery, not fencing. (Semantic `gbrain query` needs `OPENAI_API_KEY`;
without it, or if it returns nothing, fall back to keyword `gbrain search`.)

```bash
Q="$project — ${branch:-$project}: state, recent decisions, open items, conventions"
ranked=""
[ -n "$OPENAI_API_KEY" ] && ranked="$(gbrain query "$Q" 2>/dev/null)"   # hybrid semantic
case "$ranked" in ""|*"No results"*) ranked="$(gbrain search "$project" 2>/dev/null)";; esac
printf '%s\n' "$ranked" | head -20      # read as-is — no <project>/ filter
```
Read the top 1-3 pages in full (`gbrain get "<slug>"`); pull cross-project hits in only
when they're relevant (e.g. shared conventions). Every `gbrain` call here is logged
automatically by the `PostToolUse(Bash)` hook to `projects/<project>/gbrain-queries.log`
— you don't call any wrapper; just use `gbrain` normally.

## Step 5 — Refresh the live world
Status lives in the world, never invented.
```bash
git -C "$cwd" fetch --quiet 2>/dev/null || true
git -C "$cwd" status -sb | head -20
git -C "$cwd" log --oneline -5
command -v gh >/dev/null && gh issue list --limit 10 2>/dev/null || true
command -v gh >/dev/null && gh pr status 2>/dev/null || true
```

## Step 6 — Brief the user (short)
A few lines, then move straight into the work:
- **Folded in:** N new brain pages + M new queue tasks from last session (or
  "nothing new"); "review with `git -C "$DATA" diff`" if anything was written.
- **Where you are:** project, branch, and the task the branch implies.
- **From the brain:** the 2-4 most relevant in-scope facts/decisions/open items
  (with page slug pointers, e.g. `<project>/<topic>`).
- **From the world:** uncommitted changes, ahead/behind, open issues/PRs, CI.
- **Top of the queue:** the highest-priority task you're about to pick up.

Briefing plus pointers — do not dump whole pages. The flusher pushes pages/tasks you
wrote in Step 3 automatically (every 5 min); no manual git needed.

## Step 7 — Work the top task → minimal-MVP PR → follow-ups
The queue exists to be drained. After the briefing, pull the highest-priority task
and do it. This is the heart of `/continue` — not just orienting, but moving.

```bash
TODO="$HOME/.claude/hooks/devbrain-todo.sh"; [ -x "$TODO" ] || TODO="$cwd/scripts/todo.sh"
id="$("$TODO" next)"          # highest-priority open task id (empty if queue empty)
```

1. **Empty queue?** If `id` is empty, there's nothing to do — say so and stop
   (this also ends a `/loop /continue`). Don't invent work.
2. **Claim it** so parallel workspaces don't collide:
   ```bash
   "$TODO" claim "$id"        # exit 2 → someone else grabbed it; re-run `next` and try the following one
   "$TODO" show "$id"         # read the full task: H1 = goal, body = why / acceptance criteria
   ```
3. **Prime context for THIS task — query the brain before you build.** Step 4's read
   oriented you on the *project*; now pull what's relevant to *this task* so you don't
   re-derive a decision already made or miss a convention. Run a FEW focused queries
   off the task's goal and keywords — aim for 2-4, and stop early once nothing new
   surfaces (each call is logged by the `PostToolUse(Bash)` hook; no wrapper needed):
   ```bash
   title="$("$TODO" show "$id" | sed -n 's/^# //p' | head -1)"
   qmode=query; [ -n "$OPENAI_API_KEY" ] || qmode=search    # query needs an OpenAI key; else keyword search
   for q in "$title" "$project conventions" "decisions and prior work related to $title"; do
     echo "── $q"; gbrain "$qmode" "$q" 2>/dev/null | head -8
   done
   ```
   Read the most relevant hits in full (`gbrain get "<slug>"`) before writing code:
   existing decisions, file/naming conventions, and related implementation pages are
   what keep the MVP consistent with what's already there. Prefer this over asking the
   user for context the brain may already record.
4. **Branch off the base.** Start clean from the target branch (don't pile onto an
   unrelated WIP branch):
   ```bash
   git -C "$cwd" stash -u 2>/dev/null || true
   git -C "$cwd" fetch --quiet origin
   git -C "$cwd" checkout -b "todo/$id" origin/main      # or your base branch
   ```
5. **Build a MINIMAL MVP — this is the rule, not an aside.** Implement the smallest
   coherent slice that delivers the task's core and can be reviewed. Resist
   gold-plating: no extra config, no adjacent refactors, no "while I'm here." If the
   task is big, ship the thinnest end-to-end version and let the follow-ups grow it.
   Run whatever tests/build exist for the touched area.
6. **Open the PR for review.**
   ```bash
   git -C "$cwd" add -A && git -C "$cwd" commit -m "<task title>

   <one line on what this minimal slice does; ends with the devbrain recap rule>"
   git -C "$cwd" push -u origin "todo/$id"
   pr_url="$(gh pr create --base main --title "<task title>" --body "<what/why · scope · what's deferred>")"
   ```
   Use the plain task title — do **not** append "(MVP)" to the PR title or commit
   subject. Build-small is the working philosophy (step 5), not a label; note what's
   deferred in the PR body instead.
   Then move the task to **review**, recording the PR — do NOT mark it done yet:
   ```bash
   "$TODO" review "$id" "$pr_url"   # open->...->review: hidden from next/list, but not done until merge
   ```
   The task is only `done` once its PR **merges** — it stays in `review` until then.
   It gets closed one of two ways:
   - **Default path (tell the agent):** when the PR merges, the user says so and the
     agent runs `"$TODO" done "$id"`. So **end the run by reminding the user**: name
     the open PR + its task and say "tell me when it merges (or just re-run
     `/continue`) and I'll mark it done."
   - **Inferred path:** the next `/continue` runs `/distill` step 3c, which checks
     review-tasks' PRs with `gh` and proposes closing the merged ones — **after asking
     you to confirm**, never silently.
   (If you hit a real blocker mid-task, `"$TODO" release "$id"` and explain — don't
   leave it dangling as `taken`.)
7. **Ask follow-up questions.** The MVP is a starting point, not the finish. End your
   turn by asking the user the 2–4 questions that decide the next iteration: scope to
   grow, edge cases to handle, choices you made by judgement that they should confirm.
   Their answers become the *next* tasks (you or `/distill` queue them). Include the
   merge reminder from step 6 here. Then the one-sentence recap (devbrain's Stop hook
   logs it): name the task + the PR you opened.

**One task per `/continue`.** Drain the rest with `/loop /continue` — each run picks
up the next, opens its own MVP PR, and asks its own follow-ups.
