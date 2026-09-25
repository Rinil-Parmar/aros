# ADR-0009: In the TUI, goroutines talk to the model only through messages

- **Status:** Accepted
- **Date:** 2026-09-17
- **Deciders:** Rinil Parmar
- **Related:** [ADR-0010](0010-single-ui-agnostic-work-scheduler.md), [ADR-0014](0014-generation-gating-and-universal-cancel.md)

## Context

The TUI is Bubble Tea: a single event loop calls `Update(msg)` and then `View()`
on one goroutine. Phases run for minutes, so they must run off that goroutine.

The first implementation had phase goroutines holding a `*Model` pointer and
writing fields on it directly (`m.project.Phase = …`, `m.busy = false`), and had
`Update` calling `program.Send(...)` to emit follow-up messages.

Both are broken, and both were confirmed by the race detector and by a hung UI:

- `Program.Send` writes to an **unbuffered** channel that only the event loop
  reads. Calling it from inside `Update` — which *is* the event loop — deadlocks
  the entire program. This froze the TUI on chat start and at every phase
  completion.
- Concurrent field writes from phase goroutines raced with `View()` reading the
  same fields to render.

## Decision

Two rules, stated at the top of `tui/messages.go` and enforced by review:

1. **A goroutine never touches `Model` fields.** It receives a `phaseRun`
   snapshot — an immutable copy of config, registry, memory handle, `.aros`
   path and the project state — and communicates results back only via
   `send(msg)`. Messages that carry state (e.g. `phaseResultMsg.project`) carry
   a *copy* the model may adopt wholesale.
2. **Code running inside `Update` never calls `send()`.** It mutates the model
   directly, because it already owns the event loop. Work that must happen
   asynchronously is returned as a `tea.Cmd`.

Corollaries:
- `bootstrap` (config load, registry build, state load) is a `tea.Cmd` returning
  one `bootstrapMsg`, not a goroutine writing into the model.
- The right panel renders from a cached manifest refreshed on
  `manifestChangedMsg`, instead of reading `manifest.json` from disk on every
  frame.
- The `*tea.Program` used by `send()` is an atomic pointer, since many
  goroutines read it.

## Consequences

**Good**
- `go test -race` on the TUI package is meaningful and currently clean; the
  remaining state machine is testable headlessly.
- Rendering is deterministic: `View()` reads only fields the event loop wrote.
- A deadlock class is gone rather than worked around.

**Bad**
- Snapshots mean a phase works from a possibly stale view of config. Changing a
  model mid-phase does not affect the running phase. Acceptable, and arguably
  correct, but it is a real behaviour difference.
- More boilerplate: every new piece of state a phase needs must be added to
  `phaseRun`, and every result needs a message type.

**Neutral**
- The rule is a convention, not something the type system enforces. A reviewer
  has to catch a `send()` added inside `Update`.

## Alternatives considered

- **A mutex around the model** — rejected: it would make `View()` block on phase
  goroutines and invites lock-ordering bugs, while Bubble Tea's own design
  already provides a serialisation point.
- **Buffered message channel** — would paper over the `send`-from-`Update`
  deadlock until the buffer filled, turning a reproducible hang into an
  intermittent one.
- **Keep direct model writes, add locks only where the race detector complains**
  — rejected: the race detector only reports what a test actually exercised.

## Notes

`tui/messages.go` (rules + message types), `tui/helpers.go` (`phaseRun`),
`tui/phases.go`. Verified by `tui/tui_test.go`, which drives the real event loop
headlessly and fails on a deadlock by timing out.
