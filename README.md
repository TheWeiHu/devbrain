# devbrain

Turn the prompts you write — in *any* repo — into a durable, queryable brain any
agent can resume from. **The log is the agent.**

devbrain captures every prompt to a private, git-synced store, distills it into a
searchable brain, and lets any future session (or machine) pick up where you left
off. Markdown + git is the source of truth; everything else is a rebuildable
projection.

## How it works

`./setup` wires three things into Claude Code on *this machine* and then gets out of
your way:

- **Capture — automatic, model-free.** A `UserPromptSubmit` hook appends every
  prompt verbatim to a raw markdown log; a `Stop` hook appends a one-line trace of
  what the turn did. No model, never blocks your turn. **This log is the source of
  truth** — everything below is rebuilt from it.
- **Flush — automatic.** A launchd agent commits and pushes that log every 5 min, so
  the brain is durably backed up off-machine and shared across machines.
- **Brain & resume — on demand.** `/distill` folds new log into linked, searchable
  `gbrain` pages **and extracts open items into the TODO queue**; `/continue` pulls
  the relevant pages, refreshes the live world, briefs you, then **picks up the
  highest-priority task, builds a minimal-MVP PR for review, and asks follow-ups**.

```
A. Capture    every prompt → raw markdown log          automatic, model-free · source of truth
B. Brain      /distill → gbrain pages + queue tasks    searchable · a rebuildable projection
C. Assemble   /continue → brief, then work the top     pulls what's relevant, opens an MVP PR
              task as a minimal-MVP PR + follow-ups     · /loop /continue drains the queue
```

Routing is mechanical: the log path is
`projects/<project>/log/<date>/<worktree>.<session>.md`, keyed by **git remote**
(every worktree of a repo collapses to one project), never by topic — topic grouping
is the brain's job. Every distilled fact keeps a provenance pointer back to the log.

**Golden rule:** everything downstream of the raw log is re-derivable — never lose
the log. Full design in [`DESIGN.md`](DESIGN.md).

**Two halves — only one is shipped.** What you install (this repo) is the **system**:
installer, hooks, skills, docs — no personal content. Your prompts and brain pages
live in a *separate* **data store** at `~/devbrain-data` that **you own and maintain**
— `setup` creates it locally on first run; it is not shipped, hosted, or populated
for you. Give it your own private git remote to back it up and sync machines. The
system never holds your data; the data store never holds code.

## Install

**Needs:** [Claude Code](https://claude.ai/code), Git, `jq`, `python3`. The
[`gbrain`](#gbrain--openai-key) engine auto-installs if [`bun`](https://bun.sh) is
present; an OpenAI key is optional (it unlocks semantic search).

```bash
git clone --depth 1 https://github.com/TheWeiHu/devbrain.git ~/.claude/skills/devbrain \
  && cd ~/.claude/skills/devbrain && ./setup
```

`./setup` is idempotent and wires *this machine* (never your working repos): the
capture hooks, the `/continue` and `/distill` skills, a launchd flusher that
commits/pushes the data repo every 5 min, and a standing line in
`~/.claude/CLAUDE.md`. Tear down with `scripts/uninstall.sh` — your data is left
untouched.

The brain lives in `~/devbrain-data` by default. To put it elsewhere — or to clone
an existing brain — set the path up front (works in every context, including when
the command is run by Claude Code or CI):

```bash
DEVBRAIN_DATA=~/path ./setup                               # store the brain elsewhere
DEVBRAIN_DATA_REMOTE=git@github.com:you/brain.git ./setup  # clone an existing brain
```

(Run directly in a terminal, `setup` will also *prompt* for the path; that prompt is
skipped in non-interactive runs — agent/CI/pipe — which just take the default.)

To back up / sync across machines, give the data repo a private remote:
`git -C ~/devbrain-data remote add origin <url>`.

## Daily use

| Command | What it does |
|---|---|
| *(automatic)* | Every prompt is captured; a flusher commits/pushes every 5 min. |
| **`/distill`** | Fold new log → brain pages **and** extract open items → queue tasks (review by git diff). |
| **`/continue`** | Resume: fold in → brief → **work the top task as a minimal-MVP PR + follow-ups**. |
| **`/loop /continue`** | Keep draining the queue — one MVP PR per task until it's empty. |
| `gbrain search "<q>"` | Query the brain from the shell. |
| `devbrain-todo list` | See the queue from the shell. |

## TODO queue

The brain records *what happened*; the queue records *what's next* — a priority-ranked
backlog, **one markdown file per task** under `~/devbrain-data/projects/<project>/todo/`
(same conflict-free, git-synced sharding as the log). **`/distill` fills it** by
extracting open items from the log; **`/continue` drains it** — claims the top task,
builds a minimal MVP, opens a PR, and asks follow-ups. You rarely touch it by hand;
the `devbrain-todo` CLI (`add · list [status] · next · show · claim · review · done ·
release`) is there if you do. Details in [`DESIGN.md`](DESIGN.md).

**Task lifecycle: `open → taken → review → done`.** A task is `taken` when a run
claims it (parallels skip it), then `review` when its PR is open — recording the PR
number, still hidden from `next`/`list`. **An open PR is not shipped work, so the
task is not `done` until the PR merges.** Closing happens two ways:

- **Default (you tell the agent):** `/continue` ends by naming the open PR and its
  task. When the PR merges, tell the agent (or just re-run `/continue`) and it runs
  `devbrain-todo done <id>`.
- **Inferred (with your confirmation):** the next `/continue` runs `/distill`, which
  checks each `review` task's PR via `gh`; for any that merged it **lists them and
  asks you to confirm** before marking them `done` — never silently.

See in-flight tasks with `devbrain-todo list review` (or `list all`).

## gbrain & OpenAI key

The brain lives in **gbrain** (local PGLite by default). `setup` installs it via bun
and initializes a local brain; if bun is missing it prints the one command to run.
Capture works without gbrain — you just can't *query* until it's installed.

Semantic search needs an **OpenAI key** (optional). Without one, search falls back
to keyword + graph ranking (still useful). Add it and backfill embeddings:

```bash
gbrain config set openai_api_key sk-...   # or: export OPENAI_API_KEY=sk-...
gbrain embed --stale
```

## Layout

```
~/.claude/skills/devbrain/      this system repo (installer + tooling)
├── setup                       entrypoint (wraps scripts/install.sh)
├── scripts/                    install · uninstall · flush · rebuild · todo · test · plist
├── hooks/                      capture · capture-response  (→ ~/.claude/hooks)
├── skills/{continue,distill}/  resume-and-work-the-queue · checkpoint-and-extract-tasks
└── DESIGN.md

~/devbrain-data/                the private data repo (source of truth)
└── projects/<project>/{log,brain,todo}/
```

The two repos are separate on purpose: the brain spans every project, the wiring
lives at the machine level, and your working repos (including OSS ones) stay clean.
The data home defaults to `~/devbrain-data` (override with `DEVBRAIN_DATA`).

## Troubleshooting

- **Prompts not captured** — check the hook is registered
  (`jq .hooks ~/.claude/settings.json`) and `jq` is installed; the hook fails open
  by design.
- **`gbrain not found`** — install the engine and re-run `./setup`.
- **Brain looks stale** — `~/.claude/hooks/devbrain-rebuild.sh` re-imports every page.
- Re-run `./setup` anytime; it only adds what's missing.
