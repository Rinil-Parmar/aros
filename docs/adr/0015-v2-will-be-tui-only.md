# ADR-0015: v2 will be TUI-only, dropping the Cobra CLI

- **Status:** Proposed
- **Date:** 2026-05-04
- **Deciders:** Rinil Parmar
- **Related:** [ADR-0010](0010-single-ui-agnostic-work-scheduler.md), [ADR-0003](0003-three-phase-workflow-with-approval-gates.md)

## Context

Aros ships two front ends over the same engine: a Cobra CLI (`aros plan`,
`aros divide`, `aros work`, `aros session`, `aros config`, `aros status`) and an
interactive TUI. Every user-facing change has to be made in both, and in
practice the TUI is what gets used — the CLI mainly serves scripting and the
mock end-to-end test.

The duplication is not free. Plan and divide still exist twice (`planner`/
`divider` for the CLI, `tui/phases.go` for the TUI); only the work phase is
shared (ADR-0010). Several past bugs existed in exactly one copy.

`AROS-V2-PLAN.md` proposes rebuilding around a single TUI with an
`internal/{agent,orchestrator,scheduler,judge,budget,memory,session,state,tui}`
layout, streaming adapters, a token budgeter and a command palette.

## Decision

*Proposed, not yet accepted.* v2 targets a **TUI-only** Aros:

- the Cobra command tree is dropped; `aros` launches the TUI,
- all phase logic lives behind one orchestrator, so no front end owns a copy,
- work continues on a `v2` branch; v1 is tagged `v1-final` before the rewrite.

This ADR records the intent and its cost so the decision is revisited
deliberately rather than drifted into.

## Consequences

**Good**
- One front end to build, test and document; the remaining plan/divide
  duplication disappears by construction.
- Removes the flag-parsing and terminal-detection paths that only the CLI needs.

**Bad**
- **Loses scriptability.** `aros plan … && aros divide && aros work` in CI or a
  shell script has no TUI-only equivalent. Anything automated would need a
  headless mode — which is the CLI under another name.
- `scripts/e2e-mock.sh`, currently the cheapest full-flow test, drives the CLI.
  It would have to be rewritten against the headless TUI harness.
- A rewrite risks losing v1's hard-won fixes (ADRs 0009–0014) if the new code
  does not carry the same invariants forward.

**Neutral**
- Nothing in v1 is removed by this ADR; it only records direction.

## Alternatives considered

- **Keep both front ends, finish unifying the phases** — lower risk and it
  addresses the actual duplication, without losing scripting. The main argument
  against is that the CLI's interactive prompts (`human.Confirm`) are a second
  approval UX to maintain.
- **TUI plus a deliberately minimal `--headless` mode** — probably the honest
  end state if v2 proceeds; it keeps CI and the e2e script working.

## Notes

`AROS-V2-PLAN.md` in the repo root. Revisit before starting the `v2` branch: if
scriptability matters, this ADR should be superseded by one that keeps a
headless path.
