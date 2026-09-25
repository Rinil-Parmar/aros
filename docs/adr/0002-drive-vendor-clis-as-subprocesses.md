# ADR-0002: Drive vendor CLIs as subprocesses, not provider SDKs

- **Status:** Accepted
- **Date:** 2026-05-01
- **Deciders:** Rinil Parmar
- **Related:** [ADR-0011](0011-subprocess-process-groups-and-stdin-prompts.md), [ADR-0013](0013-tool-less-reasoning-tool-enabled-work.md)

## Context

Aros orchestrates several AI coding agents. Each vendor offers two ways in: a
provider HTTP API (Anthropic, OpenAI, …) or the vendor's own CLI (`claude`,
`opencode`, `copilot`).

Going through provider APIs would mean Aros holds API keys, re-implements tool
use, file editing, permission prompts and session handling — that is, rebuilding
the coding agent itself. The whole value of Aros is that these agents already
exist and are good; it wants to *coordinate* them, not replace them.

The developer running Aros already has these CLIs installed and authenticated
(`claude`, `opencode auth login`, `copilot login`). A GitHub Copilot subscription
in particular has no usable raw-API equivalent.

## Decision

Every agent is an adapter that shells out to the vendor's CLI in
non-interactive mode and parses its JSON output. Adapters implement:

```go
type Agent interface {
    Name() string
    Run(ctx context.Context, prompt string) (AgentResult, error)
}
```

Authentication is entirely the CLI's own — Aros never reads or stores
credentials. `agents.<name>.api_key` exists only as an optional environment
passthrough for OpenCode, which sometimes wants a key the CLI has not stored.

Adding an agent means adding one adapter file; the rest of the system only sees
the interface. Unknown agent names in config fall through to the OpenCode
adapter, so `[agents.opencode-gemini]` works with no code change.

## Consequences

**Good**
- No API keys in Aros' config, no key handling code, no secret-leak surface.
- Aros inherits each CLI's tool set, permission model, MCP servers and updates
  for free.
- Works offline-ish and local-first: everything runs as a child process of the
  user's own session.

**Bad**
- Aros is coupled to CLI flags and stdout formats, which are not stable APIs.
  This has already broken twice: `copilot --model gpt-5.4-mini` became
  "not available", and OpenCode's event JSON changed shape. Adapters must treat
  parsing defensively and tests pin the observed formats.
- Per-call process startup costs hundreds of milliseconds to seconds — far more
  than an HTTP call. Acceptable because agent latency dominates anyway.
- No token-level streaming: each adapter returns one complete result.
- Cost and token accounting are only as good as what the CLI reports.

**Neutral**
- An agent that is configured but whose binary is missing is skipped with a
  warning rather than being a fatal error (see ADR-0007).

## Alternatives considered

- **Provider SDKs / raw APIs** — rejected: requires API keys, and re-implements
  the agent loop (tools, edits, permissions) that the CLIs already provide.
- **A mix: API for reasoning, CLI for work** — rejected as premature. It would
  split authentication across two models and double the failure modes for a
  benefit (cheaper reasoning calls) that ADR-0013 largely achieves anyway.
- **MCP servers as the integration point** — interesting, but MCP standardises
  *tools for* an agent, not *invocation of* an agent. Wrong layer.

## Notes

Adapters: `agent/claude.go`, `agent/opencode.go`, `agent/copilot.go`, wired in
`agent/registry.go`. Output-format regressions are guarded by
`agent/parse_test.go`, which uses JSON captured from real CLI runs.
