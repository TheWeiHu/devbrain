---
name: work
description: |
  devbrain worker — pull the highest-priority ready TODO off this project's queue,
  claim it (so parallel agents don't collide), do the work, and close it. One
  iteration per invocation; wrap in `/loop /work` to drain the queue continuously.
  The queue is file-per-task markdown under the data repo, managed by the
  `devbrain-todo` CLI. Use when asked to "work the queue", "pull a todo", "pick up
  the next task", or "/work".
---

# /work — claim the next TODO and do it

devbrain's TODO queue is a priority-ranked, git-synced backlog any agent can pull
from (see `/todo` and the `devbrain-todo` CLI). `/work` is the worker loop body:
it grabs **one** task, claims it atomically (status `open` → `taken`, so no two
agents take the same one), executes it, and marks it `done`. Run it once for a
single task, or `/loop /work` to keep draining until the queue is empty.

`devbrain-todo` is installed at `~/.claude/hooks/devbrain-todo.sh`. If that path
doesn't exist, fall back to `scripts/todo.sh` in the devbrain system repo, or tell
the user to run `./setup`.

## Protocol

### 1. Pull the next ready task
```bash
TODO="$HOME/.claude/hooks/devbrain-todo.sh"; [ -x "$TODO" ] || TODO="$(command -v devbrain-todo || echo "$TODO")"
"$TODO" next --json
```
`next` returns the highest-`priority` task that is **open** and **ready** (all its
`deps` are `done`), skipping anything already `taken` or `blocked`. If it prints
`null` / "queue empty", there's nothing to do — **say so and stop** (this also ends
a `/loop`). Do not invent work.

### 2. Claim it (the concurrency gate)
```bash
"$TODO" claim <id>
```
Claiming flips `open` → `taken` under an atomic `mkdir` lock and records
`claimed_by` (this worktree/session/host). If claim exits non-zero (code 2),
someone else took it between step 1 and now — go back to step 1 and pull the next
one. **Never work an unclaimed task**; the claim is what makes parallel `/work`
agents safe.

### 3. Do the work
Read the full task — `"$TODO" show <id>` — and execute it for real: the H1 is the
goal, the body is the spec / acceptance criteria. Use the rest of your tools. If
the task is underspecified, use judgement; if it's genuinely blocked (missing
dependency you discover mid-flight), **release it** so another run can revisit:
```bash
"$TODO" release <id>     # taken -> open; clears the claim
```
and explain what blocked it (optionally `devbrain-todo add` a prerequisite task
with a `-d` dependency edge).

### 4. Close it
On success:
```bash
"$TODO" done <id>
```
Then, if the work produced durable knowledge, consider `/distill` so the brain
learns from it. Commit/PR the code change per the user's normal flow.

### 5. Recap + loop
End with a one-sentence recap naming the task id and outcome (devbrain's Stop hook
logs it). If invoked under `/loop`, the next iteration re-enters at step 1 and
pulls the following task; the loop ends naturally when `next` returns empty.

## Notes
- **One task per invocation.** Don't batch — claiming one at a time keeps the queue
  honest about what's in flight and lets other agents grab the rest in parallel.
- **Priority is advisory ordering, not permission.** `next` always hands you the
  top ready task; trust it rather than scanning the whole list.
- Add tasks with `devbrain-todo add "<title>" -p <0-100> [-d <dep-id>] [-t <tag>]`.
