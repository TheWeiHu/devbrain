---
name: todo
description: |
  devbrain TODO queue — a priority-ranked, git-synced backlog of tasks for this
  project that any agent (or machine) can pull from. File-per-task markdown under
  the data repo; concurrency-safe claiming. Use when asked to "add a todo", "show
  the queue", "what's next", "queue this up", "prioritize", or "/todo". To actually
  *do* the next task, use `/work` (or `/loop /work` to drain the queue).
---

# /todo — manage the project work queue

Each TODO is one markdown file with YAML frontmatter under
`$DEVBRAIN_DATA/projects/<project>/todo/<id>.md` — the same conflict-free,
file-per-unit sharding the prompt log uses, so the queue syncs across machines by
plain `git pull` and the flusher's push. After cullback/ticket: the file *is* the
ticket, git *is* the database, no service. devbrain adds an explicit **claim**
(`open` → `taken`) so parallel agents don't grab the same task.

Driver: `devbrain-todo` at `~/.claude/hooks/devbrain-todo.sh` (fallback:
`scripts/todo.sh` in the system repo). Identity (which project's queue) comes from
the working repo's git remote, exactly like capture — run it from any worktree of a
repo and you hit one shared queue.

## Commands
```bash
devbrain-todo add "<title>" [-p N] [-t tag] [-d depid] [-b "body"]  # create (prints id); -p 0..100
devbrain-todo list [--all] [--json]   # open todos, priority desc (--all incl. taken/done)
devbrain-todo ready                   # open todos whose deps are all done
devbrain-todo blocked                 # open todos still waiting on a dep
devbrain-todo next [--json]           # the single top ready task (what /work pulls)
devbrain-todo show  <id>
devbrain-todo claim <id> [--by WHO]   # atomic open -> taken (exit 2 if already taken)
devbrain-todo done  <id>              # close it
devbrain-todo release <id>            # taken -> open (un-claim)
devbrain-todo reopen  <id>            # done -> open
devbrain-todo rm <id>
```

## Model
- **Priority** is a 0–100 score; `next`/`list` sort high→low, ties broken FIFO by
  creation time.
- **Deps** gate readiness: a task with an unfinished `-d <id>` dependency is
  `blocked` and never returned by `next` until the dep is `done`.
- **Status** is `open | taken | done`. Claiming is the only concurrency primitive —
  it's atomic locally (mkdir lock) and git arbitrates across machines.
- **Provenance**: tasks distilled from the log or born in conversation can carry a
  pointer in their body; closing a task pairs well with `/distill`.

When the user describes work to queue, translate it into one `add` per discrete
task with sensible priorities and dependency edges, then show the resulting `list`.
