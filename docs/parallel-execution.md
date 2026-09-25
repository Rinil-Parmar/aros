# Parallel execution: how tasks run, how output combines, and where it breaks

What actually happens when the work phase runs two agents at once. Line
references are to the state of the repo on 2026-09-25.

Related: [ADR-0010](adr/0010-single-ui-agnostic-work-scheduler.md) (scheduler),
[ADR-0012](adr/0012-blocked-tasks-cascade-and-retry.md) (failure handling),
[ADR-0016](adr/0016-no-file-level-conflict-protection.md) (the collision risk).

---

## 1. The scheduler

`worker.Run` (`worker/worker.go`) is shared by the CLI and the TUI.

- Tasks form a **DAG** — each carries `dependencies: []taskID`.
- A poll loop dispatches every task whose dependencies are all `done`, bounded by
  `work.max_concurrent` (**default 2**).
- The semaphore is acquired **non-blockingly** (`select`/`default`,
  `worker/worker.go:139`) so the dispatcher never sleeps while holding the
  mutex that finishing tasks need.
- A completed task pushes to a wake channel, so the next task dispatches
  immediately instead of waiting for the 2s poll tick.
- Each task is one subprocess in its own process group
  ([ADR-0011](adr/0011-subprocess-process-groups-and-stdin-prompts.md)).

Ordering is deterministic: ready tasks are dispatched sorted by ID
(`sortedTasks`, `worker/worker.go:240`).

## 2. What "combining output" actually means

There is exactly **one** combination mechanism, and it is textual:

```go
// state/tasks.go:202
func CollectDepOutputs(t *Task, byID map[string]*Task) string {
    // for each completed dependency, in dependency order:
    //   "=== <title> [<id>] ===\n<that agent's stdout>"
}
```

That string is injected into the dependent task's prompt under
`OUTPUTS FROM PREREQUISITE TASKS:` (`worker.BuildWorkPrompt`). Each task's output
is also persisted to `manifest.json`.

So when `task-002` (opencode) depends on `task-001` (copilot), opencode receives
**copilot's prose summary of what it did** — not its diff, not a list of files it
touched, not its exit state. Coordination between agents is a summary passed
through a prompt.

**Nothing merges file edits, because nothing intercepts them.**
`agent.BuildRegistry(cfg, cwd)` gives every adapter the *same* `WorkDir` — the
project root:

| Agent | Invocation in the work phase | Writes |
|---|---|---|
| claude | `claude -p … [--dangerously-skip-permissions]` | directly into `cwd` |
| copilot | `copilot -p … --allow-all` | directly into `cwd` |
| opencode | `opencode run … -m <model>` | directly into `cwd` |

All three edit your real working tree, concurrently, with no coordination.

## 3. What Aros does **not** do

Verified by grep over the non-test sources:

```
worktree · flock · lockfile · conflict · merge · git  →  no matches
```

- No file locking or leases.
- No per-task git worktree or branch.
- No diff/patch application layer.
- No conflict detection, before or after.
- No git commit or snapshot, so no rollback point.

## 4. Failure modes when two agents touch one file

Ordered by likelihood, worst first.

**1. Lost update.** Both agents read `main.go`, both hold it in context, both
write a full version back. The second write wins completely; the first agent's
work is gone. Both tasks still report success and are marked `done` with
plausible output. **A green run with half the work missing.**

**2. Torn read/write.** Agent B reads while agent A is mid-write, gets a
truncated file, "repairs" it, and writes that back.

**3. Stale-read semantic conflict.** Both add a `parseConfig()` with different
signatures in different places. No textual conflict — the result just contains
duplicated or contradictory code.

**4. No recovery.** Aros never commits to git and takes no snapshot. The only
undo is whatever you committed before running `work`.

This is **not** a git merge conflict. Git would stop and tell you. Here the
writes are ordinary `write()` calls from two unrelated processes, so they
silently overwrite.

## 5. Why it usually doesn't blow up

- Dependency edges serialise genuinely related work — a task and its dependent
  never run concurrently.
- The divide prompt asks the judge for tasks that are "concrete and completable
  by a single agent," which nudges toward independence.
- You see the full task table at the divide approval gate before anything runs.

## 6. Why the risk is real anyway

The judge has **no file-level knowledge**. It never reads the repository — and
since [ADR-0013](adr/0013-tool-less-reasoning-tool-enabled-work.md) it runs
tool-less, so it definitively cannot. It splits a prose plan into prose tasks.
"Implement the storage layer" and "Add error handling" sound independent and can
land in the same file.

`max_concurrent` defaults to **2**, so the risk is live out of the box.

## 7. Mitigations available today

```toml
[work]
max_concurrent = 1     # serialise; the only real safety switch that exists
```

- **Commit to git before every `work` run.** Non-negotiable until this is fixed.
- At the divide approval gate, read the task list asking *"could any two of these
  touch the same file?"* — answer `n` and give feedback if yes.
- Prefer plans whose tasks are split by module, not by concern.

## 8. Proposed fixes

| Approach | Cost | What it buys |
|---|---|---|
| **File leases** — add `assigned_files: []` to the task schema; the dispatcher refuses to co-schedule tasks with overlapping sets | Medium | *Prevents* the collision; one extra field for the judge, one validation, one dispatcher check. Best value. |
| **git worktree per task**, merge on completion | High | True isolation; collisions become visible git conflicts |
| **Patch mode** — agents emit unified diffs, Aros applies them serially | High | Parallel thinking, serialised writing |
| **Snapshot + verify** — hash files before/after each task, flag unexpected changes | Low | Detection only, but converts silent loss into a loud warning |

See [ADR-0016](adr/0016-no-file-level-conflict-protection.md) for the decision
record and the reasoning behind preferring leases.
