# devbrain

Personal, cross-project infrastructure: turn the prompts you write into a durable,
queryable brain that any agent can resume from. *The log is the agent.*

This is the **system** repo — the portable installer + tooling. It contains *no*
personal content and could go public. Your actual brain (raw logs + distilled
pages) lives in a separate **private** repo,
[`devbrain-data`](https://github.com/TheWeiHu/devbrain-data), at a fixed home
(`~/Desktop/devbrain-data`). System never holds data; data never holds code.

- **Picking this up? Start with [`CONTINUE.md`](CONTINUE.md)** — the resume cursor.
- **Full design:** [`DESIGN.md`](DESIGN.md)
- **Brain source (markdown):** the private `devbrain-data` repo, `projects/<project>/brain/`
- **Rebuild the gbrain index anywhere:** `DEVBRAIN_DATA=~/Desktop/devbrain-data ./scripts/rebuild-brain.sh`

It is deliberately *not* part of any other (e.g. OSS) repo: the brain spans every
project you work in, and the wiring lives at the machine level (`~/.claude` +
`~/Desktop/devbrain-data`).

## The two repos

| Repo | Visibility | Holds | Lifecycle |
|------|-----------|-------|-----------|
| `devbrain` (this) | could be public | design, scripts, capture hook, flusher, `/continue` + `/checkpoint` skills | the portable system |
| `devbrain-data` | **private, always** | raw prompt logs + distilled brain pages | your personal brain, all projects |

The system installs a hook that *writes into* the data repo, and a flusher that
*commits + pushes* the data repo.

**Status:** design + seed brain are done and verified queryable; the two-repo
split is in place. The capture hook, the flusher, the `/continue` and
`/checkpoint` skills, and the per-machine discovery wiring are specified in
`DESIGN.md` but **not yet built** — see `CONTINUE.md`.
