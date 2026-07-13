# Architecture

devbrain turns the prompts you write into a durable, queryable brain any agent can
resume from. It's a pipeline of stages, each a rebuildable projection of the one
before it. **Golden rule:** everything downstream of the raw log is disposable and
re-derivable — lose the brain, rebuild it from the log; never lose the log.

```
capture → brain/distill → queue → work → nightshift
```

The runtime is one Go binary: hooks are `devbrain hook <event>` commands
registered in the agent's `settings.json`, the dashboard is `devbrain dashboard`,
and config lives at `~/.config/devbrain/config.json`. Git is the only runtime
requirement.

## Capture

A model-free sweep (`devbrain sweep`, run by every flush) harvests the agents' own
on-disk transcripts — Claude Code's `~/.claude/projects` JSONL and Codex's
`~/.codex/sessions` rollouts — into an append-only markdown log: every prompt
verbatim with a UTC timestamp, plus a `↳` recap line from the agent's final
sentence. No capture hooks: nothing to trust, re-approve, or silently lose (Codex
in particular gates hooks behind a fingerprint that any rewrite invalidates).
Logs are keyed by project / day / session, so one session is the only writer of
its file and syncs conflict-free by plain `git pull`. A one-minute timer sweeps,
commits, and pushes; an idle tick costs milliseconds (mtime cursor + clean tree).

## Brain (`/distill`)

`/distill` reads new log entries and folds them into linked, tagged brain pages —
markdown on disk, queried through gbrain (a per-machine, rebuildable search index).
The same pass extracts actionable open items into the queue. There's no approval
gate: pages are a projection of the log, so review is by `git diff`, not a prompt.

## Queue

A file-per-task markdown backlog of what's next. Tasks are born in `/distill` (its
only writer) and move through `open → taken → review → done` — a task with an open
PR sits in `review`, not `done`, until the PR merges. One file per task means
parallel agents never touch the same file, so the queue syncs conflict-free too.

## Work

`/work` is the lean drain skill: it reads the brain for context, claims the top
task, builds a minimal-MVP slice, and opens a PR — no resume ceremony. `/continue`
is the interactive sibling that also folds the log in and briefs a human first.
Loop either (`/loop /work`) to drain the queue one PR at a time.

## Nightshift

An orchestrator runs N headless `claude` workers in parallel, each in its own git
worktree off `origin/nightshift`, draining the queue overnight. Each finished
branch is green-gated by the test suite, then serially merged into the disposable
`nightshift` branch. You wake to one diff — `git diff main...nightshift` — and
merge it to `main`, or reset and lose only compute.
