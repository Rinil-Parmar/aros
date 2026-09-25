# ADR-0016: Parallel tasks share one working tree with no conflict protection

- **Status:** Accepted (as a known limitation)
- **Date:** 2026-09-25
- **Deciders:** Rinil Parmar
- **Related:** [ADR-0010](0010-single-ui-agnostic-work-scheduler.md), [ADR-0005](0005-task-manifest-as-validated-json-dag.md), [ADR-0013](0013-tool-less-reasoning-tool-enabled-work.md)

## Context

The work phase runs up to `work.max_concurrent` agents at once (**default 2**).
Every adapter is constructed with the same `WorkDir` — the project root
(`agent.BuildRegistry(cfg, cwd)`) — and each agent CLI edits files directly:
claude with `--dangerously-skip-permissions`, copilot with `--allow-all`,
opencode plainly.

Aros coordinates *scheduling* but not *writes*. A grep over the non-test sources
for `worktree`, `flock`, `lockfile`, `conflict`, `merge` and `git` returns
nothing. There is no locking, no per-task isolation, no patch layer, no conflict
detection, and no snapshot to roll back to.

The only inter-task data flow is textual: `state.CollectDepOutputs` pastes a
completed dependency's stdout into the dependent task's prompt. An agent learns
what another agent *said* it did, never what it actually changed.

The judge that assigns tasks has no file-level knowledge. It never reads the
repository, and since ADR-0013 it runs tool-less so it cannot. It splits a prose
plan into prose tasks. Two tasks that read as independent — "implement the
storage layer", "add error handling" — can land in the same file.

Concrete consequence: two concurrent tasks editing one file produce a **lost
update**. Both agents read the file, both write a full version back, the second
wins, and the first agent's work disappears — while both tasks report success and
are marked `done`. The run looks green and is silently half-finished. Unlike a
git merge conflict, nothing stops or warns; these are ordinary `write()` calls
from unrelated processes.

## Decision

We accept this limitation for v1 and document it, rather than shipping a partial
mitigation that implies a safety guarantee Aros does not provide.

Specifically:
- Concurrency stays opt-out via `work.max_concurrent = 1`, which is the only
  real safety switch and is documented as such.
- The risk is stated in the README, in `docs/parallel-execution.md` and here,
  including the "reports success while losing work" failure mode.
- The recommended workflow is to commit to git before every `work` run, and to
  read the task table at the divide approval gate specifically asking whether two
  tasks could touch the same file.
- When this is fixed, the intended mechanism is **file leases**: add
  `assigned_files: []` to the task schema, have the judge populate it, validate
  it in `state.ValidateTasks`, and have the dispatcher refuse to co-schedule
  tasks whose sets intersect. This fits the existing DAG and manifest with no
  architectural change.

## Consequences

**Good**
- The scheduler stays simple, and single-concurrency runs are completely safe.
- Serialised execution remains available with one config line, so the risk is
  avoidable today without new code.
- Writing the failure mode down makes it reviewable at the divide gate, which is
  where a human can actually catch it.

**Bad**
- With the default `max_concurrent = 2`, a user who never reads the docs can
  silently lose work on a green run. This is the most dangerous behaviour in the
  system: everything else either degrades gracefully or fails loudly.
- "Documented" is not "mitigated". The default remains unsafe for plans whose
  tasks overlap.
- Users are pushed to `max_concurrent = 1`, which forfeits the parallelism that
  motivates a multi-agent orchestrator in the first place.

**Neutral**
- Dependency edges already serialise related work, so the exposure is limited to
  tasks the judge believed were independent.

## Alternatives considered

- **Default `max_concurrent` to 1** — safe, and genuinely tempting. Rejected for
  now because it turns the product's headline capability off by default to work
  around a bug that has a real fix; the honest move is to fix it, not to hide it.
  This is the first thing to revisit if a user actually loses work.
- **Per-task git worktree + merge** — the strongest isolation, and it converts
  collisions into visible git conflicts. Rejected for v1: it requires the project
  to be a git repo, changes every agent's `WorkDir`, and needs a merge strategy
  for agents that produce whole-file rewrites.
- **Patch mode (agents emit diffs, Aros applies serially)** — clean, but no agent
  CLI reliably emits a machine-applicable diff in non-interactive mode; it would
  mean fighting each CLI's output format (ADR-0002).
- **Advisory file locks (`flock`)** — cheap to add, but Aros does not mediate the
  writes, so it could only lock files an agent *declared*, which is the lease
  design with a worse mechanism.
- **Snapshot + verify (hash before/after each task)** — detection only, and it
  reports the loss after the tokens are spent. Worth adding as a warning even
  alongside leases, but insufficient on its own.

## Notes

`worker/worker.go` (scheduler), `agent/registry.go` (shared `WorkDir`),
`state/tasks.go` (`CollectDepOutputs`). Full analysis with failure modes in
[`docs/parallel-execution.md`](../parallel-execution.md).

No test covers this: the harness uses mock agents that do not write files, so the
collision is invisible to the suite. A regression test for the lease fix would
need two mock agents that write to the same path.
