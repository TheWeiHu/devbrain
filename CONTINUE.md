# CONTINUE — devbrain handoff

You're picking up **devbrain**: personal, cross-project infrastructure that turns
the prompts you write into a durable, queryable brain an agent can resume from.
*The log is the agent.* Read `DESIGN.md` for the full design; this file is the
resume cursor — read it first.

## Pipeline (one line)

raw log → brain (gbrain) → assembled context → `/continue`
**Golden rule:** everything downstream of the raw log is a rebuildable projection.
Never lose the log.

## Where things stand (as of 2026-06-15)

**Built — real, running, and now vendored + installable:**
- `DESIGN.md` — full design + Q&A (capture scheme, sync, locking, rebuild, discovery).
- **Stage A capture:** `hooks/devbrain-capture.sh` (`UserPromptSubmit`) +
  `hooks/devbrain-capture-response.sh` (`Stop`) + `hooks/devbrain-flush.sh`
  (per-machine git flusher) + `hooks/devbrain-rebuild.sh`.
- **Stage B/C skills:** `skills/continue/` (resume + auto-distill) and
  `skills/distill/` (checkpoint). `/checkpoint` was renamed `/distill` to avoid
  Claude Code's native `/checkpoint` rewind alias.
- **`setup`** — gstack-style idempotent installer that wires `~/.claude` (hooks,
  skills, `settings.json`, gbrain MCP, `CLAUDE.md` standing line) and the data repo.
- **`README.md`** — install + usage docs (gstack-inspired).
- Data repo lives **separately** at `~/devbrain-data` (own remote). Brain pages
  loaded into gbrain (local PGLite) and **verified queryable**.

These artifacts are the live wiring from `~/.claude`, copied into the repo and
path-normalized (`$DEVBRAIN_DATA` / `$HOME/devbrain-data`) so a fresh machine can
reproduce the install with one command.

**Still loose / next:**
- The flusher isn't auto-scheduled — wire `devbrain-flush.sh` to cron/launchd.
- `setup`'s settings.json merge + MCP add are written but not yet run end-to-end
  on a clean machine — dry-run on a second machine to confirm.

## Next actions (suggested order)

1. **Dry-run `./setup` on a clean machine** (or with a temp `CLAUDE_HOME`) to
   verify the settings.json merge, MCP registration, and CLAUDE.md append.
2. **Schedule the flusher** (launchd/cron) so capture durably pushes off-machine.
3. **Distill this session** into the brain (`/distill`) — the install story is new
   knowledge worth a page.

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
