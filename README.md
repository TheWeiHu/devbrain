# devbrain

Personal, cross-project infrastructure: turn the prompts you write into a durable,
queryable brain that any agent can resume from. **The log is the agent.**

devbrain captures every prompt you send — in *any* repo — to a private, git-synced
data store, distills it into a searchable brain, and lets any future session pick
up exactly where you (or another machine) left off. Markdown + git is the source of
truth; everything else is a rebuildable projection.

It's **two repos**: this **system** repo (the portable installer + tooling — no
personal content, could go public) and a separate **private** `devbrain-data` repo
that holds your raw logs + distilled pages. System never holds data; data never
holds code.

---

## Install (30 seconds)

**Prerequisites:** [Claude Code](https://claude.ai/code) · Git · `jq` · `python3`.
The [`gbrain`](#the-gbrain-engine) query engine is **installed for you** if [`bun`](https://bun.sh)
is present (otherwise `setup` tells you the one command to run). An
[OpenAI API key](#openai-key-recommended-for-semantic-search) is **recommended**
(not required) — it unlocks semantic search.

Open Claude Code and paste this single command:

```bash
git clone --depth 1 https://github.com/TheWeiHu/devbrain.git ~/.claude/skills/devbrain \
  && cd ~/.claude/skills/devbrain && ./setup
```

`./setup` is **idempotent and additive** — safe to re-run. It wires *this machine*,
never your working repos. Concretely it:

1. Installs the **gbrain** engine (via bun) and initializes a local brain.
2. Asks where to store the brain (defaults to `~/devbrain-data`) and creates/clones it.
3. Installs the **capture hooks** into `~/.claude/hooks/`.
4. Installs the **`/continue`** and **`/distill`** skills into `~/.claude/skills/`.
5. Registers the capture hooks (`UserPromptSubmit` + `Stop`) in `~/.claude/settings.json`.
6. Loads a **launchd flusher** that commits + pushes the data repo every 5 minutes.
7. Appends a standing line to `~/.claude/CLAUDE.md` so agents know to resume.
8. Builds the brain index from the data repo.

That's it. From now on, every prompt in every repo is captured automatically.
(Under the hood, `./setup` hands off to `scripts/install.sh` for the machine
wiring; tear it all down — leaving your data untouched — with
[`scripts/uninstall.sh`](scripts/uninstall.sh).)

### Choosing where the brain lives

`setup` **prompts** for the storage path (default `~/devbrain-data`). To skip the
prompt — for scripted/CI installs, or to point at an existing brain — set it
up front:

```bash
DEVBRAIN_DATA=~/my/path ./setup                             # store the brain elsewhere
DEVBRAIN_DATA_REMOTE=git@github.com:you/brain.git ./setup   # clone an existing brain
```

To sync your brain across machines, give the data repo a **private** remote:

```bash
git -C ~/devbrain-data remote add origin <your-private-repo-url>
```

---

## How it works

```
   you type a prompt            /continue                /distill
        │                           ▲                        │
        ▼                           │                        ▼
   ┌──────────┐   git push/pull  ┌──────────┐   gbrain    ┌──────────┐
   │ A Capture │ ───────────────▶ │ B  Brain  │ ──────────▶ │ C Assemble│
   │  (hook)   │                  │ (markdown │   import    │ (briefing)│
   │ raw log   │ ◀─ source of ──  │  + index) │             │           │
   └──────────┘     truth         └──────────┘             └──────────┘
```

**A — Capture** (dumb, automatic, model-free). A `UserPromptSubmit` hook appends
every prompt verbatim to `~/devbrain-data/projects/<project>/log/<date>/<worktree>.<session>.md`.
Routing is mechanical — by git remote, never by topic. It never blocks your turn
and never fails the session. A `Stop` hook attaches a one-line trace of what the
agent did. **This is the source of truth. Never lose it.**

**B — Brain** (gbrain). `/distill` reads new log entries and curates them into
linked, tagged **brain pages** grouped by *topic* — searchable via gbrain
(Postgres + graph + hybrid search, exposed over MCP). Every fact carries
provenance back to the log. The brain is a *rebuildable projection*: lose it,
rebuild from the log.

**C — Assemble** (the right amount). `/continue` resolves the current project,
pulls the relevant brain at low detail, refreshes the live world (git / issues /
CI), and hands back a short briefing — subtraction, not context-stuffing.

**Golden rule:** everything downstream of the raw log is disposable and
re-derivable. Never lose the log.

See [`DESIGN.md`](DESIGN.md) for the full design + Q&A.

---

## Daily use

| Command | What it does |
|---|---|
| *(automatic)* | Every prompt is captured to the data repo — no action needed. |
| **`/continue`** | Resume a project: fold in new log → pull brain → refresh world → briefing. Also: "where was I", "pick up where I left off". |
| **`/distill`** | Checkpoint: distill new log into brain pages (writes directly; review by git diff). |
| `gbrain search "<q>"` | Query the brain from the shell. |
| *(automatic)* | A launchd flusher commits + pushes the data repo every 5 min — no action needed. Run `~/.claude/hooks/devbrain-flush.sh` to force one. |

---

## The gbrain engine

devbrain stores and queries the brain with **gbrain**, a personal knowledge brain
(local PGLite by default — you own the file — or Supabase for a shared live brain).
It's a separate tool (a JS/Bun binary, **not** pip), but you don't install it by
hand: `setup` runs `bun add -g gbrain`, initializes a local PGLite brain, and
registers the MCP for you.

It only asks you to act if **bun isn't installed** — then it prints the one
command to run:

```bash
/setup-gbrain          # if you have gstack — full bring-up + MCP
# or:  bun add -g gbrain      then re-run ./setup
```

Capture (Stage A) works even without gbrain — you just can't *query* the brain
until the engine is present, so `setup` fails open rather than blocking.

### OpenAI key (recommended, for semantic search)

gbrain searches best with embeddings, which need an **OpenAI API key**. It's
optional — without one, search falls back to **keyword + graph** ranking (still
useful); with one, you get **semantic** search (the "find the right page even when
you don't remember the words" behavior). Embeddings cost pennies and the key stays
local in `~/.gbrain/config.json`.

`setup` checks for a key and, if it's missing, prints exactly how to add one:

```bash
gbrain config set openai_api_key sk-...      # stored locally in ~/.gbrain/config.json
# or, via environment:
export OPENAI_API_KEY=sk-...                 # gbrain also reads it from the env
```

After adding the key, backfill embeddings for existing pages:

```bash
gbrain embed --stale
```

---

## Keeping the brain in sync

- **Logs sync conflict-free.** One file per session per day → one writer per file,
  so `git pull` only ever *adds* files. Never a content conflict.
- **The brain is per-machine.** It's rebuilt from the synced logs with
  `~/.claude/hooks/devbrain-rebuild.sh` (or `gbrain import`). `/continue` pulls,
  then folds in, automatically.
- **Durability ladder:** capture appends locally (instant) → the flusher
  commits/pushes (off-machine).

---

## Layout

```
~/.claude/skills/devbrain/        ← this system repo (the installer + tooling)
├── setup                         ← gstack-style entrypoint (wraps scripts/install.sh)
├── scripts/install.sh            ← machine wiring (hooks, launchd flusher, skills, CLAUDE.md)
├── scripts/uninstall.sh          ← reversible teardown (leaves your data alone)
├── scripts/flush.sh · rebuild-brain.sh · com.devbrain.flush.plist
├── hooks/capture.sh · capture-response.sh   ← Stage A capture (installed to ~/.claude/hooks)
├── skills/{continue,distill}/    ← the resume + checkpoint skills
├── DESIGN.md · CONTINUE.md       ← design + resume cursor

~/devbrain-data/                  ← the private data repo (source of truth)
└── projects/<project>/
    ├── log/<date>/<worktree>.<session>.md   ← Stage A raw prompts (sacred)
    └── brain/*.md                           ← Stage B distilled pages
```

The system repo (here) and the data repo are **separate on purpose**: the brain
spans every project, the wiring lives at the machine level, and your working repos
(including OSS ones) stay clean. On this machine the data home is
`~/Desktop/devbrain-data`; new installs default to `~/devbrain-data` (override with
`DEVBRAIN_DATA`).

---

## Troubleshooting

- **Prompts aren't being captured.** Confirm the hook is registered
  (`jq .hooks ~/.claude/settings.json`) and `jq` is installed. The hook fails open
  by design — a missing dependency silently skips capture rather than breaking
  your turn.
- **`gbrain not found`.** Install the engine ([above](#the-gbrain-engine)) and
  re-run `./setup`.
- **Brain looks stale.** `~/.claude/hooks/devbrain-rebuild.sh` re-imports every
  page (idempotent upsert by slug).
- **Re-run `./setup` anytime** — it only adds what's missing.

---

## Why not pip?

devbrain is markdown + git + a few bash hooks; its engine (gbrain) is a JS/Bun
binary. There's nothing Pythonic to package. The install vector that fits the
design is the one above: **clone into `~/.claude/skills/`, then `./setup`** wires
the per-machine plumbing a package manager can't (hooks, launchd flusher, the
standing instruction). Inspired by [gstack](https://github.com/garrytan/gstack)'s
installer.

---

## The two repos

| Repo | Visibility | Holds | Lifecycle |
|------|-----------|-------|-----------|
| [`devbrain`](https://github.com/TheWeiHu/devbrain) (this) | could be public | design, installer, capture hooks, flusher, `/continue` + `/distill` skills | the portable system |
| [`devbrain-data`](https://github.com/TheWeiHu/devbrain-data) | **private, always** | raw prompt logs + distilled brain pages | your personal brain, all projects |

The system installs a hook that *writes into* the data repo, and a launchd flusher
that *commits + pushes* it. System never holds data; data never holds code.
