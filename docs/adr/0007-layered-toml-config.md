# ADR-0007: Layered TOML config; runtime edits are session-only

- **Status:** Accepted
- **Date:** 2026-05-01 (runtime-edit semantics clarified 2026-09-17)
- **Deciders:** Rinil Parmar
- **Related:** [ADR-0002](0002-drive-vendor-clis-as-subprocesses.md)

## Context

Some settings are personal and global (which agents I have installed, my default
models). Some are per project (this repo's work is Go, so use a stronger model;
disable the agent that has no credentials here). And during a session a user
wants to try a different model without editing files.

## Decision

Three layers, merged by viper, later overriding earlier:

1. built-in defaults in `config.setDefaults`,
2. global `~/.aros/config.toml` (or `--config <path>`),
3. project `./.aros/config.toml`.

After unmarshalling, `Config.normalize()` clamps values that would otherwise
break the system downstream: `max_concurrent < 1 → 1`,
`agent_timeout_seconds < 1 → 300`, empty judge → `claude`, an unrecognised
`dense` level → off. Callers therefore never re-validate config.

Defaults are cheap models (`claude` → `haiku`) because the default should not
surprise the user with a large bill.

The TUI commands `/model`, `/dense` and `/judge` change the **in-memory** config
for the current session only. They say so, and point at
`aros config set <key> <value>`, which writes the global file. Changing a model
rebuilds the agent registry immediately.

Enabled agents whose binary is missing are dropped from the registry with a
warning, not an error — a config listing three agents still works on a machine
with one installed.

## Consequences

**Good**
- A project can pin its own models and disable agents without touching the
  user's global setup.
- Experimentation in the TUI is free and never corrupts a config file.
- Normalisation at the boundary removed a whole class of downstream bugs (a
  `max_concurrent` of 0 used to deadlock the scheduler).

**Bad**
- Runtime edits being session-only is a real surprise; it has to be communicated
  in the command's own output because there is nowhere else the user looks.
- viper merges whole keys, so a project config that sets `[agents.claude]` at all
  must repeat the fields it wants from the global file for that agent.
- `aros config set` writes the whole resolved config, including defaults, into
  the global file — which then pins today's defaults for future versions.

**Neutral**
- `agents.<name>.api_key` exists as an env passthrough for OpenCode only
  (ADR-0002); no other credential is ever read.

## Alternatives considered

- **Persist `/model` and `/judge` immediately** — rejected for now: a quick
  experiment in one project would silently change the user's global default.
  A future `/model … --save` is the obvious middle ground.
- **Flags only, no config file** — rejected: per-agent model, strengths and
  dense level are too much to retype per invocation.
- **JSON or YAML config** — TOML reads better for this shape and is what the
  Go ecosystem around viper handles most predictably.

## Notes

`config/config.go`. Precedence and clamping are exercised indirectly by
`scripts/e2e-mock.sh`, which relies on a project config overriding defaults.
