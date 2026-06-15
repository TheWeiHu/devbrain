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

## Stages

**A — Capture** (dumb, automatic)
- `UserPromptSubmit` hook appends every prompt verbatim — no model, never fails.
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
- A priority-ranked backlog of tasks, **one markdown file per task** with YAML
  frontmatter, under `~/devbrain-data/projects/<project>/todo/<id>.md`. Same
  file-per-unit sharding as the log: different tasks = different files = no merge
  conflict, so the queue syncs by plain `git pull` (the flusher pushes it). After
  `tk`/cullback-ticket — the file *is* the ticket, git *is* the database, no service.
- Frontmatter: `id · status(open|taken|done) · priority(0-100) · created ·
  claimed_by · claimed_at · deps[] · tags[]`. `next` returns the top-priority
  **open + ready** task (all `deps` done), skipping `taken`/`blocked`.
- Driver: the `devbrain-todo` CLI (`scripts/todo.sh`, installed to `~/.claude/hooks`).
  Consumed by `/work` (claim → do → close, one task per run) and `/loop /work` (drain).
  `/continue` surfaces the top ready tasks on resume.

## Principles

- **Concurrency — no locks** (after `tk`): one worktree ↔ one branch ↔ one issue.
  **Branch existence is the claim** for *code*. Logs shard per session
  (conflict-free); brain facts append-only, projected newest-wins. Real code
  overlap is a git merge.
- **Queue claiming is the one explicit lock** — but a thin one, in keeping with the
  no-lock spirit. To stop parallel agents grabbing the same TODO, `claim` flips a
  task `open → taken`. The durable claim is the committed frontmatter (git push
  ordering arbitrates across machines); a local `mkdir` guard only serializes the
  read-modify-write so two worktrees on one machine can't both flip the same file.
  No lock server, no daemon — just a file and git, like everything else.
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
Two layers. For *code*, not in gbrain: `git checkout -b feat/issue-N` *is* the
claim; first push / issue-assignment wins. gbrain only mirrors advisory status,
refreshed from the world. For *queue* coordination, `devbrain-todo claim` flips a
task `open → taken` (atomic `mkdir` guard locally; git push ordering across
machines) so parallel `/work` agents pull *different* tasks — see Stage D.

**Q: Won't two machines claim the same TODO before the flusher syncs?**
Possible but rare and self-healing. The flush cadence is ~5 min, so two machines
*can* both claim the same task in that window; the second push hits a conflict on
that one file and the rebase surfaces it (one `taken` line vs another) — newest
claimant wins, the loser re-pulls and `next` hands it a different task. The blast
radius is one file, never the whole queue, because tasks are sharded per file. If
you need hard single-claim, run one shared brain (Supabase) — same trade-off as
the brain itself.

**Q: How do the logs sync across machines?**
`git push`/`pull` of `~/devbrain-data`. Per-session sharding means one writer per file,
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
(`git -C "$cwd" remote`) and writes *to* `~/devbrain-data/...`. The two repos never
entangle — devbrain is a sibling at a fixed home path (no nesting, no submodule),
so an OSS repo's git never sees the prompts. A **single per-machine flusher**
commits/pushes devbrain-data explicitly via `git -C ~/devbrain-data` — never inheriting cwd.
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
