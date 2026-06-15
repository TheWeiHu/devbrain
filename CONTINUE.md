# CONTINUE — devbrain handoff

You're picking up **devbrain**: personal, cross-project infrastructure that turns
the prompts you write into a durable, queryable brain an agent can resume from.
*The log is the agent.* Read `DESIGN.md` for the full design; this file is the
resume cursor — read it first.

## Pipeline (one line)

raw log → brain (gbrain) → assembled context → `/continue`
**Golden rule:** everything downstream of the raw log is a rebuildable projection.
Never lose the log.

## Where things stand (as of 2026-06-14)

**Architecture — two repos (split done 2026-06-14):**
- `devbrain` (this, system) — design, scripts, and the to-build tooling. No personal data.
- `devbrain-data` (private, `~/Desktop/devbrain-data`, github.com/TheWeiHu/devbrain-data) —
  the markdown brain: `projects/<project>/log/...` (raw logs) + `projects/<project>/brain/*.md`
  (distilled pages). The capture hook writes here; the flusher commits/pushes here.

**Built — real (the whole pipeline is now wired on this machine, 2026-06-14):**
- `DESIGN.md` — full design + Q&A (capture scheme, sync, locking, rebuild, discovery).
- `devbrain-data/projects/devbrain/brain/*.md` — 6 distilled design pages (the brain's source).
- Both repos standalone + pushed (data repo private).
- **Stage A capture — LIVE.** `hooks/capture.sh` (`UserPromptSubmit`) appends each
  prompt verbatim to `~/Desktop/devbrain-data/projects/<project>/log/<date>/<worktree>.<session>.md`.
  Model-free, fails open. Verified capturing across repos/sessions/worktrees.
- **Flusher — LIVE.** `scripts/flush.sh` via a launchd LaunchAgent
  (`com.devbrain.flush`, every 5 min): pull --rebase → commit → push the data repo.
  Verified end-to-end (captured prompt → pushed to private GitHub).
- **gbrain — installed** (v0.18.2, local PGLite), MCP registered + connected,
  brain loaded via `scripts/rebuild-brain.sh`. Queryable via `gbrain search`.
- **Stage C skills — installed** (user-level): `/continue` (resume) and `/distill`
  (the design's `/checkpoint` role; renamed to dodge Claude Code's native
  `/checkpoint` rewind alias).
- **Discovery wiring — done.** gbrain MCP + a marker-delimited block in
  `~/.claude/CLAUDE.md`. `scripts/install.sh` / `uninstall.sh` do it all idempotently.

## Install on a new machine

```bash
git clone git@github.com:TheWeiHu/devbrain.git ~/Desktop/devbrain   # system
git clone git@github.com:TheWeiHu/devbrain-data.git ~/Desktop/devbrain-data # private data
~/.claude/skills/setup-gbrain  # or: brew install bun && install gbrain CLI
~/Desktop/devbrain/scripts/install.sh
```

## Resolved (were open questions)

- **`gbrain query --detail low` "compiled truth" is unbuilt** → skills use
  `gbrain search` instead (reliable across embedders). Revisit if a compiled layer ships.
- **Home path** → data repo lives at the fixed `~/Desktop/devbrain-data` (single writer).

## Still open

- **Secrets in prompt logs.** Capture is verbatim; prompts can contain keys, now
  auto-pushed (to a private repo, but still). Add a redaction pass to `capture.sh`
  before this runs long. `/distill` is told not to copy secrets into brain pages.
- **`/distill` cadence** (per-session? explicit only?). Currently explicit.
- **gbrain relevance** is low without an embedder API key (local model). Configure
  an OpenAI embedder for sharper `search`/`query`.

## Rebuild the brain (on any machine)

```bash
DEVBRAIN_DATA=~/Desktop/devbrain-data ./scripts/rebuild-brain.sh
gbrain query "how does devbrain sync logs across machines" --detail low
```

## Provenance

Born from a design conversation on **2026-06-13**, held in the `redlens` worktree
but *about* devbrain. Decisions + rationale:
`devbrain-data/projects/devbrain/brain/devbrain-decisions.md`.
