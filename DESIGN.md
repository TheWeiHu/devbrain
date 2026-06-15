# devbrain — Design

**Thesis:** One job — turn the prompts you write into a brain an agent resumes
from. *The log is the agent.* Markdown + git is the source of truth; everything
else is a rebuildable projection. (After `tk` / cullback-ticket: "records intent
— you execute it.")

**Pipeline:** raw log → brain → assembled context → `/continue`

**Golden rule:** every stage downstream of the raw log is disposable and
re-derivable. Lose the brain → rebuild from the log. Never lose the log.

**Two repos (2026-06-14):** this **system** repo (`devbrain`) holds the design +
tooling and no personal data; the **data** repo (`devbrain-data`, private, at the
fixed home `~/Desktop/devbrain-data`) holds the markdown brain. Paths below that read
`~/Desktop/devbrain-data` are the data home; the capture hook and flusher target it.

## Stages

**A — Capture** (dumb, automatic)
- `UserPromptSubmit` hook appends every prompt verbatim — no model, never fails.
- Append-only markdown, **one file per session per day**:
  `~/Desktop/devbrain-data/projects/<project>/log/<YYYY-MM-DD>/<worktree>.<session-id>.md`
- Split by **mechanical keys (project / date / session), never by topic** — topic
  lives in the brain. `<project>` = git remote of cwd (worktrees collapse to one);
  `<session-id>` = one writer per file (conflict-free git merge). File = a session's
  day; entry = one turn. Lossless. Sacred.

**B — Brain** (gbrain)
- Distilled tasks / requirements / assumptions as linked, tagged gbrain pages
  (Postgres + graph + hybrid search, MCP).
- Each fact carries **provenance** (→ log / issue). Append events; never rewrite
  in place.
- Curation is **explicit**: `/checkpoint` distills new log → proposes pages →
  you approve. No magic inference.

**C — Assemble** (the right amount)
- `/continue`: resolve project → resolve task (branch→issue) →
  `gbrain query "<task>" --detail low` → refresh world (`git fetch`, `gh issue`,
  CI) → small briefing + pointers.
- Subtraction, not stuffing. Progressive disclosure via the `--detail` dial.

## Principles

- **Concurrency — no locks** (after `tk`): one worktree ↔ one branch ↔ one issue.
  **Branch existence is the claim.** Logs shard per session (conflict-free);
  brain facts append-only, projected newest-wins. Real code overlap is a git merge.
- **State:** tasks are **open/closed**. Status lives in the world, never invented.
- **Wiring is per-machine, not per-repo:** the capture hook, gbrain MCP, the
  `/continue` skill, and the standing instruction all live in `~/.claude`; the
  brain data lives in `~/Desktop/devbrain-data`. The working repo (incl. OSS repos) stays clean.

## Q&A

**Q: What's the source of truth?**
The raw prompt logs (markdown in git). The brain, the index, and the assembled
context are all rebuildable from them.

**Q: What is gbrain's role?**
The queryable brain (stages B + C): linked pages, semantic search, the "right
amount" `--detail` dial, MCP access. Not the source of truth and not the lock —
a fast, rebuildable projection.

**Q: How are tasks locked across worktrees?**
Not in gbrain. `git checkout -b feat/issue-N` *is* the claim; first push /
issue-assignment wins. gbrain only mirrors advisory status, refreshed from the
world.

**Q: How do the logs sync across machines?**
`git push`/`pull` of `~/Desktop/devbrain-data`. Per-session sharding means one writer per file,
so pulls only *add* files — never a content conflict. Durability ladder: append
locally (instant) → background flusher commits/pushes (off-machine).

**Q: Is the brain synced too?**
No. It's per-machine, rebuilt via `gbrain import` from the synced logs. `/continue`
does `git pull` *then* `import`.

**Q: How long to rebuild the brain?**
Seconds at small size. At scale: `import --no-embed` is instant (keyword + graph
usable immediately); embeddings backfill in the background (~minutes for ~10k
chunks, pennies via the OpenAI embedder). `sync` / `embed --stale` keep it
incremental — full cost paid only once per new machine.

**Q: PGLite or Supabase?**
PGLite local by default (you own the file). Supabase only if you want one shared
*live* brain *and* gbrain-mediated leasing — accepting a hosted-DB dependency.

**Q: Prompting in a *different* repo — how does it write to the brain?**
By **absolute path**: the hook reads identity *from* the working repo
(`git -C "$cwd" remote`) and writes *to* `~/Desktop/devbrain-data/...`. The two repos never
entangle — devbrain is a sibling at a fixed home path (no nesting, no submodule),
so an OSS repo's git never sees the prompts. A **single per-machine flusher**
commits/pushes devbrain-data explicitly via `git -C ~/Desktop/devbrain-data` — never inheriting cwd.
Split paths: hook *appends* (lock-free, instant); flusher *commits* (serialized,
avoids `index.lock` contention).

**Q: How do agents in *other* repos know to read the brain?**
Per-machine wiring, mirroring capture: (1) **gbrain MCP** registered in
`~/.claude/settings.json` → the query tool exists in every session; (2) a standing
line in **`~/.claude/CLAUDE.md`** → the agent knows to query the project's brain on
resume; (3) a user-level **`/continue` skill** → the protocol, invokable anywhere.
Routing is by git remote → `project/<slug>`. Optional `SessionStart` hook injects
a *tiny* nudge ("brain for X: N open tasks — /continue"); the full load stays on
explicit `/continue` (budget + explicit-over-magic).

**Q: How are prompts broken into files?**
By three mechanical keys: `projects/<project>/log/<YYYY-MM-DD>/<worktree>.<session-id>.md`.
One file per session per day (one writer → conflict-free sync); a prompt is an
appended *entry*, not its own file. Split by **where/when you worked, never by
topic** — capture can't know topic without a model, and topic isn't collision-free.
Topic grouping is the brain's job: `/checkpoint` re-routes knowledge from these
session files into topic pages. (So this conversation logs under `redlens/` but
distills into `devbrain` pages.) "All prompts by date" is a read-time projection:
merge a day's session files, sort by in-file timestamps.
