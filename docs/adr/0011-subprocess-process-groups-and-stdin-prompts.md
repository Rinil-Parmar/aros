# ADR-0011: Run agents in their own process group; pass prompts on stdin

- **Status:** Accepted
- **Date:** 2026-09-17
- **Deciders:** Rinil Parmar
- **Related:** [ADR-0002](0002-drive-vendor-clis-as-subprocesses.md)

## Context

The agent CLIs are Node programs that spawn children of their own. Two problems
followed from running them naively with `exec.CommandContext`:

- **Timeouts did not stop anything.** `CommandContext` kills only the direct
  child. Its children survived, kept the stdout pipe open, and the `Run` call
  blocked past its own deadline while orphaned processes kept working.
- **Large prompts risked the argv limit.** A prompt containing an approved plan
  plus dependency outputs plus memory context can reach hundreds of kilobytes;
  Linux caps a single argument at 128 KiB (`MAX_ARG_STRLEN`), and the failure
  mode is a confusing `argument list too long` rather than a clear error.

## Decision

One shared `runCommand` in `agent/agent.go` does all subprocess execution:

- `SysProcAttr{Setpgid: true}` puts the child in a new process group;
  `cmd.Cancel` sends `SIGTERM` to `-pid` (the whole group) and a `SIGKILL` to the
  group follows if the process is still around after the wait.
- `cmd.WaitDelay` bounds how long `Run` waits on inherited pipes after
  cancellation, so a child holding stdout can no longer hang the caller.
- The prompt is written to the child's **stdin** rather than passed as an
  argument. `claude -p` with no positional prompt reads stdin, which is the
  documented way to do this.
- On a context cancellation the returned error is the context error, so callers
  can distinguish "timed out / cancelled" from "the agent failed".
- stderr is captured and appended (tail-truncated to 800 bytes) to the error, so
  a CLI's own diagnostics reach the user.

## Consequences

**Good**
- Cancelling a phase (Esc, `--force`, Ctrl+C, timeout) actually stops the agents
  within seconds, rather than leaving background work running and billing.
- Prompt size is limited by memory, not by argv.
- One place to fix subprocess behaviour for all adapters.

**Bad**
- `Setpgid` and signalling a process group are POSIX. Windows would need a
  different implementation (job objects); Aros is Linux/macOS only today.
- Killing the group is abrupt: an agent mid-edit can leave a partially written
  file. Acceptable given the alternative is not being able to stop it at all.
- Adapters that want to stream output cannot, since `runCommand` buffers
  stdout fully.

**Neutral**
- Stdin is used for the prompt, so no adapter can also use stdin
  interactively — which is correct for non-interactive mode anyway.

## Alternatives considered

- **Write the prompt to a temp file and pass the path** — works, but every CLI
  handles file arguments differently and it leaves files to clean up.
- **Kill only the direct child and hope** — the original behaviour; orphaned
  agents kept running and kept spending tokens.
- **`prctl(PR_SET_PDEATHSIG)`** — Linux-only and only covers the immediate child.

## Notes

`agent/agent.go`. `agent/parse_test.go` has a regression test that runs
`sh -c 'sleep 30 & wait'` under a 300 ms deadline and asserts it returns
promptly with `context.DeadlineExceeded`.
