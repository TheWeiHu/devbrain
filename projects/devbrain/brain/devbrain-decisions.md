# devbrain — Design Decisions

Resolved decisions from the 2026-06-13 design conversation, with rationale.

1. **The log is the source of truth; everything else is a rebuildable projection.**
   Why: keeps the system recoverable and ownable; matches "the log is the agent."

2. **Capture is a model-free `UserPromptSubmit` hook; summaries are written by the
   main agent, not a separate Haiku call.** Why: prompts are the high-value data
   and must be guaranteed; a second model is needless cost/latency.

3. **This is personal cross-project infra, NOT a feature of any one (OSS) repo.**
   Why: category error + privacy; the brain spans all repos.

4. **gbrain is the brain + retrieval engine (stages B/C), not the source of truth
   and not the lock.** Why: it's per-machine and disposable.

5. **Logs sharded per session; per-machine brain rebuilt from synced logs.**
   Why: one-writer-per-file → conflict-free git sync; cheap rebuild justifies a
   local brain.

6. **No locks: branch existence is the claim (after `tk`).** Why: file/git-native
   coordination with no lock manager; partition work by task.

7. **Default storage: PGLite local + export-to-git for portability.** Supabase
   only if a shared live brain + gbrain leasing is required.

8. **Wiring (hook, MCP, `/continue` skill, standing instruction) lives at the
   machine level (`~/.claude` / `~/devbrain`), never in the working repo.**

**Open questions:** how gbrain's `--detail low` "compiled truth" is produced
(auto-distill vs explicit `put`); `/checkpoint` cadence; secrets/privacy policy
for prompt logs.

Source: design conversation, 2026-06-13.
See also: [[project/devbrain-overview]].
