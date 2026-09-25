# ADR-0003: Structure work as plan → divide → work with human approval gates

- **Status:** Accepted
- **Date:** 2026-05-01
- **Deciders:** Rinil Parmar
- **Related:** [ADR-0004](0004-judge-agent-synthesises-and-assigns.md), [ADR-0006](0006-session-scoped-state-on-disk.md)

## Context

Handing a large task to a single coding agent gives you one long opaque run that
you can only accept or throw away. Handing it to several agents at once without
structure gives you conflicting edits to the same files.

What makes multi-agent work useful is deciding *what* to build before deciding
*who* builds which part — and letting the human veto both, cheaply, before any
code is written.

## Decision

Every project moves through an explicit, persisted phase:

```
init → plan → divide → work → done
```

- **plan** — all enabled agents draft a plan in parallel; the judge synthesises
  one plan; the human approves or gives feedback (up to 3 rounds).
- **divide** — the judge breaks the approved plan into a task DAG with an agent
  assigned to each task; the human approves.
- **work** — tasks execute in dependency order; agents may pause for a human
  decision via the `<<AROS_HUMAN>>` sentinel.

The phase lives in `state.json` and gates the commands: `divide` refuses to run
before a plan is approved. Movement is forward-only by default; `--force`
(CLI) and `/phase <p> --force` (TUI) override it in both directions, because
being locked out of your own project is worse than an inconsistent phase.

The two approval gates are unconditional. Aros never writes code off an
unapproved plan.

## Consequences

**Good**
- The expensive, irreversible part (agents editing files) happens only after the
  human has seen and approved both the plan and the assignments.
- Rejecting a plan costs two cheap reasoning calls, not a work run.
- The phase gives every other part of the system — TUI header, CLI guards,
  resume-after-restart — one thing to key off.

**Bad**
- It is heavier than "just ask an agent to do it". For a one-line change the
  ceremony is not worth it; Aros is the wrong tool for that, and chat
  (ADR-0004) is the escape valve.
- Three phases mean three places where an agent's output can fail to parse or
  fail to satisfy the next phase's expectations.

**Neutral**
- Phases are per session, not per project (ADR-0006), so several independent
  workflows can coexist in one repo.

## Alternatives considered

- **One free-running agentic loop** — rejected: it is exactly what the
  underlying CLIs already do, and it gives the human no cheap veto point.
- **Approval after work, as a diff review** — rejected: by then the tokens are
  spent and the files are changed. Review-after remains possible with git; the
  gates exist to prevent obviously wrong work, not to replace code review.
- **More granular gates (per task)** — rejected as too noisy for a first
  version. `<<AROS_HUMAN>>` covers the case where a single task genuinely needs
  a decision.

## Notes

CLI: `cmd/plan_cmd.go`, `cmd/divide_cmd.go`, `cmd/work_cmd.go`.
TUI: `tui/phases.go`. Phase constants and guards: `state/state.go`
(`RequirePhase`). End-to-end coverage: `scripts/e2e-mock.sh`.
