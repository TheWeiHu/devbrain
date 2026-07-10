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
fixed home `~/devbrain-data`) holds the markdown brain. Paths below that read
`~/devbrain-data` are the data home; the capture hook and flusher target it.

**Runtime (2026-07):** everything ships as **one Go binary**. Hooks are
`devbrain hook <event>` commands registered in the agent's `settings.json` — no
script copies under `~/.claude/hooks`, no path-pinning of installed files — and
the dashboard server is `devbrain dashboard`. Config lives at
`~/.config/devbrain/config.json`; the only runtime requirement is git. The
behavior of the retired bash/python implementation is frozen as goldens under
`testdata/golden/` (see its README).

## Stages

**A — Capture** (dumb, automatic)
- `UserPromptSubmit` hook appends every Claude Code or Codex prompt verbatim — no model, never fails.
- Append-only markdown, **one file per session per day**:
  `~/devbrain-data/projects/<project>/log/<YYYY-MM-DD>/<worktree>.<session-id>.md`
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

**D — Queue** (what's next, vs. the brain's what-happened)
- A priority-ranked backlog of tasks, **one markdown file per task** with flat
  frontmatter, under `~/devbrain-data/projects/<project>/todo/<id>.md`. Same
  file-per-unit sharding as the log: different tasks = different files = no merge
  conflict, so the queue syncs by plain `git pull` (the flusher pushes it). After
  `tk`/cullback-ticket — the file *is* the ticket, git *is* the database, no service.
- Core frontmatter: `id · status(open|taken|review|held|done) · priority(0-100) ·
  created · claimed_by`. An optional versioned contract adds `task_type`,
  `depends_on`, `conflict_keys`, and `budget_turns`; the Markdown body keeps the
  human outcome/scope/acceptance/verification contract.
- **Sources = `/distill` and the nightshift planning turn.** Distill extracts
  user-authored open items from the log; an explicitly started forever-run may add
  contract-validated objective gaps when its queue empties. Both dedupe first.
- **Sink = `/continue`.** After briefing, `/continue` claims the top task, builds a
  **minimal MVP**, opens a PR and marks the task `review`; it becomes `done` only
  after merge. Interactive runs ask the
  follow-up questions whose answers become the next tasks. `/loop /continue` drains
  the queue, one MVP PR per task.
- **Claim = atomic eligibility + status flip** (`open → taken`). `claim-next` uses
  a short local file lock, rechecks dependency/conflict eligibility, and writes the
  claim before another worker can select overlapping work. No lock service or daemon
  is introduced. Legacy tasks remain runnable in shadow mode.

## Principles

- **Concurrency — shard first** (after `tk`): one worktree ↔ one branch ↔ one issue.
  **Branch existence is the claim** for *code*. Logs shard per session
  (conflict-free); brain facts append-only, projected newest-wins. Real code
  overlap is a git merge.
- **Queue claiming is locally atomic, still service-free.** Task files remain the
  durable state and git remains the sync mechanism. A short host-local lock only
  serializes selection + claim among concurrent workers sharing that queue.
- **State:** brain/world tasks are **open/closed**; queue tasks add an in-flight
  `taken` between them. Status lives in the world (or the task file), never invented.
- **Wiring is per-machine, not per-repo:** the capture hook, gbrain MCP, the
  `/continue` skill, and the standing instruction all live in `~/.claude`; the
  brain data lives in `~/devbrain-data`. The working repo (incl. OSS repos) stays clean.

## Q&A

**Q: What's the source of truth?**
The raw prompt logs (markdown in git). The brain, the index, and the assembled
context are all rebuildable from them.

**Q: What is gbrain's role?**
The queryable brain (stages B + C): linked pages, semantic search, the "right
amount" `--detail` dial, MCP access. Not the source of truth and not the lock —
a fast, rebuildable projection.

**Q: How are tasks locked across worktrees?**
For *code*, not in gbrain: `git checkout -b feat/issue-N` *is* the claim; first push
wins. For the *queue*, `devbrain todo claim` flips a task `open → taken` so a
parallel `/continue` skips it (Stage D) — a soft signal, not a hard lock. Two runs
racing the same task is rare and self-evident; harden it only if it bites.

**Q: How do the logs sync across machines?**
`git push`/`pull` of `~/devbrain-data`. Per-session sharding means one writer per file,
so pulls only *add* files — never a content conflict. Durability ladder: append
locally (instant) → background flusher commits/pushes (off-machine).

**Q: Is the brain synced too?**
No. It's per-machine, rebuilt from the synced pages by `devbrain rebuild`, which
`gbrain put`s each brain page under its canonical `<project>/<page>` slug. `/continue`
pulls the data repo, then `/distill` re-puts the pages it folds in. Do **not** run
`gbrain import`/`gbrain sync` on the data dir — those slug pages by file path
(`projects/<project>/brain/<page>`) and index raw logs, creating duplicate entries
under a second slug scheme.

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
(`git -C "$cwd" remote`) and writes *to* `~/devbrain-data/...`. The two repos never
entangle — devbrain is a sibling at a fixed home path (no nesting, no submodule),
so an OSS repo's git never sees the prompts. A **single per-machine flusher**
commits/pushes devbrain-data explicitly via `git -C ~/devbrain-data` — never inheriting cwd.
Split paths: hook *appends* (lock-free, instant); flusher *commits* (serialized,
avoids `index.lock` contention).

**Q: How do agents in *other* repos know to read the brain?**
Per-machine wiring, mirroring capture: (1) the **`nudge` component** registers a
`SessionStart` hook → at the start of every session in a tracked repo it injects a
*tiny* project-specific line ("project X has N brain pages and M open tasks — query
`gbrain search` before answering or asking"), arriving exactly when the model forms
its plan; (2) a standing line in **`~/.claude/CLAUDE.md`** → the agent knows to query
the project's brain on resume; (3) a user-level **`/continue` skill** → the protocol,
invokable anywhere. Routing is by git remote → `project/<slug>`. The nudge is a
reminder, not a query: it never runs gbrain itself (no latency, no cost, no stale
injection) and the full load stays on explicit `/continue` (budget +
explicit-over-magic). gbrain is installed as a **CLI** (`bun add -g gbrain`), invoked
via Bash — devbrain does **not** register it as an MCP server, which keeps the query
trace (the `PostToolUse(Bash)` logger) intact and avoids a per-session tool tax.
This is also the durable fix for **PGLite lock contention**: a *global* `gbrain serve`
MCP server (top-level `mcpServers` in `~/.claude.json`) spawns one daemon **per
workspace** against the single shared `~/.gbrain/brain.pglite`; PGLite is single-writer,
so the daemons deadlock on the lock ("Timed out waiting for PGLite lock"). The CLI
opens the DB, does the op, and exits — no resident daemon, nothing to contend. `install`
therefore *warns* (never auto-removes) if a global `gbrain` MCP server is present, with
the `claude mcp remove gbrain` fix. If interactive MCP is ever required, register it
**project-scoped, never global**.

**Q: How are prompts broken into files?**
By three mechanical keys: `projects/<project>/log/<YYYY-MM-DD>/<worktree>.<session-id>.md`.
One file per session per day (one writer → conflict-free sync); a prompt is an
appended *entry*, not its own file. Split by **where/when you worked, never by
topic** — capture can't know topic without a model, and topic isn't collision-free.
Topic grouping is the brain's job: `/checkpoint` re-routes knowledge from these
session files into topic pages. (So this conversation logs under `redlens/` but
distills into `devbrain` pages.) "All prompts by date" is a read-time projection:
merge a day's session files, sort by in-file timestamps.
