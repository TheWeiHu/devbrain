# devbrain — Stage C: Assemble & Continue

**Goal:** output the *right amount* of context to load into an agent — which
skews **small**. The job is subtraction, not stuffing; over-loading dilutes
attention, buries the next action, and costs budget.

**`/continue` protocol:** resolve project (git remote → slug) → resolve task
(branch → issue) → `gbrain query "<task>" --detail low` ("compiled truth") →
**refresh the world** (`git pull` devbrain, `git fetch`, `gh issue`, CI) → emit a
small briefing + pointers. World-drift follow-ups (main moved, issue closed) are
*good* and expected — the agent re-engages rather than trusting stale memory.

**Progressive disclosure:** gbrain's `--detail low|medium|high` *is* the budget
dial. Load a tiny core; the agent pulls more on demand. Deterministic scoping
first; LLM compression only when over budget.

**Self-tuning:** if the agent asks for something the brain already held →
under-load (bump ranking). If it ignores most of what loaded → over-load (trim).

**Discovery in other repos (per-machine wiring):**
1. gbrain **MCP** registered in `~/.claude/settings.json` → the query tool exists
   in every session, every repo.
2. A standing line in **`~/.claude/CLAUDE.md`** → the agent knows to query the
   current project's brain on resume.
3. A user-level **`/continue` skill** → the protocol, invokable anywhere.
Optional `SessionStart` hook injects only a tiny nudge ("brain for X: N open
tasks — /continue"); full load stays explicit.

Source: design conversation, 2026-06-13.
See also: [[project/devbrain-brain]], [[project/devbrain-concurrency-sync]].
