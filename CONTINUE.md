# CONTINUE — devbrain handoff

You're picking up **devbrain**: personal, cross-project infrastructure that turns
the prompts you write into a durable, queryable brain an agent can resume from.
*The log is the agent.* Read `DESIGN.md` for the full design; this file is the
resume cursor — read it first.

## Pipeline (one line)

raw log → brain (gbrain) → assembled context → `/continue`
**Golden rule:** everything downstream of the raw log is a rebuildable projection.
Never lose the log.

## Where things stand (as of 2026-06-13)

**Built — real, in this folder:**
- `DESIGN.md` — full design + Q&A (capture scheme, sync, locking, rebuild, discovery).
- `projects/devbrain/brain/*.md` — 6 distilled design pages (the brain's source).
- Those pages loaded into gbrain (local PGLite) and **verified queryable**
  (semantic search returns the right page at ~0.9 relevance).
- This is a standalone git repo.

**Now built — runnable, in this repo (`./install.sh`):**
- **Stage A capture:** `hooks/devbrain-capture.sh` (`UserPromptSubmit`) +
  `hooks/devbrain-capture-response.sh` (`Stop`, model-free summary line) +
  `hooks/devbrain-flush.sh` (launchd flusher, commits + pushes every 5 min).
- **Stage B/C skills:** `skills/distill` (fold log → pages, no approval gate) and
  `skills/continue` (sync → auto-distill → semantic/keyword rank → brief).
- **Discovery wiring:** `install.sh` registers the hooks in `settings.json`, adds
  the standing instruction to `~/.claude/CLAUDE.md`, and installs the flusher.
- **Real captured logs exist** under `$DEVBRAIN_DATA/projects/*/log/`. The brain
  *data* now lives in a separate private repo (`~/devbrain-data`), not here.

**Still open / next:**
- gbrain MCP registration is assumed (not done by `install.sh` yet).
- Brain pruning of stale pages is manual (distill supersedes in place; no GC).

## Open questions

- How gbrain's `--detail low` "compiled truth" is produced (auto-distill vs explicit `put`).
- `/checkpoint` cadence (per-session? explicit only?).
- Secrets/privacy policy for prompt logs (they may contain keys).

## Rebuild the brain (on any machine)

```bash
./scripts/rebuild-brain.sh
gbrain query "how does devbrain sync logs across machines" --detail low
```

## Provenance

Born from a design conversation on **2026-06-13**, held in the `redlens` worktree
but *about* devbrain. Decisions + rationale: `projects/devbrain/brain/devbrain-decisions.md`.
