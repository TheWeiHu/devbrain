# devbrain

Personal, cross-project infrastructure: turn the prompts you write into a durable,
queryable brain that any agent can resume from. *The log is the agent.*

- **Picking this up? Start with [`CONTINUE.md`](CONTINUE.md)** — the resume cursor.
- **Full design:** [`DESIGN.md`](DESIGN.md)
- **Brain source (markdown):** `projects/devbrain/brain/`
- **Rebuild the gbrain index anywhere:** `./scripts/rebuild-brain.sh`

This is a **standalone git repo** — move it anywhere, push it to its own remote.
It is deliberately *not* part of any other (e.g. OSS) repo: the brain spans every
project you work in, and the wiring lives at the machine level (`~/.claude`).

**Status:** design + seed brain are done and verified queryable. The capture hook,
the `/continue` and `/checkpoint` skills, and the per-machine discovery wiring are
specified in `DESIGN.md` but **not yet built** — see `CONTINUE.md`.
