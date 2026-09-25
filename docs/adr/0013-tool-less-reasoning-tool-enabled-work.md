# ADR-0013: Reasoning steps run tool-less; only the work phase gets tools

- **Status:** Accepted
- **Date:** 2026-09-23
- **Deciders:** Rinil Parmar
- **Related:** [ADR-0002](0002-drive-vendor-clis-as-subprocesses.md), [ADR-0004](0004-judge-agent-synthesises-and-assigns.md)

## Context

Aros invokes agents for two very different purposes: to **think** (draft a plan,
synthesise plans, split a plan into JSON tasks, answer a chat question) and to
**act** (edit files during the work phase).

Every call used the same invocation, with tools enabled. That is the default
mode of a coding CLI, and it made the reasoning phases behave badly. From a real
run against `claude` (haiku) on the task *"create hello.txt containing hello and
bye.txt containing bye"*:

- The plan phase returned **"Both files are ready to be created in parallel once
  you approve the permissions"** — the agent had spent the call trying to *do*
  the task instead of planning it.
- The divide phase never produced a JSON array on the first try. It burned all
  3 attempts × 2 parse retries — **9 agent calls** — and only produced tasks on
  the last one.

The cause is structural, not prompt wording: with tools available, an agentic
CLI's natural response to "here is a task" is to start executing it.

## Decision

Invocation mode is chosen by *purpose*, not left to the adapter's default:

```go
// agent/agent.go
func Reason(ctx context.Context, a Agent, prompt string) (AgentResult, error) {
    if ca, ok := a.(ChatAgent); ok {
        return ca.Chat(ctx, prompt)   // tool-less
    }
    return a.Run(ctx, prompt)          // fallback
}
```

- **Reasoning** — plan drafting, judge synthesis, divide, chat — goes through
  `agent.Reason`, which prefers the adapter's tool-less mode. For Claude that is
  `claude -p --tools ""`; for Copilot, omitting `--allow-all`; for OpenCode,
  `--pure`.
- **Work** — and only work — calls `Agent.Run`, which keeps tools enabled and
  honours `dangerously_skip_perms`. That is where files are supposed to change.

Tool-less mode also never passes `--dangerously-skip-permissions`: a reasoning
call has no business being granted write access.

## Consequences

**Good**
- The plan phase produces plans, and the divide phase parsed on the **first
  attempt with zero retries** on the same task that previously needed nine calls.
- Large token and latency savings on every reasoning call — no tool definitions
  in the prompt, no file reads, no tool round-trips.
- Reasoning calls cannot touch the filesystem, which makes the approval gates in
  ADR-0003 meaningful: nothing can be changed before the human approves.

**Bad**
- Planning is now uninformed by the repository. The agent cannot read the code to
  plan against it, so plans are shallower for tasks that need to inspect existing
  files. This is the real cost, and the likely reason to revisit this ADR —
  probably via an explicit, read-only context-gathering step rather than by
  re-enabling tools wholesale.
- The isolation is only as strong as each CLI's flag. Claude's `--tools ""` is
  explicit; Copilot and OpenCode are weaker approximations, so the guarantee is
  not uniform across agents.
- One more thing to get right when adding an adapter: implement `ChatAgent` or
  silently fall back to a tool-enabled call.

**Neutral**
- `ChatAgent` is optional, so an adapter without it still works, just without
  the benefit.

## Alternatives considered

- **Prompt harder ("do not use tools, only output JSON")** — tried implicitly;
  the divide prompt already said "return ONLY a JSON array" and it still failed.
  Capability beats instruction.
- **Use a cheap raw API for reasoning and CLIs for work** — would give the
  cleanest separation, but reintroduces API keys and a second auth path
  (ADR-0002).
- **Allow read-only tools during planning** — attractive, and the likely next
  step, but no CLI exposes a reliable read-only tool set today.

## Notes

`agent/agent.go` (`Reason`), `agent/claude.go` (`chatArgs` vs `runArgs`).
Tested in `agent/parse_test.go`: `Reason` must pick the tool-less path and fall
back when absent; chat args must disable tools and must not request write
permissions, run args must keep them.
