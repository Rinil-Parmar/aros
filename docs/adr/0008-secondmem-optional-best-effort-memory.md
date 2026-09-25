# ADR-0008: secondmem is optional, best-effort shared memory

- **Status:** Accepted
- **Date:** 2026-05-01 (timeouts and availability check added 2026-09-17)
- **Deciders:** Rinil Parmar

## Context

Agents in Aros are separate processes with no shared context. Anything one
learns is lost to the others. A companion project, `secondmem`, is a local
AI knowledge base with `ask` and `ingest` commands, backed by a local model
(Ollama) — so it is useful as a cross-agent memory layer.

It is also a second local AI system in the hot path: it can be slow, not
installed, not initialised, or stalled behind a model load.

## Decision

secondmem is an **optional, non-fatal** dependency:

- `Ask` returns a context string, or `""` on any error. Phases feed that string
  into prompts when non-empty and otherwise carry on. A memory failure can never
  fail a phase.
- `Ingest` returns an error only so the caller can *log* it. Ingestion of task
  results happens on a detached goroutine after the task is already marked done,
  so it never delays scheduling.
- Both calls are bounded: 30s for `ask`, 90s for `ingest`.
- If `secondmem.enabled = true` but the binary is not on PATH, the layer
  silently behaves as disabled instead of failing every call.

## Consequences

**Good**
- Aros runs identically with or without secondmem installed; it is genuinely
  optional.
- A stalled local model degrades output quality (no memory context) instead of
  hanging a phase — which is what happened before the timeouts existed.

**Bad**
- Failures are invisible by design. If secondmem is misconfigured, the user sees
  slightly worse prompts and no clear signal why. Only ingest failures surface,
  as a warning line.
- Retrieved context is injected into prompts untrimmed, so a verbose memory
  answer inflates every prompt in the phase.
- No relevance threshold: whatever `ask` returns is used.

**Neutral**
- Memory is keyed by the question text only (task title, plan task). There is no
  per-project or per-session namespacing inside secondmem.

## Alternatives considered

- **Make it required** — rejected: it would make Aros unusable without a second
  local AI stack and a running model.
- **In-process vector store** — would remove the subprocess and the timeouts,
  but duplicates a tool the author already maintains and adds an embedding
  dependency to a program whose whole premise is orchestrating other tools
  (ADR-0002).
- **Pass previous task outputs only, no external memory** — already done inside
  a run (dependency outputs are threaded into prompts). secondmem adds
  *cross-session* memory, which that cannot.

## Notes

`memory/secondmem.go`. Disabled in all automated tests
(`memory.New("", false)`) so tests never depend on a local model.
