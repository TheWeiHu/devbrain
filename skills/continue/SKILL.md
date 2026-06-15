---
name: continue
description: |
  devbrain resume cursor (Stage C — Assemble), now with auto-distill folded in.
  First folds any new prompt-log entries into the project's brain pages, then
  pulls the brain for the current project, refreshes the live world
  (git/issues/CI), and produces a small briefing so you can pick up where you (or
  another machine) left off. Use when asked to "continue", "resume", "where was
  I", "pick up where I left off", or "what's the state of this".
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
**Run the `/distill` skill's protocol now** (Steps 2-4 of `~/.claude/skills/distill/SKILL.md`):
find log entries newer than the last distill, distill them into topic pages, write
them directly (no gate), and load gbrain. `/distill` is the single source of truth
for *how* fold-in works — do not duplicate its logic here; follow it.

`$DATA`, `$project`, `$LOGDIR`, `$BRAINDIR` are already resolved (Steps 1-2), so
skip distill's Step 1 and start from its "read what's new" step. If there are no
new log entries, say so and move on.

## Step 4 — Read this project's brain (hard-scoped)
`gbrain search` is global (no tag filter), so scope to THIS project's own page
slugs from the filesystem. Use search only to rank; keep in-scope hits.
```bash
# This project's slugs:
for f in "$BRAINDIR"/*.md; do [ -e "$f" ] && echo "project/$(basename "$f" .md)"; done
# Rank by relevance, then intersect with the slugs above:
gbrain search "$project ${branch:-overview}" 2>/dev/null | head -20
```
Read the top 1-3 **in-scope** pages in full (`gbrain get "project/<slug>"`, or
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
A few lines:
- **Folded in:** N new pages distilled from last session (or "nothing new"), with
  a "review with `git -C "$DATA" diff`" pointer if anything was written.
- **Where you are:** project, branch, and the task the branch implies.
- **From the brain:** the 2-4 most relevant in-scope facts/decisions/open items
  (with page slug pointers, e.g. `project/<slug>`).
- **From the world:** uncommitted changes, ahead/behind, open issues/PRs, CI.
- **Suggested next action**, one line.

Briefing plus pointers — do not dump whole pages. The flusher pushes any pages you
wrote in Step 3 automatically (every 5 min); no manual git needed.
