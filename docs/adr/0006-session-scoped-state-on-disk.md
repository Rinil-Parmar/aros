# ADR-0006: Session-scoped state on disk, written atomically

- **Status:** Accepted
- **Date:** 2026-05-03
- **Deciders:** Rinil Parmar
- **Related:** [ADR-0003](0003-three-phase-workflow-with-approval-gates.md)

## Context

A project is not one linear workflow. The same repository may have an
in-progress refactor, an abandoned experiment, and a new feature — each with its
own plan, task list and phase. The first version kept a single
`.aros/state.json` per directory, so starting anything new destroyed the
previous work.

State is also written from several goroutines at once during the work phase
(each finishing task saves the manifest), and a half-written `manifest.json`
loses the whole run.

## Decision

State is per **session**, stored under the project's `.aros/` directory:

```
.aros/
├── config.toml               project-level config overrides
├── active-session.json       pointer to the current session id
└── sessions/<session-id>/
    ├── session.json          name, created_at, updated_at
    ├── state.json            phase, task, approved plan
    └── manifest.json         task list
```

Session IDs are `sess-<unixnano>-<4 random bytes>`, so they sort by creation and
never collide.

All JSON writes go through one helper that writes to a **unique** temp file in
the same directory (`os.CreateTemp`) and then `os.Rename`s it into place. Rename
within a directory is atomic, so a reader sees either the old file or the new
one, never a partial one. The temp file is unique per call, so concurrent saves
cannot clobber each other's staging file.

Legacy single-file state is migrated into a session on first read.

If `active-session.json` is missing but sessions exist, the most recently
updated one is adopted rather than erroring.

## Consequences

**Good**
- Multiple independent workflows per repo; switching is `/session use <id>`.
- A crash or a `kill -9` during the work phase cannot corrupt the manifest.
- `aros status` after a crash shows exactly where the run stopped, which is what
  makes retry (ADR-0012) meaningful.

**Bad**
- Session IDs are long and ugly in the UI; the TUI shows a shortened form, which
  means the full ID has to be copied from `/session list`.
- Nothing prunes old sessions. A long-lived repo accumulates them until the user
  runs `/session rm`.
- Two Aros processes in the same directory share the same files with no locking.
  Last writer wins. Not currently prevented.

**Neutral**
- `.aros/` is in `.gitignore`: state is local, not shared through the repo.
- Chat history is *not* persisted (v2 plans `chat.jsonl`; see ADR-0015).

## Alternatives considered

- **A single state file per project** — the original design; rejected once it
  became clear that starting a second task destroyed the first.
- **SQLite** — real transactions and querying, but adds a dependency and a
  migration story for state that is a few kilobytes of JSON a human may want to
  read or hand-edit.
- **State under `~/.aros/<project-hash>/`** — rejected: keeping state next to the
  code means moving or deleting the project takes its state with it.
- **Write-then-fsync in place** — not atomic; a crash mid-write truncates the file.

## Notes

`state/session.go`, `state/load.go` (`writeJSON` is the atomic helper).
