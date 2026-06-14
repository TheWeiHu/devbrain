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

**NOT built yet — specified in `DESIGN.md`, no code:**
- **Stage A capture:** the `UserPromptSubmit` hook + the per-machine git flusher.
- **Stage C skills:** `/continue` (resolve project → `gbrain query --detail low` →
  refresh world) and `/checkpoint` (distill new log → propose page updates).
- **Discovery wiring:** gbrain MCP + a standing line in `~/.claude/CLAUDE.md` +
  a user-level `/continue` skill (so any repo's agent reads the brain).
- **No raw prompt-log files exist.** The 6 pages were hand-distilled from the
  2026-06-13 design conversation, not from a captured log. Stage A was simulated.

## Next actions (suggested order)

1. **Capture hook + flusher.** `UserPromptSubmit` appends each prompt to
   `projects/<project>/log/<YYYY-MM-DD>/<worktree>.<session-id>.md` (scheme in
   `DESIGN.md` / `devbrain-capture`). A single per-machine flusher does
   `git -C ~/devbrain pull --rebase && add && commit && push`.
2. **`/continue` + `/checkpoint` skills** (user-level, so they work in any repo).
3. **Per-machine discovery wiring** (MCP + `~/.claude/CLAUDE.md` + the skill).

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
