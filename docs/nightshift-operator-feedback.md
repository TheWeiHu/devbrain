# Nightshift operator feedback

This note captures sanitized operator feedback from a real nightshift run on a
private repository. It intentionally omits project names, customer names, branch
names, URLs, exact task identifiers, and any prompt contents.

## Summary

The core workflow is useful: a long-lived queue, a dashboard, and an overnight
worker fleet make it possible to hand off repo maintenance work and review one
aggregate result later. The rough edges below came from the control plane around
pausing, provider limits, task dedupe, and project targeting.

## Observed experience

1. **Provider-limit loops created noisy queue state.**
   Workers repeatedly started, immediately hit the coding-agent provider's usage
   limit, and then produced new high-priority held tasks for the same underlying
   blocker. From the operator's point of view, no useful work was happening, but
   the queue kept accumulating duplicate "red fleet" tasks that had to be
   mentally ignored.

2. **The fleet needed an explicit pause mode.**
   When the operator decided to stop nightshift and come back later, the desired
   state was not "blocked with many held tasks"; it was "paused until I restart
   it." A pause/resume state would match that intent better than continuing to
   express the situation as work items.

3. **`stop` worked, but only after escalation.**
   The stop command eventually reported success, but the orchestrator ignored the
   first termination signal and required a forced kill. That is probably the
   right fallback, but it would be helpful for the CLI and dashboard to make the
   final stopped state obvious and fast to verify.

4. **Status calls felt slow during failure handling.**
   Checking whether the fleet was stopped took tens of seconds. During a stop or
   pause flow, operators need a quick answer: running, stopping, stopped, or
   unknown/stale.

5. **Project targeting was easy to get wrong outside the dashboard.**
   The dashboard URL had the target project encoded in it, while CLI commands were
   routed by current working directory. During incident handling, that mismatch
   made it easy for an agent or operator to inspect the wrong queue before
   noticing the project context.

6. **Catch-up/distill needed a "latest only" path.**
   A project with a large historical log backlog made a normal ledger scan noisy.
   The operator wanted the latest context folded in right now, not a full
   historical backfill. A first-class "catch up latest context" mode would reduce
   ambiguity and make ledger advancement safer.

## Suggested improvements

- Detect repeated provider-limit or authentication-limit failures and pause the
  affected fleet automatically instead of creating another duplicate task.
- Deduplicate generated blocker tasks by stable cause, such as failure class plus
  test summary, so repeated runs update one task rather than creating many.
- Add a deliberate `nightshift pause` / `nightshift resume` state, distinct from
  `stop` and from held queue tasks.
- Make `nightshift status` return a compact state quickly, even if detailed queue
  inspection is slow.
- Accept an explicit project selector for queue and nightshift commands, for
  example `--project <owner>__<repo>` or a dashboard URL parser.
- Add a `distill --latest` or `devbrain catch-up --latest` workflow that folds a
  bounded, newest-first slice of context and reports exactly which files advanced
  in the ledger.
- Surface a dashboard callout when the fleet is paused due to provider limits:
  what happened, when it can be retried if known, and which command resumes it.

## Why this matters

Nightshift is most valuable when the operator can leave it alone and then trust
the morning state. Provider-limit loops and duplicate blocker tasks make the
state look worse than it is and obscure the real next action. A clearer pause
model plus stronger dedupe would make failure handling feel intentional instead
of noisy.
