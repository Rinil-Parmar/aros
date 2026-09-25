# ADR-0005: The task manifest is a validated JSON DAG

- **Status:** Accepted
- **Date:** 2026-05-02 (validation hardened 2026-09-17)
- **Deciders:** Rinil Parmar
- **Related:** [ADR-0004](0004-judge-agent-synthesises-and-assigns.md), [ADR-0012](0012-blocked-tasks-cascade-and-retry.md)

## Context

The divide phase has to turn prose into something schedulable. That means a
machine-readable structure, produced by a language model, which is an
unreliable producer: it wraps JSON in markdown fences, prepends prose, invents
agent names, reuses IDs, and occasionally emits a dependency cycle.

Early versions parsed this optimistically and the failures were ugly: blank task
IDs (because IDs were assigned *after* cycle detection), tasks assigned to
agents that did not exist and silently rerouted to a random registry entry, and
a `[]` somewhere in the prose being parsed as an empty task list.

## Decision

Tasks are a JSON array persisted as `manifest.json`:

```json
{"id":"task-001","title":"…","description":"…",
 "assigned_to":"claude","dependencies":["task-000"]}
```

with `status` (`pending|in_progress|done|blocked`), `output` and `block_reason`
maintained by the scheduler.

Everything that turns agent output into a manifest goes through one package,
`state/tasks.go`, in a fixed order:

1. **`ParseTasks`** — tolerant extraction: prefer a fenced block, otherwise try
   *every* `[` in the output as the start of the array until one decodes into a
   non-empty array. This survives prose before and after the JSON.
2. **`AssignIDs`** — fill missing IDs and default status. Runs *before*
   validation, because validating nodes with blank IDs is meaningless.
3. **`ValidateTasks`** — unique IDs, no self-dependency, every dependency exists,
   no cycles (DFS with in-stack marking).
4. **`NormalizeAssignments`** — lowercase `assigned_to`; any name not in the live
   registry is reassigned to the judge and reported to the user by ID.

A manifest that fails validation is fed back to the judge as feedback and
retried (3 attempts); it is never persisted.

## Consequences

**Good**
- One implementation, one set of tests, shared by the CLI divide phase, the TUI
  divide phase and the scheduler. Previously each had its own copy, with its own
  bugs.
- Invalid graphs are caught before any agent runs, and the error tells the judge
  exactly what to fix.
- The DAG is what makes bounded parallelism possible (ADR-0010).

**Bad**
- The tolerant parser can in principle latch onto a JSON array in the prose that
  is not the task list. It takes the first array that decodes into non-empty
  tasks, which is a heuristic, not a guarantee.
- Reassigning unknown agents to the judge can concentrate all work on one agent
  if the judge keeps inventing names.
- The schema is implicit in the Go structs; there is no published JSON schema.

**Neutral**
- `dependencies` are task IDs only — no file-level or resource-level dependency
  modelling. Two tasks editing the same file can still race; the human is
  expected to catch that at the approval gate.

## Alternatives considered

- **Strict JSON parsing, fail on any deviation** — rejected: models wrap JSON in
  fences often enough that this would fail most first attempts.
- **Ask the judge for YAML / a bespoke text format** — no better on reliability
  and worse on tooling.
- **Let the judge return a flat list with no dependencies** — rejected: the
  dependency edges are what allow safe parallelism and dependency-output
  threading into prompts.

## Notes

`state/tasks.go`, tested in `state/tasks_test.go` (fenced JSON, prose with a
decoy `[`, duplicate IDs, self-dependency, cycles, unknown agents).
