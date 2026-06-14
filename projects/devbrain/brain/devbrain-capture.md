# devbrain — Stage A: Capture

The raw prompt log is the **source of truth** — lossless, append-only, sacred.

**Mechanism:** a model-free `UserPromptSubmit` hook (Claude Code) appends every
prompt verbatim with a timestamp. No LLM, no judgment, never fails. Response
summaries are written by the main agent (not a separate Haiku call), but the
prompt capture is guaranteed independently.

**Layout — one file per session per day:**
`~/devbrain/projects/<project>/log/<YYYY-MM-DD>/<worktree>.<session-id>.md`
- Split by **three mechanical keys: project / date / session.**
- `<project>` = git **remote** of cwd, so all worktrees of a repo collapse to one.
- `<session-id>` = the shard key: one session = one process = exactly one writer
  per file → append-only → conflict-free under git merge (pulls only *add* files).
- File = a session's day; **a prompt is an appended entry, not its own file.**

**Never split by topic.** You can't know a prompt's topic at capture time without
a model (breaks dumb capture), and topic isn't collision-free. The log is sharded
by *where and when you worked*; **topic grouping is the brain's job** — the
`/checkpoint` distill step routes knowledge from these session files into topic
pages, regardless of which repo's log it came from. (This conversation logs under
`redlens/` but distills into `devbrain` pages.) "All prompts by date" is a
read-time projection: merge a day's session files, sort by in-file timestamps.

**Cross-repo write:** the hook reads identity *from* the working repo
(`git -C "$cwd" remote`) and writes *to* the absolute `~/devbrain` path. The two
git repos never entangle; prompts physically cannot leak into an OSS repo.

**Durability ladder:** append locally (durable on this machine instantly) →
a single per-machine flusher commits/pushes (`git -C ~/devbrain ...`) for
off-machine durability.

Source: design conversation, 2026-06-13.
See also: [[project/devbrain-overview]], [[project/devbrain-concurrency-sync]].
