
## devbrain (cross-project brain)

Every prompt is captured to the private data repo at `~/devbrain-data`
(routing by git remote -> `projects/<project>/`). On resume or when the
user asks "where was I" / "continue", run `/continue` to pull this project's
brain and refresh the live world. After meaningful progress, run `/distill`
to curate new log into brain pages. Query the brain with `gbrain search`.

**Lead every response with a one-sentence summary** of what you did or
concluded this turn (then continue normally). devbrain's Stop hook captures
that first sentence as the turn's log summary — so make it a faithful,
self-contained recap, not a preamble like "Sure, let me...".
