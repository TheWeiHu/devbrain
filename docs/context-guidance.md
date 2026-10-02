# Context that follows the task

devbrain should preserve the decisions an agent needs without making every task
replay the same setup. This is a shared workflow contract for Claude and Codex;
model settings belong to the host or project configuration.

At session start, retrieve relevant project context and inspect the queue. The
current user request remains the assignment. Starting a session does not itself
authorize `/continue`, distillation, or work on another queued item. An explicit
`/continue` still runs its existing resume, distill, and task workflow.

## Choose depth by missing information

The continue/work briefing needs the objective, acceptance criteria, accepted
decisions, constraints, current state, failed approaches, remaining work, and source
pointers. Preserve observation or verification dates when freshness matters, and
identify stale evidence, conflicts, and missing information.

Read source pages with enough surrounding context to interpret their decisions.
Reuse relevant pages already read; follow links or search again to resolve a real
gap. There is no minimum page count or word count, and no maximum brief length.
One relevant page can cover a small task. A cross-service change may need many.
Attach the brief to the task so later workers can recover the context and sources.

## Keep preferences scoped

Global preferences contain durable cross-project defaults. A repeated correction
within one incident does not establish a universal rule. Preserve the condition
that makes a preference useful: schema review for schema changes, for example,
rather than a full database review before every edit. Keep project rules scoped
and detailed evidence in the relevant brain page. Existing hand-edit precedence,
deletion history, maintenance gating, and privacy rules still apply.

## Check changes before calling them an optimization

Compare representative tasks against the existing setup using the same model,
effort, tools, and acceptance criteria. Include a small edit, a task with substantial
prior context, and a task involving stale or conflicting notes. Check correctness,
retained decisions and sources, tool calls, latency, and measured token usage. When
available, report cached input separately and count reasoning within output.

Do not equate shorter instruction files with measured usage savings or equal
quality. Do not compare an API-equivalent estimate with a subscription invoice.
Adjust model effort or compaction policy separately, based on the host's actual
behavior and the task's quality requirements; avoid universal token thresholds.

As reviewed on 2026-09-19, both providers recommend revisiting older scaffolding:
[OpenAI's Astra guidance](https://developers.openai.com/blog/rethinking-skills-and-prompts-for-gpt-6-astra)
emphasizes precise skill triggers and reading relevant documentation;
[Anthropic's Fable 5 guidance](https://platform.claude.com/docs/en/build-with-claude/prompt-engineering/prompting-claude-fable-5)
recommends removing overly prescriptive instructions while preserving useful memory
and verification for long runs. Their effort and verification recommendations
differ. Those model-specific choices should not become universal devbrain defaults.
