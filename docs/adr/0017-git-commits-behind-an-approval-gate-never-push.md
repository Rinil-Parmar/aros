# ADR-0017: Record agent changes as git commits behind an approval gate; never push

- **Status:** Accepted
- **Date:** 2026-10-30
- **Deciders:** Rinil Parmar
- **Related:** [ADR-0003](0003-three-phase-workflow-with-approval-gates.md), [ADR-0016](0016-no-file-level-conflict-protection.md)

## Context

Until now the work phase ran agents straight at the working tree and recorded
nothing. The user was left with the agents' prose claims and a dirty
`git status`. Three consequences:

- No way to see *what* a task changed, only what it said it changed.
- No rollback point. The only undo was whatever happened to be committed before
  the run.
- The lost-update bug in ADR-0016 is silent precisely because nothing observes
  the files.

Pushing is a different kind of act from committing. A local commit is private
and undone with `git reset`; a push is visible to collaborators, triggers CI,
and cannot be cleanly retracted.

## Decision

Aros records the work phase's changes as a git commit, gated by human approval,
and **has no push capability at all**.

- After the work phase (and after `aros commit` / `/commit`), Aros lists the
  changed files and asks. `git.commit = "ask"` is the default; `"auto"` commits
  without asking, `"off"` and `git.enabled = false` disable the layer.
- The gate holds the phase open: the TUI stays busy until it is answered, so the
  approval cannot be orphaned by a new command.
- Commits use the repo's own identity and carry **no trailers** — the project's
  history stays the author's own.
- `.aros/` is added to `.git/info/exclude` when Aros first opens the repo, so
  session state never enters the project's history and never shows up in
  `git status`. `CommitAll` refuses to commit if that exclude cannot be
  installed: silently writing session state into someone's history is worse
  than refusing.
- Not a git repository, or git not installed → the layer is skipped with a note,
  not an error. Not every project is a repo.
- A declined or unavailable prompt (piped stdin) leaves the work uncommitted and
  is **not** an error, so `aros work` stays scriptable.
- `package git` exposes no Push, Fetch or remote operation, and the package
  comment says why.

## Consequences

**Good**
- Every run produces a reviewable commit and a rollback point.
- An empty diff after a "successful" run is now visible: Aros says the agents
  reported success but changed nothing.
- The approval gate matches the plan and divide gates, so the model is
  consistent: nothing irreversible happens without a yes.
- Nothing leaves the machine. No network, no credentials, no remote.

**Bad**
- One commit per run, not per task. Accurate per-task attribution needs the
  tasks serialised (`max_concurrent = 1`) or worktrees, because with concurrent
  tasks a diff taken after one task also contains another's in-flight edits.
  Deliberately not faked.
- A tree that was already dirty gets swept into the commit. Aros warns before
  starting but cannot separate the user's edits from the agents'.
- An agent that runs `git` itself could interleave with Aros's commit.

**Neutral**
- `git.enabled` defaults to true, but the gate means the default cannot surprise
  anyone with an unwanted commit.

## Alternatives considered

- **Auto-commit with no gate** — rejected as the default: a commit the user did
  not ask for, in their own project history, is exactly the kind of surprise an
  approval-gated tool should not produce. Available as `commit = "auto"`.
- **Push / open a PR automatically** — rejected outright, and the capability is
  not in the code. Hard to retract, and it turns a local mistake into a public
  one.
- **Commit per task** — the better end state, but honest attribution requires
  serialisation; see ADR-0016's lease design. Revisit together.
- **A branch per run** (`aros/run-<ts>`) — keeps the main branch untouched
  mid-run. A good next step; left out to keep this change small.
- **Stash the user's changes before a run** — too invasive to do on the user's
  behalf; warning and letting them decide is safer.

## Notes

`git/git.go` (no Push, by design), `git/message.go`, wired in
`cmd/work_cmd.go` (`commitRun`), `cmd/commit_cmd.go`, `tui/phases.go`
(`commitChanges`). Tested in `git/git_test.go` against a real repo — including
that `.aros` is never committed and that the commit carries no trailers — and in
`tui/tui_test.go`, where the mock agent writes real files and the gate is both
approved and declined.
