# ADR-0018: Runtime settings persist to the project config, written key by key

- **Status:** Accepted
- **Date:** 2026-10-30
- **Deciders:** Rinil Parmar
- **Supersedes in part:** [ADR-0007](0007-layered-toml-config.md)
- **Related:** [ADR-0001](0001-record-architecture-decisions.md)

## Context

ADR-0007 decided that `/model`, `/judge` and `/dense` would change only the
in-memory config, on the reasoning that writing to the global file would be a
surprise: a quick experiment in one project would silently change the user's
default everywhere.

In use, the opposite was the surprise. Setting a model and finding it gone is
the complaint that produced this ADR:

> "I change model to sonnet then i chat it went back to haiku"

The model had not reverted mid-session — a test confirms the running session
does use the new model. It reverted on *reopening*, because the config file
still said otherwise, and for a user with no model in their config that means
the built-in default, `haiku`. The message said "this session only", which
nobody reads as "and it will be thrown away".

ADR-0007 also noted a second problem as a known downside: `aros config set`
used viper's `WriteConfigAs`, which serialises the *whole resolved
configuration*. One `config set` therefore baked every current default into the
user's file — pinning today's defaults forever — and in a project config it
overrode their global settings for every key, not the one they changed.

## Decision

Runtime settings persist, and every write is surgical.

- `/model`, `/judge` and `/dense` write to **`./.aros/config.toml`** by default.
  A model choice belongs to the project being worked on, not to every project
  the user owns — which is what made global writes feel wrong in ADR-0007.
- `--global` writes `~/.aros/config.toml`; `--session` writes nothing, for
  trying something out. Each confirmation names the file it wrote, so a change
  is never silently scoped.
- `config.SetValue` reads the file, sets one dotted key, and writes it back, so
  no key the user did not name is ever added or changed. `aros config set` uses
  the same path, replacing `WriteConfigAs`.
- The file's leading comment block is preserved across writes, because a TOML
  round-trip through a map drops comments and the header is where the file
  explains itself.
- Writes go through a temp file and a rename, so a crash cannot truncate a
  config, and are serialised by a mutex.

## Consequences

**Good**
- Choosing a model means choosing a model. The common case needs no second
  command, and the confirmation says where the setting lives.
- Config files stay small and readable: only what the user actually set.
- A project can pin its own model without touching the user's global defaults —
  the property ADR-0007 wanted, now achieved by scope rather than by refusing
  to write at all.
- Upgrades no longer freeze old defaults into a file the first time anyone runs
  `config set`.

**Bad**
- Comments *within* the config body are still lost on a write; only the leading
  block survives. Preserving them all would need a line-based editor rather
  than a TOML round-trip.
- `/model` now writes to disk from the TUI, so a read-only or permission-denied
  project directory turns a settings change into a visible error. It degrades to
  session-only with a message rather than failing the command.
- A project-scoped setting is invisible from another directory, which can be
  confusing when the same agent behaves differently in two checkouts. `/model`
  with no arguments shows what is actually in effect.

**Neutral**
- `--session` keeps the old behaviour available for experiments.

## Alternatives considered

- **Keep session-only, document it louder** — rejected: the message already
  said so and was still surprising. A setting that evaporates is a bug wearing
  documentation.
- **Default to the global config** — rejected for ADR-0007's original reason: a
  one-off experiment should not change every project. Available via `--global`.
- **Prompt for the scope each time** — an extra decision on a frequent action,
  to avoid a wrong guess that `/model` already displays and `--global` already
  fixes.
- **Preserve all comments with a line-based TOML editor** — more faithful, but
  it has to handle nested tables, inline tables, arrays and multi-line strings
  to avoid corrupting a file. Keeping the header covers the case that matters.

## Notes

`config/write.go` (`SetValue`, `SetAgentModel`, `SetJudge`, `SetAgentDense`,
`leadingComments`), wired in `tui/models.go` (`applyModel`), `tui/model.go`
(`parseScope`, `persistSetting`) and `cmd/config_cmd.go`.

Tested in `config/write_test.go` — one key changed and nothing else, no
defaults pulled in, the comment header kept, types preserved, scopes correct —
and in `tui/modelswitch_test.go`, where a mock agent echoes back the `--model`
it was invoked with, so the test checks the model actually used rather than the
one the UI claims. `TestModelChangeSurvivesRestart` is the regression for the
reported bug.
