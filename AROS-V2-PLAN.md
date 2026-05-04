# AROS v2 — Master Build Plan

**Scope:** TUI-only multi-agent coding orchestrator. Production-grade, token-efficient, cost-aware. Built in Go. Inspired by OpenCode, Claude Code, GitHub Copilot CLI, Gemini CLI.

**Why v2:** v1 (Aros CLI+TUI at `/home/parmar7f/project/aros`) reached working PoC — three-phase plan/divide/work flow, secondmem, judge pattern, Bubble Tea TUI — but accumulated structural bugs that are cheaper to redesign than patch:
- CLI and TUI have parallel near-duplicate implementations of plan/divide/work (worker.go vs tui/phases.go, divider.go vs tui/phases.go's parseTasks). Drift caused 3 of 5 critical bugs in last session.
- Single goroutine per agent invocation per phase — no streaming, agents must finish before any output renders. Wastes wall-clock and prevents cancellation.
- Approval flow couples TUI mode state to phase callbacks via `onYes`/`onNo` closures captured from goroutines — fragile, hard to reason about.
- Token spend is unbounded: every plan call sends full context, no caching, no per-step model tier selection.
- No verification pass on the plan or the divide output (judge synthesizes but nothing checks the output).

**v2 deletes:** the standalone `cmd/` Cobra CLI, `worker/`, `divider/`, `planner/`, `human/` packages. Logic consolidates into TUI-driven services that the TUI calls directly.

---

## Architecture overview

```
┌─────────────────────────────────────────────────────────────┐
│  TUI (Bubble Tea)                                            │
│  - Sessions, chat, approvals, activity, task board, settings │
└──────────────────┬───────────────────────────────────────────┘
                   │ method calls + tea.Cmd
┌──────────────────▼───────────────────────────────────────────┐
│  Orchestrator (state machine)                                │
│  PLAN → JUDGE → VERIFY → DIVIDE → JUDGE → VERIFY → WORK      │
│  Phase events → TUI; approval gates managed here             │
└──┬──────────────┬──────────────┬────────────┬───────────────┘
   │              │              │            │
┌──▼───┐  ┌───────▼───┐  ┌───────▼─────┐  ┌───▼──────┐
│Agent │  │Scheduler  │  │TokenBudgeter│  │Memory    │
│Pool  │  │(DAG +     │  │(per-call    │  │(secondmem│
│(stream│  │concurrency│  │tier choice) │  │+ context │
│ I/O) │  │+ retries) │  │             │  │ window)  │
└──┬───┘  └───────────┘  └─────────────┘  └──────────┘
   │
┌──▼─────────────────────────────────────────────────┐
│  Agent Adapters (subprocess)                       │
│  claude • opencode • copilot • gemini • cursor-cli │
│  All implement: Stream(ctx, prompt) <-Event        │
└────────────────────────────────────────────────────┘
```

### Layered packages (target)

```
internal/
  agent/        # interface + adapters; all stream events, never block
  orchestrator/ # phase state machine, runs purely on channels
  scheduler/    # DAG executor for work phase
  judge/        # plan synthesis, divide synthesis, verify
  budget/       # per-step model tier selection, token accounting
  memory/       # secondmem wrapper + ephemeral session memory
  session/      # multi-session manager (active + archived)
  state/        # on-disk persistence (json) — single source of truth
  tui/          # bubble tea views, components, theme
    components/ # reusable: chat, taskboard, activity, approval, settings
config/         # toml load/save; live edits persist
main.go
```

**Rule:** TUI does not import `agent/` directly — only via `orchestrator`. This eliminates the v1 dual-implementation drift.

---

## The 10 user-stated features — design pass

### 1. Multi-agent like OpenCode / Claude Code / Copilot

Same subprocess model as v1 (no API keys managed by AROS) but with five adapters at launch:

| Agent      | Binary       | Default tier        | Strengths                         |
|------------|--------------|---------------------|-----------------------------------|
| claude     | `claude`     | haiku 4.5           | reasoning, planning, judge        |
| opencode   | `opencode`   | gpt-5-nano / haiku  | implementation, debugging         |
| copilot    | `copilot`    | gpt-4.1-mini        | refactor, in-repo edits           |
| gemini     | `gemini`     | flash               | research, large-context summarize |
| cursor-cli | `cursor-agent`| auto                | repo-aware edits (when present)  |

Each adapter implements:
```go
type Agent interface {
    Name() string
    Stream(ctx context.Context, req Request) (<-chan Event, error)
    Capabilities() Capabilities  // {Tools, MaxContext, SupportsCache, etc.}
}
type Event struct {
    Kind   EventKind  // text | tool_use | tool_result | error | done | usage
    Text   string
    Usage  *Usage     // input, output, cached tokens
    Err    error
}
```

**Critical:** Streaming is the contract. v1 buffers entire stdout then parses — blocks UI for 30–120s per call. v2 reads NDJSON line-by-line off stdout pipe and emits Events as they arrive. UI updates incrementally.

**Adapter notes per CLI:**
- `claude` already streams JSON via `--output-format stream-json` — switch to that.
- `opencode run --format json` already emits NDJSON — wire line reader.
- `copilot -p ... --output-format json` emits NDJSON — wire line reader.
- `gemini --prompt "..." --output-format json` (verify flag in current version).
- `cursor-agent` if installed on PATH.

**Edge cases v1 missed (must handle in v2):**
- Subprocess writes interleaved stdout/stderr — separate goroutines per pipe.
- Subprocess never exits (hung) — context deadline kills with SIGKILL after grace period (SIGTERM first, 5s, then KILL).
- Subprocess exits with stdout truncated mid-line — reader must accumulate partial line until newline or EOF.
- Subprocess prints non-JSON banner before JSON stream — skip lines until first valid JSON.
- Output >10MB — cap per-call buffer; truncate with marker rather than OOM.
- Adapter binary not on PATH — disable agent in registry, surface in TUI Agents view, not a hard error.
- Adapter authenticated state changes mid-session (login expired) — reflect via error event, prompt re-auth.

### 2. Judge verifies plan

v1 judge only synthesizes. v2 splits into two roles:
- **Synthesizer (judge):** merges N agent plans → one plan.
- **Verifier (separate or same agent w/ different prompt):** checks plan against criteria.

Verifier rubric (machine-checkable + LLM-checkable):
1. Every requirement from user task appears in plan (LLM check).
2. Plan has no contradictions (LLM check).
3. Plan lists concrete deliverables, not vague advice (LLM check).
4. Each step has measurable completion criteria (LLM check).
5. Token cost projection — verifier estimates total work-phase cost; if > configured budget, recommends scope reduction *before* user approves.
6. Risk list ≥1 item (heuristic: if zero, ask "are there really no risks?").

Verifier runs same prompt with cheaper model (haiku/flash) to keep cost low. Output format is JSON `{verdict: "pass"|"revise", issues: [...], cost_estimate_tokens: N}`. If revise, judge gets verifier's issues and re-synthesizes once before user sees anything.

**Same pattern at divide phase:** verifier checks the task DAG (no cycles, every task has acceptance criteria, deps are real, agent assignments match agent strengths).

### 3. Three phases plan/divide/build

Same as v1 but:
- **Plan phase output** is structured (markdown sections + JSON metadata block at end). The verifier reads the JSON metadata; user reads the markdown.
- **Divide phase output** is a strict task DAG (json schema validated). Each task has: `id, title, description, acceptance_criteria[], assigned_to, model_tier, estimated_tokens, dependencies[]`.
- **Work phase** executes DAG with the scheduler.

Phase transitions are explicit and atomic. State machine in orchestrator:
```
idle → planning → plan_review → dividing → divide_review → working → done
                ↑                        ↑
            (revise loop)            (revise loop)
```
Any phase can be cancelled cleanly (context cancel propagates to running adapter subprocesses).

### 4. Divide tasks to agents efficiently

v1 lets the judge choose. v2 adds an explicit assigner step with hard constraints:
1. Judge proposes assignments based on `Strengths` field.
2. Assigner re-checks: each agent has a per-session work cap (avoid pinning all tasks to one binary if it's slow). Default cap = ceil(total_tasks / num_enabled_agents) + 1.
3. Token-tier matching: implementation tasks → mini/nano models; architectural decisions → sonnet/opus.
4. Skill match score (Jaccard between task tags and agent strengths).
5. If score < threshold, fall back to default agent (configurable, default `claude`).

User sees the assignment table in TUI with score column. Single key (`r`) lets user reassign any task before approval.

### 5. Normal chat like ChatGPT/OpenCode

A fourth mode beside the three phases: `chat`. Always available, never blocked by phase state. In TUI: typing without prefix = chat with judge agent (already in v1 — keep), typing `@<agent> message` = chat with specific agent. Chat history is per-session, persisted, fed back into next phase as optional context (user toggle: `/chat-context on|off`).

Edge cases:
- Long chat history → token bloat. Solution: rolling window (last N turns) + secondmem ingestion of older turns; on next phase, retrieve relevant chat snippets via secondmem semantic search.
- Chat during work phase: allowed; routed to chat thread, not work queue. Background tasks keep running.

### 6. Multi-session management

v1 already has sessions. v2 hardens:
- Sessions persisted as `<projectroot>/.aros/sessions/<id>/`.
- Each session: state.json, manifest.json, chat.jsonl (append-only), runs/<run-id>/ (full prompt+output logs per agent invocation, opt-in for debug).
- TUI session picker: `Ctrl+P` opens fuzzy-search of sessions across projects (global index in `~/.aros/sessions.idx`).
- Session ops: `new`, `list`, `use`, `rename`, `archive`, `rm`, `clone` (clone=fork from current state).
- Session metadata includes `last_active_at`, `total_cost_tokens`, `phase`. Sort by recency.
- Active session indicator in header bar.

Edge cases:
- Switching session mid-work: prompt confirm + cancel running tasks gracefully.
- Two TUI instances on same session: detect via lockfile (`.aros/sessions/<id>/.lock` w/ pid), refuse second instance to write, allow read-only.
- Crash during write: state files written via temp+rename for atomicity.

### 7. Professional TUI

Reference quality: OpenCode's TUI (split panel, dark purple theme), Claude Code's chat view, Cursor's command palette.

**Layout (v2):**
```
┌─ ◆ AROS  project · session · phase · cost ─────────────────────┐
│                                                                  │
│  Chat / Stream (left, 65%)        │  Activity (top right)       │
│                                   │  - per-agent spinner        │
│                                   │  - last line + tokens used  │
│                                   ├─────────────────────────────┤
│                                   │  Task board (bottom right)  │
│                                   │  - DAG view, status, agent  │
│                                   │  - selectable for details   │
├─────────────────────────────────────────────────────────────────┤
│  Approval card (when active) — y/n or [r]eassign                │
├─────────────────────────────────────────────────────────────────┤
│  Input box (multi-line, slash autocomplete)                     │
├─────────────────────────────────────────────────────────────────┤
│  Statusline: shortcuts · tokens · model · network              │
└──────────────────────────────────────────────────────────────────┘
```

**Components (each its own file under tui/components/):**
- `chat.go` — viewport, message kinds (system, user, agent, tool-call, tool-result, error, success), markdown rendering via glamour, code-fence syntax highlighting via chroma.
- `activity.go` — agent strip, spinner, last line, in-flight tokens (input cached/output).
- `taskboard.go` — DAG visualization (ASCII or unicode box), task selection, expand task to show acceptance criteria + last output.
- `approval.go` — modal overlay, action keys, supports "yes / no / edit / reassign".
- `palette.go` — `Ctrl+K`-style command palette (fuzzy search of all slash commands + agent ops + session ops).
- `settings.go` — overlay panel (`Ctrl+,`) for live config: per-agent model, dense, judge, concurrency, budget caps.
- `statusline.go` — bottom shortcut + cost line.

**TUI requirements (non-negotiable):**
- 60fps render budget — never block Update; all work async via tea.Cmd.
- Mouse: wheel scrolls chat, click selects task, click agent badge focuses.
- Resize without flicker — recompute layout once per WindowSizeMsg, cache.
- Color: dark purple primary; respect `NO_COLOR`, fall back to ASCII when not a TTY.
- Markdown rendering w/ glamour for agent responses; code blocks auto-highlight.
- Syntax-aware diff rendering when an agent emits a patch (parse unified diff, highlight +/-).
- Copy-to-clipboard for any chat block (`y` while focused).
- Search in chat (`/`).
- Help discoverable via `?` and `Ctrl+H` — same content, two entry points.
- Themes: dark, light, high-contrast (config + `/theme`).

### 8. Approvals without freezing

v1 bug: approval mode locked input until callback resolved. v2:
- Approval is its own modal overlay rendered on top, but input box stays focused.
- Two answer paths: **modal hotkey** (`y`, `n`, `r`, `e`) handled by overlay, OR **text** (typing "approve", "reject", "reassign task-3 claude") handled by parser.
- Approval has *timeout default off, configurable*: e.g. auto-approve plan after 10 min if user is AFK and `auto_approve.plan = true` is set (off by default; user opts in for autonomous runs).
- Cancellable: `Ctrl+G` aborts current phase from any approval.
- Non-blocking: chat with judge ("explain step 3") works while approval is up.

### 9. Token-efficient model selection

This is the make-or-break design lever. Token budget is a first-class object:

```go
type Budget struct {
    SessionCap       int  // hard ceiling, abort if exceeded
    PhaseCap         map[Phase]int
    PerCallCap       int  // any single call
    Spent            int
    EstimateRemaining int
}
```

**Tiering policy (default — overridable in config):**

| Step                       | Tier      | Rationale                          |
|----------------------------|-----------|------------------------------------|
| Chat (small Q&A)           | nano/haiku| Fast, cheap                        |
| Plan generation (per agent)| mini/sonnet| Quality matters here              |
| Plan synthesis (judge)     | sonnet    | Reasoning over multiple plans     |
| Plan verify                | haiku     | Pattern check, cheap              |
| Divide                     | sonnet    | Structured output reliability     |
| Divide verify              | haiku     | Schema check                      |
| Work — implementation      | mini      | Tight feedback loop, retryable    |
| Work — architecture/refactor| sonnet   | Decisions are expensive to undo   |
| Work — tests/docs          | nano/haiku| Boilerplate-heavy                 |

Budgeter:
- Decides tier per step using task tags (`implementation`, `architecture`, `test`, `doc`, `research`).
- Tracks running cost using usage events from adapters (`{input, output, cached}`).
- Soft cap: when 80% of phase budget consumed, switch remaining steps to a tier down.
- Hard cap: refuse next call, surface modal "Continue at full cost? Switch all to nano? Stop?".

**Prompt-side savings (mandatory):**
- Prompt cache: claude supports cache_control breakpoints — long static prefixes (system prompt + project context) marked cacheable. Adapter exposes; orchestrator opts in.
- Dense mode preamble (already in v1) only on long-output steps; chat replies skip dense to keep voice natural.
- Strip secondmem context to top-K relevant chunks (currently ingests verbatim — wasteful).
- Diff-only prompts in work phase: send the dependency outputs as references not full inlined text when an agent has filesystem access (it can re-read).
- No re-sending full chat history on every chat call — session ID + last-N rolling window.

**Telemetry (visible in TUI statusline):** `tokens 12.3k in / 4.1k out (1.8k cached) · $0.04 est`.

### 10. Production-grade edge case handling

Hard problems v1 dodged. v2 must own all of these:

**Agent failure modes:**
- Subprocess panic / crash → emit error event, mark task `blocked`, do not crash orchestrator.
- Subprocess hung (no stdout for >N seconds) → liveness watchdog; SIGTERM + grace + SIGKILL.
- Subprocess emits malformed JSON → fallback to raw-text path; flag in run log.
- Subprocess writes to non-stdout sink (e.g. tmp file) → adapter-specific; document & test.
- Subprocess output-rate too high (flooding TUI) → coalesce events: emit at most 30 fps per agent.
- Agent rate-limited by upstream → detect 429 in stderr/JSON, exponential backoff with jitter, max 3 retries.
- Agent auth lapsed mid-stream → emit `auth_required` event, pause task, surface re-auth modal.

**Concurrency / scheduling:**
- DAG with diamond dependencies — already handled (deps_complete check); test with diamond cases.
- `<<AROS_HUMAN>>` from two agents simultaneously — already serialized via humanMu in v1; carry forward, but add queue UX (badge "2 agents waiting").
- Orphaned task (dep blocked) → mark dependents `skipped` rather than indefinite pending; user can retry from any point.
- Mid-phase cancellation → all goroutines must respect ctx; current state.SaveManifest snapshot captures partial progress so resume works.
- Resource pressure: cap concurrent subprocesses at min(max_concurrent, NCPU). Each subprocess can be memory-heavy.

**State / persistence:**
- Atomic writes (temp + rename) for all `.aros/` json files — partial corruption is silent and lethal.
- Schema migrations: every json file has `schema_version`; loader migrates forward.
- Resume: on TUI launch, read state, if phase=work and tasks `in_progress`, mark them `pending` (they were interrupted). Show resume prompt.
- `.aros/.lock` per project — prevents two TUIs from trampling.
- Run logs (full prompt + full response) toggleable, default off (privacy + disk). When on, written under `.aros/sessions/<id>/runs/` for postmortem.

**TUI / input:**
- IME / wide-rune input — Bubble Tea handles via runewidth; verify with emoji + CJK.
- Paste handling — `tea.PasteMsg` on bracketed-paste-capable terminals; otherwise multi-key burst handled by buffer.
- Terminal resize to 1×1 — clamp layout, show "terminal too small" placeholder.
- Color terminal lacking 256-color → fall back to 16-color palette via lipgloss profile detection.
- Long single line (e.g. 8000-char paste) → wrap by grapheme, not by byte.

**Network / external:**
- secondmem unavailable → silent fallback (already in v1), warn once in statusline.
- secondmem returns error mid-ingest → log, don't fail the task.
- DNS / disk full / out-of-fd errors from subprocess spawn → bubble to user with actionable message.

**Security:**
- `--dangerously-skip-permissions` on claude is per-agent, off by default. TUI Settings shows shield icon when on; warn modal first time enabled per session.
- Never log API keys (we don't store any, but env var passthrough must not be echoed in run logs).
- Tasks cannot escape project directory: working dir of every subprocess is the project root; any `cd ..` is the user's problem inside the agent.
- File-write tools used by agents are agent-internal; AROS doesn't need to sandbox them, but document the risk in README.

---

## Build phases

Sequential. Each phase ships a working binary. Don't move on until current one passes its acceptance criteria.

### Phase 0 — Foundations (week 1)

Goal: clean repo skeleton, reusable libs, no orchestration logic yet.

Tasks:
1. New repo branch `v2` (or new dir `aros-v2/` to keep v1 buildable for reference).
2. Set up `internal/` packages with empty interfaces and tests.
3. `state/` package: schema-versioned JSON load/save with atomic temp+rename.
4. `config/` package: TOML load + live save (replaces v1 viper-only).
5. `agent/` interface + skeletal `claude` adapter that streams events.
6. Logging: structured (slog), file-only (no stdout pollution), level configurable.
7. Goroutine leak detector test (uber-go/goleak) for adapter shutdown.
8. CI: `go vet`, `golangci-lint`, race-detector tests.

Acceptance: `go test ./... -race` green; running `aros` shows empty TUI shell with header + input + statusline.

### Phase 1 — Streaming agent layer (week 2)

Tasks:
1. Implement all 5 adapters with `Stream` returning `<-chan Event`.
2. Stdin/stdout/stderr pipe handling, graceful shutdown (TERM → KILL).
3. Usage parsing per adapter (where available).
4. Adapter unit tests with fake binaries (testdata scripts that emit canned NDJSON).
5. Integration smoke: `aros chat` mode talks to one real agent and streams.

Acceptance: typing in TUI streams agent reply token-by-token; cancel with Ctrl+G stops subprocess within 5s; usage shown in statusline.

### Phase 2 — Orchestrator + plan phase (week 3)

Tasks:
1. State machine (idle/planning/plan_review/...).
2. Plan flow: parallel agent calls, judge synthesis, verifier check.
3. Approval modal component (non-blocking).
4. Revise loop with feedback input.
5. Plan persisted on approve.
6. Per-call token accounting wired to statusline.

Acceptance: `aros` → enter project → "plan build a CLI todo app" → see streams → see synthesized plan → verify pass/fail → approve → state saved; cancel mid-plan releases all subprocesses; resume after restart shows plan.

### Phase 3 — Divide phase + verifier + assigner (week 4)

Tasks:
1. JSON-schema validation for divide output.
2. Assigner with score-based assignment, user-override (`r` to reassign).
3. Verifier checks DAG (cycles, dangling deps, acceptance criteria present).
4. Task board component (DAG render, selection, detail expand).

Acceptance: divide produces valid manifest; reassignment in TUI works; cycles caught + repaired without manual edit.

### Phase 4 — Work phase (week 5)

Tasks:
1. Scheduler: topological execution with concurrency cap.
2. `<<AROS_HUMAN>>` queue + serialized prompts.
3. Per-task retry (on transient errors) with backoff.
4. Mid-task cancellation (Ctrl+G on focused task in board).
5. Task → done updates board + ingests result to memory.
6. Resume from interrupted work (in_progress→pending).

Acceptance: 10-task DAG with 3 layers runs concurrently; killing TUI mid-run + restart resumes from where it left; one task asks human → others keep going → human queue clears in order.

### Phase 5 — Token budget + tiering (week 6)

Tasks:
1. `budget/` package; per-step tier defaults.
2. Soft / hard caps with modal escalation.
3. Cost estimate from verifier added to plan-approval card ("est. cost: 14k tokens / $0.06").
4. Dense / cache / context-trimming integration.
5. Cost telemetry in statusline + per-session totals in /status.

Acceptance: a 10-task project completes under target token budget; tier-down kicks in correctly when soft cap hit; cost displayed and accurate ±10%.

### Phase 6 — Multi-session + polish (week 7)

Tasks:
1. Session manager full feature set (new/list/use/rename/archive/rm/clone).
2. Global session index (`~/.aros/sessions.idx`).
3. Lockfile for concurrent-instance protection.
4. Command palette (`Ctrl+K`).
5. Settings overlay (`Ctrl+,`) with live writes back to TOML.
6. Themes: dark, light, high-contrast.
7. Markdown / diff rendering polish.

Acceptance: 5+ sessions across 2+ projects, switch via palette in <2 keystrokes; settings change applies live without restart; theme switch works.

### Phase 7 — Hardening + release (week 8)

Tasks:
1. Goroutine leak audit (goleak in every test).
2. Fuzz tests for json parsers (state, manifest, plan, divide output).
3. Race detector clean across full test suite.
4. End-to-end test scripts (use mock adapters that emit canned streams).
5. Crash recovery test matrix (kill -9 at each phase boundary).
6. README with screenshots + asciinema demo.
7. Tagged v2.0.0 release; binary published.

Acceptance: full e2e test green; documented edge case matrix; first install works from `go install`.

---

## Token efficiency — concrete tactics

Listed by impact:

1. **Prompt caching** for the static prefix in every plan/divide/work prompt. Project name + agent strengths + system instructions ≈ 1.5k tokens cached → recovered on every subsequent call (cost ~10× lower for cached portion). Claude API supports `cache_control: ephemeral`. Verify CLIs expose this — if not, file issue / fork.

2. **Top-K secondmem retrieval** instead of dump. v1 ingests blobs and re-ingests on every step; embed them and retrieve top-3 per task. Cuts memory-context tokens by ~70%.

3. **Tiered models** as outlined in §9. Default `haiku` for plan/verify/chat; `sonnet` only for synthesis. Real savings: 10–20× input cost on the cheap steps.

4. **Reuse plan output as work-phase context**, not full chat history. Work prompt = task description + acceptance criteria + dep outputs (truncated to 500 tok per dep) + top-3 memory chunks. Bound at ~3k tokens per call.

5. **Stream-stop on `<<AROS_HUMAN>>`** — agents can keep going past the sentinel and waste tokens. Adapter cancels stdin/SIGTERM as soon as sentinel detected in stream.

6. **Coalesce dense-mode usage** to long-form steps. Dense saves ~30% on output tokens but degrades chat fluency; off for chat, on for work.

7. **Skip redundant verifier on trivial plans** — heuristic: if plan ≤ 5 steps and ≤ 1k tokens, skip verifier (save one full call).

8. **Agent parallel cap of 3 in plan phase**. v1 fans out to all enabled agents — if 5 enabled, that's 5× cost for marginal synthesis quality. Default cap=3, configurable.

9. **Per-task token budget** in manifest. If task projection > cap, judge re-divides (split into sub-tasks).

10. **Telemetry** — log every call's token usage; weekly summary in `/cost` command shows where the spend goes; informs future tier defaults.

---

## Edge case master list (live checklist for build phases)

Group by area. Mark fixed during phase; review at hardening.

### Subprocess / IO
- [ ] subprocess hang: watchdog timer + sigterm/sigkill chain
- [ ] partial JSON line at EOF
- [ ] non-JSON banner before stream
- [ ] interleaved stdout/stderr
- [ ] huge output: per-call buffer cap with truncation marker
- [ ] subprocess writes to controlling tty bypassing pipe (rare; use setsid)
- [ ] zombie processes on cancel (use process groups, kill the group)
- [ ] race between context cancel + process exit (drain pipes after kill)

### State / persistence
- [ ] atomic temp+rename on every write
- [ ] schema_version on every json
- [ ] in_progress→pending on resume
- [ ] lockfile per project
- [ ] migration path for v1→v2 state (read v1, transform, write v2)
- [ ] disk full: surface error, don't crash
- [ ] permission denied on .aros/: prompt fix

### Concurrency
- [ ] DAG diamond
- [ ] human-question queue ordering
- [ ] orphaned-dep skip-rather-than-hang
- [ ] mid-phase cancel propagates to all goroutines
- [ ] semaphore drained on cancel (don't deadlock)
- [ ] goroutine leak (verify with goleak)

### TUI
- [ ] resize 1×1 placeholder
- [ ] no-color terminal fallback
- [ ] wide runes (CJK, emoji)
- [ ] bracketed paste vs key-burst
- [ ] focus loss (terminal blur)
- [ ] mouse capture conflict (some terminals consume scroll)
- [ ] long single line wraps by grapheme
- [ ] approval modal + chat coexisting
- [ ] command palette over modal layering

### Tokens / budget
- [ ] cap exceeded mid-call (can't pre-empt; cancel next call)
- [ ] usage event missing (some adapters don't emit) — estimate from char count
- [ ] tier mismatch: user pinned model that doesn't match tier policy — respect user
- [ ] cache miss when expected (log, don't fail)

### Sessions
- [ ] two TUI instances same session
- [ ] session id collision (uuid → safe)
- [ ] orphaned active-session pointer (target session deleted)
- [ ] session in different project root than cwd (path mismatch on resume)

### Agents
- [ ] binary not on PATH (warn, not fatal)
- [ ] auth lapsed (re-auth modal)
- [ ] rate-limit (backoff)
- [ ] adapter version mismatch (e.g. opencode bumped JSON schema) — version-detect at adapter init, fail clearly

### Recovery
- [ ] kill -9 between phases: state consistent
- [ ] kill -9 mid-call: in_progress tasks resumable
- [ ] secondmem corrupted: detect on read, rebuild from session logs

---

## Open questions to resolve before Phase 0

1. **Repo strategy:** new branch on `aros` repo, or fresh repo `aros-v2`? Decision: **branch `v2`** on existing repo; merge to main when v2 ships, archive v1 in tag `v1-final`.
2. **CLI fallback:** truly TUI-only, or keep `aros plan ...` as a thin command? Decision per user: **TUI-only**. Drop cmd/ entirely. Scripted/headless use will come later via a separate `aros-headless` if needed.
3. **secondmem ownership:** is secondmem a hard dep or optional? Decision: **optional** (runs without it; semantic retrieval degrades to chat history only).
4. **Which CLIs are required at install:** at least one agent. Adapters fail-soft if absent.
5. **Telemetry / cost source-of-truth:** depends on whether each CLI emits usage. Audit per adapter in Phase 1; fall back to char-count estimate where unavailable.
6. **License + repo visibility:** keep MIT + public (current).

---

## Reference TUIs to study (sources for ideas, not code)

- OpenCode TUI (`/home/parmar7f/project/OpenCodeTUI`) — split panel + dark purple. Already prototyped basics; v2 inherits the look-and-feel.
- Claude Code (CLI tool) — chat fluency, slash command UX, settings overlay.
- Cursor CLI — repo-aware editing patterns.
- Gemini CLI — large-context summarization patterns.

Each gives us one pattern; v2 is not a clone — it's the orchestrator above all of them.

---

## Definition of done (v2.0.0)

- All 10 user-stated features shipped and demoable.
- Token cost on a benchmark project (10 tasks) ≤ 50% of v1's measured spend.
- 0 known goroutine leaks.
- 0 panics across stress tests (10 concurrent sessions, kill-9 chaos).
- README + 60-second asciinema demo.
- `go install` works on Linux + macOS.
- v1 users can migrate state with `aros migrate-v1` (one-shot).

---

## Notes

- Build feature-by-feature commits (per existing commit-style preference): one PR per phase, multiple commits within a phase grouped by package.
- Keep v1 buildable on `main` until v2 reaches Phase 4 acceptance — gives a fallback during development.
- Personal scope: this is a solo project; estimate above (8 weeks) assumes ~2 focused hours/day. Adjust as life/coursework demands.
