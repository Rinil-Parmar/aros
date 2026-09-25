# ADR-0010: One UI-agnostic work scheduler behind a Hooks interface

- **Status:** Accepted
- **Date:** 2026-09-17
- **Deciders:** Rinil Parmar
- **Related:** [ADR-0009](0009-bubbletea-message-only-concurrency.md), [ADR-0012](0012-blocked-tasks-cascade-and-retry.md)

## Context

The CLI (`worker/worker.go`) and the TUI (`tui/phases.go`) each had their own
work-phase loop. They were near-copies, and they drifted: each had bugs the
other did not, and each fix had to be made twice — when it was remembered at all.

Both copies also shared two design faults:

- The dispatcher acquired a concurrency semaphore **while holding the mutex**
  that finishing tasks needed to record their results. As soon as the number of
  ready tasks exceeded `max_concurrent`, the scheduler deadlocked.
- A failed task left its dependents `pending` forever, so the poll loop never
  reached a terminal state and spun indefinitely.

## Decision

One scheduler, `worker.Run`, used by both front ends:

```go
func Run(ctx context.Context, manifest *state.TaskManifest, arosDir string,
         reg agent.Registry, mem *memory.SecondMem,
         opts Options, hooks Hooks) (Result, error)
```

The UI is injected as optional callbacks — `Log`, `TaskStart`, `TaskOutput`,
`TaskDone`, `TaskBlocked`, `AskHuman`, `Prompt`. The CLI prints; the TUI turns
each into a Bubble Tea message (ADR-0009). `AskHuman` defaults to a stdin
prompt and is guaranteed never to be called concurrently, so two tasks can never
fight over the same input.

Concurrency rules inside the scheduler:
- semaphore acquisition is **non-blocking** (`select`/`default`); if no slot is
  free the dispatch pass simply stops and retries on the next wake,
- the mutex guards task fields and manifest writes and is never held across an
  agent call or a channel send,
- a finished task signals a wake channel so the next task dispatches
  immediately rather than waiting for the poll tick.

## Consequences

**Good**
- One implementation to fix, one to test. The regression tests (more ready tasks
  than slots, dependency ordering, cancellation) protect both front ends.
- Adding a front end means implementing `Hooks`, not another scheduler.
- The deadlock and the infinite loop are gone, with tests that fail if either
  returns.

**Bad**
- `Hooks` has seven fields and will grow; it is the seam where UI concerns leak
  into the scheduler (the `Prompt` hook exists purely so the TUI can prepend its
  dense-mode preamble).
- Callbacks run on the scheduler's goroutines, so a slow hook slows scheduling.
  The TUI's hooks only do a channel send, which is cheap, but nothing enforces that.

**Neutral**
- The scheduler still polls (default 2s) as a backstop in addition to the wake
  channel. Cheap, and it keeps a missed signal from stalling a run.
- Plan and divide remain duplicated between `planner`/`divider` (CLI) and
  `tui/phases.go`. Only the work phase is unified so far.

## Alternatives considered

- **Keep two schedulers, sync them by discipline** — that was the status quo;
  it produced the drift this ADR exists to remove.
- **An events channel instead of callbacks** — equivalent power, but forces every
  caller to run a consumer goroutine and re-serialise; callbacks let the CLI stay
  straight-line code.
- **errgroup for the whole phase** — errgroup cancels all siblings on the first
  error, which is exactly wrong here: one failed task must not abort the others
  (ADR-0012).

## Notes

`worker/worker.go`; hooks wired in `cmd/work_cmd.go` and `tui/phases.go`.
`worker/worker_test.go` covers the deadlock, ordering, blocked cascade, retry,
the `<<AROS_HUMAN>>` loop, unknown-agent fallback and cancellation.
