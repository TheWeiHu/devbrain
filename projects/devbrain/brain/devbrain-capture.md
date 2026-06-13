# devbrain — Stage A: Capture

The raw prompt log is the **source of truth** — lossless, append-only, sacred.

**Mechanism:** a model-free `UserPromptSubmit` hook (Claude Code) appends every
prompt verbatim with a timestamp. No LLM, no judgment, never fails. Response
summaries are written by the main agent (not a separate Haiku call), but the
prompt capture is guaranteed independently.

**Layout:** `~/devbrain/projects/<project>/log/<date>.<session-id>.md`
- `<project>` is derived from the **git remote** of cwd, so all worktrees of a
  repo collapse to one project.
- **Sharded per session** (globally unique id) → exactly one writer per file →
  append-only → conflict-free under git merge (pulls only *add* files).

**Cross-repo write:** the hook reads identity *from* the working repo
(`git -C "$cwd" remote`) and writes *to* the absolute `~/devbrain` path. The two
git repos never entangle; prompts physically cannot leak into an OSS repo.

**Durability ladder:** append locally (durable on this machine instantly) →
a single per-machine flusher commits/pushes (`git -C ~/devbrain ...`) for
off-machine durability. The more often it pushes, the closer to "every meaningful
state transition durably written."

Source: design conversation, 2026-06-13.
See also: [[project/devbrain-overview]], [[project/devbrain-concurrency-sync]].
