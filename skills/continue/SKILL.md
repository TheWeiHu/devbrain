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
remote="$(git -C "$cwd" remote get-url origin 2>/dev/null)"
if [ -n "$remote" ]; then project="$(basename "${remote%.git}")"; else project="$(basename "$cwd")"; fi
project="$(printf '%s' "$project" | tr '[:upper:] ' '[:lower:]-' | tr -cd '[:alnum:]._-')"
branch="$(git -C "$cwd" branch --show-current 2>/dev/null)"
DATA="${DEVBRAIN_DATA:-$HOME/devbrain-data}"
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

## Step 4 — Read this project's brain (hard-scoped)
Ranking is global (no tag filter), so scope to THIS project's own page slugs from
the filesystem. **Rank with `gbrain query`** (hybrid semantic — matches meaning,
not just literal words, which is what resuming needs) **when an OpenAI key is set,
otherwise fall back to keyword `gbrain search`.** Both print the same
`[score] project/<slug> -- <title>` format, so the slug intersection is identical.

> Why the key gate: `gbrain query` embeds your question via OpenAI, so it only
> works where `OPENAI_API_KEY` is set. **Not every user/machine has one** — and
> that's fine. Semantic search is an *enhancement*, not a requirement: keyword
> `gbrain search` is pure tsvector, needs no key, works offline, and is the
> baseline experience. So we check the key up front and skip straight to keyword
> when it's absent (also fast — keyless `query` fails in ~0.3s, but skipping is
> cleaner and self-documenting for installs). We additionally fall back if a
> key *is* set but `query` still returns nothing (offline / no semantic hit) —
> it prints the literal `"No results."`, so we test for `project/` lines, not
> emptiness. Net: **best available ranking, never empty, never key-required.**

```bash
# Call gbrain through the wrapper so every invocation first reaps orphaned
# `gbrain serve` daemons holding the PGLite lock (see /distill Step 4).
GB="$HOME/.claude/hooks/devbrain-gbrain.sh"; [ -x "$GB" ] || GB="$cwd/scripts/gbrain.sh"
# This project's in-scope slugs:
for f in "$BRAINDIR"/*.md; do [ -e "$f" ] && echo "project/$(basename "$f" .md)"; done
# Rank by relevance. Semantic if a key is configured; keyword otherwise.
Q="${branch:-$project} — what is the state, recent decisions, and open items"
ranked=""
if [ -n "$OPENAI_API_KEY" ]; then
  ranked="$("$GB" query "$Q" 2>/dev/null)"   # hybrid semantic (needs the key)
fi
# Fall back to keyword when there's no key, or query found no `project/<slug>` lines.
# (search is AND across terms, so use "$project" alone — a branch slug like
#  `owner/foo-v1` matches no page and would zero out the results.)
printf '%s' "$ranked" | grep -q 'project/' || ranked="$("$GB" search "$project" 2>/dev/null)"
printf '%s\n' "$ranked" | head -20
```
Read the top 1-3 **in-scope** pages in full (`"$GB" get "project/<slug>"`, or
just read the markdown under `$BRAINDIR`). Ignore pages that belong to other
projects even if they rank high.

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
  (with page slug pointers, e.g. `project/<slug>`).
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
3. **Branch off the base.** Start clean from the target branch (don't pile onto an
   unrelated WIP branch):
   ```bash
   git -C "$cwd" stash -u 2>/dev/null || true
   git -C "$cwd" fetch --quiet origin
   git -C "$cwd" checkout -b "todo/$id" origin/main      # or your base branch
   ```
4. **Build a MINIMAL MVP — this is the rule, not an aside.** Implement the smallest
   coherent slice that delivers the task's core and can be reviewed. Resist
   gold-plating: no extra config, no adjacent refactors, no "while I'm here." If the
   task is big, ship the thinnest end-to-end version and let the follow-ups grow it.
   Run whatever tests/build exist for the touched area.
5. **The final step is ALWAYS a PR — and its body carries the task description.**
   Every task ends as a reviewable PR, never a silent push or a local-only change.
   Put the **full task description verbatim** (the `show` output: H1 goal + body /
   acceptance criteria) into the PR body so a reviewer sees *what was asked*, then
   add what the MVP does, its scope, and what's deferred. Build the body from the
   task itself so it can't drift:
   ```bash
   task="$("$TODO" show "$id")"          # H1 = goal, body = why / acceptance criteria
   git -C "$cwd" add -A && git -C "$cwd" commit -m "<task title> (MVP)

   <one line on what this minimal slice does; ends with the devbrain recap rule>"
   git -C "$cwd" push -u origin "todo/$id"
   gh pr create --base main --title "<task title> (MVP)" --body "$(printf '## Task\n%s\n\n## What this MVP does\n<…>\n\n## MVP scope / deferred\n<…>\n' "$task")"
   ```
   **Do NOT mark the task `done` here.** A task is `done` only when its PR **merges**
   — opening a PR is not finishing. Record the PR on the task and leave it claimed so
   no parallel run re-picks it and it doesn't clutter the open list:
   ```bash
   pr="$(gh pr view --json url -q .url 2>/dev/null)"
   "$TODO" pr "$id" "$pr"     # records pr: <url>; keeps status taken (in-review)
   ```
   Mark `done` on a later run (or by hand) once the PR is merged:
   `gh pr view <n> --json state -q .state` → `MERGED` ⇒ `"$TODO" done "$id"`.
   (If you hit a real blocker mid-task, `"$TODO" release "$id"` and explain — don't
   leave it dangling as `taken`.)
6. **Ask follow-up questions.** The MVP is a starting point, not the finish. End your
   turn by asking the user the 2–4 questions that decide the next iteration: scope to
   grow, edge cases to handle, choices you made by judgement that they should confirm.
   Their answers become the *next* tasks (you or `/distill` queue them). Then the
   one-sentence recap (devbrain's Stop hook logs it): name the task + the PR you opened.

**One task per `/continue`.** Drain the rest with `/loop /continue` — each run picks
up the next, opens its own MVP PR, and asks its own follow-ups.
