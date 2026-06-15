# devbrain

Personal, cross-project infrastructure: turn the prompts you write into a durable,
queryable brain that any agent can resume from. *The log is the agent.*

This repo is the **system** (hooks, skills, installer). Your actual brain — prompts
and distilled pages — lives in a **separate private data repo** (`$DEVBRAIN_DATA`,
default `~/devbrain-data`), so personal data never mixes with this code.

## Install

```sh
./install.sh
```

Idempotent. It:
1. Deploys the capture hooks + `/continue` and `/distill` skills to `~/.claude`.
2. Turns `$DEVBRAIN_DATA` into a real git repo and **prompts you to connect a
   remote** (via `gh repo create` or a git URL) so the brain is backed up
   off-machine — not just temp files. Decline and it's local-only (still real git
   commits; add a remote anytime).
3. Adds the "lead every response with a one-sentence summary" standing instruction
   to `~/.claude/CLAUDE.md` and registers the hooks in `settings.json`.
4. Installs the launchd **flusher** that commits + pushes the brain every 5 min.

Uninstall with `./uninstall.sh` (leaves your data repo untouched).

> **macOS gotcha:** keep `$DEVBRAIN_DATA` **out of** `~/Desktop`, `~/Documents`,
> `~/Downloads`. Those are TCC-protected and launchd is denied access there
> (`Operation not permitted`), so the flusher silently never pushes. Want it
> visible on the Desktop? Keep the real repo at `~/devbrain-data` and symlink:
> `ln -s ~/devbrain-data ~/Desktop/devbrain-data`.

## How it works

```
raw log  →  brain (gbrain)  →  assembled context  →  /continue
```
Everything downstream of the raw log is a **rebuildable projection**. Never lose
the log.

- **Capture (Stage A):** `hooks/devbrain-capture.sh` (`UserPromptSubmit`) appends
  every prompt verbatim; `hooks/devbrain-capture-response.sh` (`Stop`) appends a
  model-free summary line (the first sentence of the response) + files/tools
  touched. Routed by git remote → `projects/<project>/log/<date>/<worktree>.<sid>.md`.
- **Brain (Stage B):** `/distill` folds new log entries into topic pages under
  `projects/<project>/brain/` and loads them into **gbrain** (local PGLite index).
- **Assemble (Stage C):** `/continue` syncs, auto-distills, ranks this project's
  pages (semantic `gbrain query` when `OPENAI_API_KEY` is set, keyword `gbrain
  search` otherwise), refreshes the live world, and briefs you.
- **Durability:** `hooks/devbrain-flush.sh` (launchd, every 5 min) commits + pushes
  the data repo. Local commits always happen; push only when a remote is set.

### Embeddings are optional
Semantic ranking uses OpenAI `text-embedding-3-large` (`OPENAI_API_KEY`). Without a
key, devbrain falls back to keyword search everywhere — fully functional, no cost,
offline. The key is an opt-in enhancement, never a dependency.

## Layout

```
hooks/        capture, capture-response, flush, rebuild  (deployed to ~/.claude/hooks)
skills/       continue, distill                          (deployed to ~/.claude/skills)
templates/    launchd plist + CLAUDE.md block            (rendered by install.sh)
install.sh    uninstall.sh
scripts/rebuild-brain.sh   load THIS repo's seed design pages into gbrain
projects/devbrain/brain/   the seed: devbrain's own design, as brain pages
DESIGN.md  CONTINUE.md      design doc + handoff cursor
```

`hooks/devbrain-rebuild.sh` rebuilds the gbrain index from your **live** data repo
(`$DEVBRAIN_DATA`); `scripts/rebuild-brain.sh` loads the **seed** design pages
shipped in this repo. Both only rebuild the index — neither alters the markdown.

This is a **standalone git repo**, deliberately *not* part of any other (e.g. OSS)
repo: the brain spans every project you work in, and the wiring lives at the
machine level (`~/.claude`).
