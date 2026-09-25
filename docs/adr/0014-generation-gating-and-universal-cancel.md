# ADR-0014: Gate UI messages by generation; give the user one cancel key

- **Status:** Accepted
- **Date:** 2026-09-23
- **Deciders:** Rinil Parmar
- **Related:** [ADR-0009](0009-bubbletea-message-only-concurrency.md)

## Context

Users reported the TUI "getting stuck between modes": no spinner, yet typed
commands were rejected as busy — or an approval card on screen with no phase
behind it.

The cause is a consequence of ADR-0009. Once phases communicate only by
messages, a message can outlive the run that sent it. A phase that is cancelled
or superseded still has goroutines in flight, and their late messages were
applied to the model as if they were current:

- a late `phaseResultMsg` from run N cleared `busy` while run N+1 was live, so
  the UI looked idle while a phase was running — and then the next command
  started a *third* concurrent run;
- a late `agentActivityMsg` repopulated the activity panel immediately after a
  cancel, showing an agent as "running" when nothing was.

There was also no way out. Cancelling required knowing to type
`/phase <p> --force`, which is not discoverable, and it did not cancel a chat at
all. A panic in a phase goroutine killed the process outright, leaving the
terminal in the alternate screen.

## Decision

**Generation gating.** Every phase run and every chat is tagged with a number
drawn from one monotonic sequence on the model (`genSeq`), so phase and chat
generations can never collide. The tag travels on every message a run emits
(`phaseResultMsg`, `approvalMsg`, `freeInputMsg`, `chatDoneMsg`,
`streamLineMsg`, `agentActivityMsg`). `Update` drops any message whose
generation is no longer live:

```go
func (m *Model) live(gen int) bool {
    return gen == 0 || gen == m.phaseGen || gen == m.chatGen
}
```

Starting a run allocates a fresh generation; cancelling *retires* the current
one by allocating a new number that nothing in flight holds. `gen == 0` means
"not owned by a run" and is always accepted.

**Universal cancel.** `Esc` (and `Ctrl+G`) calls `cancelAll`: cancel the phase
context and the chat context — which kills the agent subprocesses via
ADR-0011 — retire both generations, clear pending approvals and questions, clear
the activity panel, and return to `modeText`. It is listed in the shortcuts bar,
so it is discoverable. With nothing running it clears the input line instead.

**Panic containment.** Phase goroutines run under `safeGo`, which converts a
panic into an ordinary phase error. The program survives and the terminal is
restored.

## Consequences

**Good**
- A stale run cannot mutate UI state. The class of bug is closed at the message
  boundary rather than patched per symptom.
- The user always has one key that gets them back to a usable state, whatever
  the TUI is doing.
- Cancelling now actually stops the agents, so it stops spending tokens.

**Bad**
- Every message type carrying UI state must remember to include `gen`, and a new
  one that forgets defaults to `0` — always accepted. That is the safe default
  for system messages but the wrong one for a run-owned message, and nothing
  catches it.
- Output already produced by a cancelled run is discarded, including partial
  agent output the user might have wanted to read.
- `Esc` is now reserved, so it cannot be bound to anything else later.

**Neutral**
- `--force` variants of `/phase` and `/session` still exist and route through the
  same `cancelAll`.

## Alternatives considered

- **Cancel the context and trust goroutines to stop sending** — insufficient:
  there is always a window between the last check and the send, and hooks fire
  from several goroutines.
- **Close a per-run channel and have `send` select on it** — equivalent effect,
  but pushes lifetime management into every call site instead of one check in
  `Update`.
- **A single "cancelled" boolean** — breaks as soon as a new run starts while an
  old one is still winding down, which is precisely the failing case.

## Notes

`tui/messages.go` (gating rule and message types), `tui/model.go`
(`nextGen`, `live`, `cancelAll`, `stopPhase`, `stopChat`), `tui/phases.go`
(`safeGo`). Covered in `tui/tui_test.go`: Esc from every waiting mode returns to
an operable state; replayed messages from a retired generation are ignored; a
superseded run cannot wedge the model; a panic surfaces as an error.
