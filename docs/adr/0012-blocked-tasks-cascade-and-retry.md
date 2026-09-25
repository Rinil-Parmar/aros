# ADR-0012: A failed task blocks its dependents; the next run retries them

- **Status:** Accepted
- **Date:** 2026-09-17
- **Deciders:** Rinil Parmar
- **Related:** [ADR-0010](0010-single-ui-agnostic-work-scheduler.md), [ADR-0005](0005-task-manifest-as-validated-json-dag.md)

## Context

Agents fail for reasons that have nothing to do with the task: an expired token,
a rate limit, a model name that stopped being available, a timeout. Before this
decision, a failure had two bad outcomes:

- the run aborted on the first error, abandoning tasks that were running fine, and
- the failed task's dependents stayed `pending` forever, so the scheduler's
  completion check (`done + blocked == total`) never became true and the loop
  spun until the user killed it.

Separately, the work phase marked the project `done` even when tasks had failed,
and the TUI told the user to "run work again to resume" — which did nothing,
because blocked tasks were never reset to pending.

## Decision

Failure is a first-class, recoverable state:

- A failing task becomes `blocked` with a `block_reason`; the run **continues**
  for every independent task.
- Blocked status **cascades**: a pending task whose dependency is blocked becomes
  blocked too, with reason `dependency <id> is blocked`, applied transitively
  until nothing changes. Its agent is never invoked. This guarantees the run
  terminates.
- `worker.Run` returns `ErrTasksBlocked` (wrapped, with counts). The caller
  leaves the phase at `work` — **not** `done` — and tells the user to inspect
  `status` and run `work` again.
- At the start of every run, `ResetForRetry` puts `blocked` and `in_progress`
  tasks back to `pending` and clears their block reason. `done` tasks keep their
  status and output and are never re-run.

## Consequences

**Good**
- A transient failure costs only the failed subtree, not the whole run.
- `aros work` is idempotent and resumable: run it again after fixing the cause
  and it continues where it stopped.
- A run always terminates, which is what makes the TUI's busy state trustworthy.

**Bad**
- Retrying blindly re-runs a task whose cause is *not* transient, so a genuinely
  impossible task is attempted once per invocation. There is no attempt counter
  or backoff.
- Re-running a task that partially edited files repeats that work; the agent
  sees the half-finished state as its starting point. Aros does not snapshot or
  roll back.
- `in_progress` is reset too, which means a task from a *concurrently running*
  Aros process would be stolen. Nothing prevents two processes in one directory
  (see ADR-0006).

**Neutral**
- The cascade reason names only the immediate blocked dependency, not the root
  cause; the user follows the chain in `status`.

## Alternatives considered

- **Abort the whole run on first failure** — the original behaviour; it wasted
  the work of every healthy task and orphaned running agents.
- **Retry inside the same run with backoff** — better for rate limits, but for
  the common causes here (bad credentials, wrong model) the fix is human, so
  retrying in-process just burns time before the user is told.
- **Mark dependents `skipped` rather than `blocked`** — a third status that the
  retry logic would treat identically; not worth the extra state.

## Notes

`worker/worker.go` (`cascadeBlocked`, `ErrTasksBlocked`),
`state/tasks.go` (`BlockedDep`, `ResetForRetry`).
Tested in `worker/worker_test.go` and end-to-end in `scripts/e2e-mock.sh`, where
a mock agent fails once, the phase stays at `work` with the reason visible in
`status`, and a second `aros work` completes the run.
