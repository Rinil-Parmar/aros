# ADR-0004: One judge agent synthesises plans and assigns tasks

- **Status:** Accepted
- **Date:** 2026-05-01
- **Deciders:** Rinil Parmar
- **Related:** [ADR-0003](0003-three-phase-workflow-with-approval-gates.md), [ADR-0013](0013-tool-less-reasoning-tool-enabled-work.md)

## Context

The plan phase asks every enabled agent for a plan, which produces N different
plans of varying quality. Something has to turn them into one plan. The options
are: pick one by rule, merge them mechanically, ask the human to merge, or ask
an agent to merge.

The same question arises in the divide phase: some single actor has to produce
one task DAG with one assignment per task.

## Decision

One configured agent — `judge.agent`, default `claude` — is the judge. It:

1. synthesises the parallel plans into one plan,
2. breaks the approved plan into the task DAG and assigns each task to an agent,
3. answers free-text chat in the TUI.

The judge is excluded from the parallel plan-drafting round (`Registry.Enabled`
takes the judge name and filters it out) so it critiques work it did not write.
The exception is a single-agent setup: if the judge is the only available agent
it drafts a plan and then synthesises from it, so Aros still works with just
`claude` installed.

Task assignment is advisory. The judge is told which agents are actually
available and their configured `strengths`, but its output is validated: unknown
agent names are reassigned to the judge with a visible note rather than trusted
(see ADR-0005).

There is no silent fallback if the configured judge is unavailable — the phase
fails with a message naming the available agents. An earlier version silently
promoted a different agent to judge, which meant runs quietly used a model the
user had not chosen.

## Consequences

**Good**
- The synthesis step reliably produces something better than the worst plan, and
  usually better than any single one, because it can take the strongest parts.
- One clear owner for every "decide between agents" question.
- The judge doubles as the chat agent, so no extra configuration for chat.

**Bad**
- The judge is a single point of taste. A weak judge model degrades every phase,
  and the parallel plans it summarises are only as useful as its summary.
- The synthesis call re-sends every agent's full plan, so its prompt grows with
  the number of agents. This is the largest single prompt in the system.
- Judge output must parse as JSON in the divide phase; when it does not, the
  retry loop costs extra calls (mitigated by ADR-0013).

**Neutral**
- `/judge <agent>` changes the judge for the current TUI session only; making it
  permanent is `aros config set judge.agent <name>` (see ADR-0007).

## Alternatives considered

- **Human merges the plans** — rejected: it is the tedious part, and reading N
  long plans is worse than reviewing one synthesis.
- **Pick the longest / first plan** — rejected: length is not quality, and it
  throws away the other agents' work entirely.
- **Round-robin or rule-based assignment by strengths** — rejected as the
  primary mechanism; the `strengths` list is fed to the judge as input instead,
  which keeps assignment sensitive to the actual task. Validation then catches
  bad assignments.
- **Two judges voting** — doubles cost for a benefit that was not measurable at
  this scale.

## Notes

`agent/registry.go` (`Judge`, `Enabled`), `planner/planner.go`,
`divider/divider.go`, `tui/phases.go`. Prompt builders live in
`tui/prompts.go` and `divider.BuildDividePrompt`.
