# devbrain — Overview

**Thesis:** Turn the prompts you write into a brain an agent can resume from.
*The log is the agent* — the append-only history of your prompts is the durable
thing; the runtime, model, and index are interchangeable interpreters over it.

**Pipeline:** raw log → brain (gbrain) → assembled context → `/continue`.

**Golden rule:** every stage downstream of the raw log is a disposable,
re-derivable projection. Lose the brain → rebuild from the log. Never lose the log.

**Why it's separate from any one repo:** this is personal cross-project
infrastructure that sits *above* all repos. It does not belong inside an
open-source project (category error + privacy). The brain spans every repo you
work in; wiring lives in `~/.claude` / `~/devbrain`.

**Inspirations:** the "log is the agent" essay; `tk` (cullback/ticket) — files
over state, explicit over magic, branch-as-claim, open/closed minimalism.

Source: design conversation, 2026-06-13.
See also: [[project/devbrain-capture]], [[project/devbrain-brain]],
[[project/devbrain-assemble]], [[project/devbrain-concurrency-sync]],
[[project/devbrain-decisions]].
