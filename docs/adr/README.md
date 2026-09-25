# Architecture Decision Records

Why Aros is built the way it is. One file per decision, numbered in order,
never renumbered. See [ADR-0001](0001-record-architecture-decisions.md) for the
process and [template.md](template.md) to start a new one.

**Status values:** `Accepted` — in force. `Proposed` — intended, not yet
committed to. `Superseded by ADR-NNNN` — replaced; kept for the reasoning.

An accepted ADR is not edited. To change a decision, add a new ADR and mark the
old one superseded.

## Index

| # | Decision | Status | Decided |
|---|---|---|---|
| [0001](0001-record-architecture-decisions.md) | Record architecture decisions | Accepted | 2026-09-25 |
| [0002](0002-drive-vendor-clis-as-subprocesses.md) | Drive vendor CLIs as subprocesses, not provider SDKs | Accepted | 2026-05-01 |
| [0003](0003-three-phase-workflow-with-approval-gates.md) | Structure work as plan → divide → work with approval gates | Accepted | 2026-05-01 |
| [0004](0004-judge-agent-synthesises-and-assigns.md) | One judge agent synthesises plans and assigns tasks | Accepted | 2026-05-01 |
| [0005](0005-task-manifest-as-validated-json-dag.md) | The task manifest is a validated JSON DAG | Accepted | 2026-05-02 |
| [0006](0006-session-scoped-state-on-disk.md) | Session-scoped state on disk, written atomically | Accepted | 2026-05-03 |
| [0007](0007-layered-toml-config.md) | Layered TOML config; runtime edits are session-only | Accepted | 2026-05-01 |
| [0008](0008-secondmem-optional-best-effort-memory.md) | secondmem is optional, best-effort shared memory | Accepted | 2026-05-01 |
| [0009](0009-bubbletea-message-only-concurrency.md) | In the TUI, goroutines talk to the model only through messages | Accepted | 2026-09-17 |
| [0010](0010-single-ui-agnostic-work-scheduler.md) | One UI-agnostic work scheduler behind a Hooks interface | Accepted | 2026-09-17 |
| [0011](0011-subprocess-process-groups-and-stdin-prompts.md) | Run agents in their own process group; prompts on stdin | Accepted | 2026-09-17 |
| [0012](0012-blocked-tasks-cascade-and-retry.md) | A failed task blocks its dependents; the next run retries them | Accepted | 2026-09-17 |
| [0013](0013-tool-less-reasoning-tool-enabled-work.md) | Reasoning steps run tool-less; only work gets tools | Accepted | 2026-09-23 |
| [0014](0014-generation-gating-and-universal-cancel.md) | Gate UI messages by generation; one universal cancel key | Accepted | 2026-09-23 |
| [0015](0015-v2-will-be-tui-only.md) | v2 will be TUI-only, dropping the Cobra CLI | **Proposed** | 2026-05-04 |

## Reading order

New to the codebase — the shape of the system:
[0002](0002-drive-vendor-clis-as-subprocesses.md) →
[0003](0003-three-phase-workflow-with-approval-gates.md) →
[0004](0004-judge-agent-synthesises-and-assigns.md) →
[0005](0005-task-manifest-as-validated-json-dag.md) →
[0006](0006-session-scoped-state-on-disk.md)

Before touching the TUI:
[0009](0009-bubbletea-message-only-concurrency.md) and
[0014](0014-generation-gating-and-universal-cancel.md). Both encode invariants
that were expensive to learn; breaking either deadlocks or wedges the UI.

Before touching agent invocation:
[0013](0013-tool-less-reasoning-tool-enabled-work.md) and
[0011](0011-subprocess-process-groups-and-stdin-prompts.md).

Before touching the work phase:
[0010](0010-single-ui-agnostic-work-scheduler.md) and
[0012](0012-blocked-tasks-cascade-and-retry.md).
