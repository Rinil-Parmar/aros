# Aros

A multi-agent CLI orchestrator that coordinates Claude Code, OpenCode, and GitHub Copilot to collaboratively plan, divide, and execute software projects — with a production-grade terminal UI.

```
aros                          # launch interactive TUI
aros init "my project"        # or use the CLI directly
aros plan "build a REST API"
aros divide
aros work
```

---

## How it works

Aros runs your project through three phases, each with a human approval checkpoint.

```
 ◆ AROS  paramrut-api                                      3/4 tasks  1 blocked  work
╭──────────────────────────────────────────────────╮ ╭──────────────────────────────╮
│  paramrut-api · phase work                       │ │── Activity ──────────────────│
│  3/4 tasks  1 blocked                            │ │ ⣾ opencode                   │
│                                                  │ │   [task-002] pagination      │
│  next:  work      (resume — retries blocked)     │ │                              │
│  /help for all commands                          │ │── Agents ────────────────────│
│▸ you                                             │ │ ★ claude   haiku             │
│    plan add a search endpoint with pagination     │ │   opencode openai/gpt-5.4-mi │
│                                                  │ │                              │
│● judge                                           │ │── Tasks ─────────────────────│
│    Introduce a SearchRequest type with Limit     │ │ ✓ Define request types claude│
│    and Offset, validate in the handler.          │ │ ► Pagination helper  opencode│
│                                                  │ │ ○ Wire the handler    copilot│
│  ✓  Plan approved.                               │ │ ✗ Update OpenAPI       claude│
╰──────────────────────────────────────────────────╯ ╰──────────────────────────────╯
╭─ Commit 7 changed file(s)? ─────────────────────────────────────────────────────────╮
│  [y] yes    [n] no    esc cancel                                                    │
╰─────────────────────────────────────────────────────────────────────────────────────╯
╭─────────────────────────────────────────────────────────────────────────────────────╮
│ ❯                                                                                   │
╰─────────────────────────────────────────────────────────────────────────────────────╯
 esc cancel · ^y copy · ^h help · ^k clear · ^o select                   paramrut-api
```

---

## Terminal UI

Launch `aros` with no arguments to open the interactive TUI.

```
 ◆ AROS   my-project                              [PLAN]
┌──────────────────────────────────────┬──────────────────────────┐
│  [aros]   Planning started...        │ ─── Activity ────────    │
│  [claude] Your task requires three   │  ⠋ claude  (sonnet-4-5)  │
│           layers of service...       │    thinking...           │
│  [judge]  Synthesized plan:          │  ✓ opencode (sonnet-4-5) │
│           ...                        │    plan ready            │
│                                      │ ─── Agents ──────────    │
│                                      │  ● claude    sonnet-4-5  │
│                                      │  ● opencode  sonnet-4-5  │
│                                      │  ● copilot   sonnet-4.5  │
│                                      │ ─── Project ─────────    │
│                                      │  Phase  [PLAN]           │
│                                      │  Task   build REST API   │
╰──────────────────────────────────────┴──────────────────────────╯
╭── ? Approve this plan? ──────────────────────────────────────────╮
│   [y] Yes      [n] No                                            │
╰──────────────────────────────────────────────────────────────────╯
╭──────────────────────────────────────────────────────────────────╮
│ ❯                                                                │
╰──────────────────────────────────────────────────────────────────╯
  Esc cancel  ·  Ctrl+H help  ·  Ctrl+K clear  ·  Ctrl+S status  ·  pgup/dn scroll
```

**Layout:**
- **Left panel** — scrollable conversation history with all agent output
- **Right panel** — live agent activity with spinner, model name, and latest line; configured agents list; current project and phase
- **Approval card** — appears inline when a decision is needed; dismissed by `y` or `n`, or cancelled with `Esc`
- **Input box** — rounded border (brand purple when active, muted when busy); spinner replaces `❯` during processing
- **Status bar** — keyboard reference, plus the active project on the right

The frame always occupies exactly the terminal's width and height: one column
too wide makes a terminal wrap every row, one row too tall makes it scroll, and
either turns the layout into noise. `TestViewFitsTerminalExactly` pins that at
seven terminal sizes, with and without the approval card.

**Reading agent output.** Agent replies are markdown, so Aros renders the part
that matters to a programmer rather than flat prose: fenced code blocks get
their own framed, tinted surface, `inline code` and **bold** are highlighted,
headings and bullets are kept. Code is clipped rather than wrapped — a wrapped
line of code is harder to read than a truncated one, and `Ctrl+Y` / `/export`
give you the full text. Each turn shows a left bar in the agent's colour, the
model it used, and the time it arrived:

```
▌ judge                                                                  15:34
    Plan
    Introduce a SearchRequest type carrying Limit and Offset, validate both
    in the handler, and return a SearchResponse with Total.

    • Add the types to internal/api/search.go
    • Reject a limit above 100 with 400 Bad Request

    ── go ─────────────────────────────────────────────────────────────────
    type SearchRequest struct {
        Query  string `json:"query"`
    }
    ───────────────────────────────────────────────────────────────────────

▌ opencode openai/gpt-5.4-mini                                           15:34
    Reading internal/store/search.go to find the existing query path.
```

Replayed sessions keep each message's original time, so a resumed transcript
shows real history rather than the moment it was reloaded.

**Copying text out of the chat:** a TUI with mouse reporting enabled receives
your click-drag instead of letting the terminal select text. Press `Ctrl+O` to
hand mouse control back to the terminal and select normally (wheel scrolling is
off while in that mode), or use `Ctrl+Y` / `/copy` to put the whole chat on the
clipboard, or `/export notes.md` to write it to a file.

**TUI keyboard shortcuts:**

| Key | Action |
|---|---|
| `Enter` | Send message / confirm |
| `y` / `n` | Answer approval prompts (no Enter needed) |
| `Esc` / `Ctrl+G` | Cancel whatever is running and return to idle |
| `Ctrl+Y` | Copy the whole chat to the clipboard (OSC 52 — works over SSH) |
| `Ctrl+O` | Toggle mouse reporting. Turn it **off** to select and copy text with the mouse |
| `Ctrl+H` | Show help |
| `Ctrl+K` | Clear chat history |
| `Ctrl+S` | Show project status |
| `PgUp / PgDn` | Scroll history |
| Mouse wheel / trackpad | Scroll history |
| `Ctrl+C` | Quit |

**TUI commands (type in the input box):**

| Command | Description |
|---|---|
| `<project name>` | Initialize a new project |
| `plan <task>` | Run the plan phase |
| `divide` | Break plan into tasks |
| `work` | Execute all tasks |
| `status` | Show current phase and task table |
| `/model <agent> <model>` | Change an agent's model |
| `/judge <agent>` | Change the judge agent |
| `/agents` | List all configured agents |
| `/session <new|list|use|rm>` | Manage sessions |
| `/phase <phase>` | Set phase (init\|plan\|divide\|work\|done) |
| `/commit [--force]` | Commit the agents' changes (asks first; `--force` skips the prompt) |
| `/copy [last]` | Copy the chat (or just the last agent reply) to the clipboard |
| `/export [file]` | Write the chat to a markdown file |
| `help` | Show all commands |
| _Any other text_ | Chat with the judge agent |

---

## How agents are invoked

Aros calls each agent CLI in one of two modes:

| Mode | Used by | Why |
|---|---|---|
| **Reasoning** (tools disabled, e.g. `claude --tools ""`) | plan, divide, judge, chat | These CLIs are coding agents. With tools enabled they try to *perform* the task instead of answering, which produced agentic chatter where a plan or a JSON task array was expected — and burned tokens doing it. |
| **Work** (tools enabled) | work phase only | This is where the agent actually edits files and runs commands. |

Set `dangerously_skip_perms = true` under `[agents.claude]` so the work phase can
write files without an interactive permission prompt.

---

## Supported agents

| Agent | Binary | Best for |
|---|---|---|
| Claude Code | `claude` | Architecture, reasoning, planning, docs |
| OpenCode | `opencode` | Implementation, debugging, testing |
| GitHub Copilot | `copilot` | Implementation, debugging, refactoring |
| OpenCode + Gemini | custom config | Research, analysis |

Aros calls each agent as a subprocess — no API keys managed here. Auth is handled by each tool's own login flow.

---

## Requirements

- Go 1.23+
- At least one of:
  - [Claude Code](https://claude.ai/code) (`claude` on PATH)
  - [OpenCode](https://opencode.ai) (`opencode` on PATH)
  - [GitHub Copilot CLI](https://docs.github.com/en/copilot/github-copilot-in-the-cli) (`copilot` on PATH, requires Copilot Pro)
- [secondmem](https://github.com/Rinil-Parmar/secondmem) — optional, for shared agent memory across sessions

---

## Install

```bash
git clone https://github.com/Rinil-Parmar/aros
cd aros
go install .
```

---

## CLI Usage

### Initialize a project

```bash
cd my-project
aros init "my project name"
```

Creates `.aros/` with project state and a config file.

### Plan

```bash
aros plan "build a CLI tool that tracks daily habits"
```

Every enabled agent generates a plan in parallel. The judge agent synthesizes them. You review and approve — or give feedback for another pass.

### Divide

```bash
aros divide
```

The judge breaks the approved plan into a task list and assigns each task to the most capable agent. You see a table and approve.

```
ID           Title                          Assigned To     Dependencies
--------------------------------------------------------------------------------
task-001     Define data models             claude          (none)
task-002     Implement storage layer        opencode        task-001
task-003     Build CLI commands             opencode        task-002
task-004     Write documentation            claude          task-003
```

### Work

```bash
aros work
```

Tasks execute in dependency order, up to `max_concurrent` agents in parallel. Each agent gets context from its dependencies and from secondmem. If an agent hits an ambiguous decision, it outputs `<<AROS_HUMAN>>` and pauses for your input.

Aros records what the agents changed as a **git commit**, behind an approval
gate — it shows you the changed files and commits only if you say yes. **Aros
never pushes**: a local commit is private and undone with `git reset`, while a
push is outward-facing and cannot be cleanly retracted, so publishing stays
something you type yourself.

```toml
[git]
enabled = true     # false = never touch git
commit  = "ask"    # "ask" (approval gate), "auto" (commit silently), "off"
```

The commit is authored by whoever your `git config` says, carries no trailers,
and lists the completed tasks with the agent that did each one. Aros's own
`.aros/` state is added to `.git/info/exclude`, so session files never enter
your project's history and never appear in `git status`. Declined the gate?
`aros commit` (or `/commit` in the TUI) records it later. Not a git repo? The
git layer is skipped with a one-line note.

> **⚠ Concurrent writes are not protected.** Parallel agents share one working
> tree with no locking or isolation. If two tasks edit the same file, one
> agent's work is silently overwritten and **both tasks still report success**.
> Commit to git before running `work`, or set `work.max_concurrent = 1`.
> Details: [`docs/parallel-execution.md`](docs/parallel-execution.md),
> [ADR-0016](docs/adr/0016-no-file-level-conflict-protection.md).

If an agent fails, its task is marked **blocked** (and so are tasks that depend on it) while the rest of the run continues. The phase stays at `work`; `aros status` shows the block reason, and running `aros work` again retries every blocked task. Agent subprocesses that exceed `agent_timeout_seconds` are killed together with their child processes.

### Check status

```bash
aros status
```

Shows current phase and task statuses at any point.

### Sessions

```bash
aros session new "feature branch"
aros session list
aros session use <id>
aros session rm <id>
```

Sessions let you keep multiple workflows under the same project directory.

### Change phase

```bash
aros phase init
aros phase plan
aros phase divide
aros phase work
aros phase done
```

Use this to move backward or forward explicitly.

---

## Configuration

Global config: `~/.aros/config.toml`
Project config: `.aros/config.toml` (overrides global)

```toml
[judge]
agent = "claude"

[agents.claude]
enabled   = true
model     = "haiku"                  # any model name `claude --model` accepts
strengths = ["architecture", "reasoning", "docs", "planning"]
dense     = "full"                   # "", "lite", "full" or "ultra" — terser agent output

[agents.opencode]
enabled   = true
model     = "openai/gpt-5.4-mini"    # provider/model — see `opencode models`
strengths = ["implementation", "debugging", "testing"]
# api_key = "sk-..."                 # optional; exported as OPENAI_API_KEY / ANTHROPIC_API_KEY etc.

[agents.copilot]
enabled   = true
model     = "auto"                   # let the Copilot CLI pick; explicit names must be enabled on your plan
strengths = ["implementation", "debugging", "refactoring"]

[secondmem]
enabled = true
binary  = "secondmem"

[work]
max_concurrent        = 2
agent_timeout_seconds = 300
```

### Which models can I use?

Ask the agents instead of guessing — a hardcoded list goes stale and then
suggests models that no longer exist (both of Aros's old defaults did exactly
that):

```bash
aros models                      # every available agent
aros models opencode             # all 75, live from `opencode models`, cached a day
aros models opencode gpt-5.4     # just the matching ones
aros models opencode --refresh   # ignore the cache
```

```
opencode — 75 models, from opencode just now  (5 match "gpt-5.4")
     openai/gpt-5.4
     openai/gpt-5.4-fast
  →  openai/gpt-5.4-mini          ← in use
```

Each agent reports by the best means it has, and a list Aros is unsure about
never claims to be complete:

| Agent | Source | Validation |
|---|---|---|
| **opencode** | `opencode models` — authoritative, cached for a day (3.7s → 0.02s) | membership in the list |
| **claude** | no list command, so documented aliases, labelled *"not an exhaustive list"* | **free** — rejects an unknown model with no API call and no cost |
| **copilot** | no list command, model set depends on your subscription | none; `auto` always works |

In the TUI, `/model` shows what each agent is using, `/model opencode` lists,
and `/model opencode gpt-5.4` either sets it (exact name) or filters. Setting a
model is checked first, so a typo surfaces now rather than mid-phase:

```
$ aros config set agents.opencode.model gpt-5.4-mini
Error: opencode does not list model "gpt-5.4-mini"
did you mean: openai/gpt-5.4-mini, openai/gpt-5.4-mini-fast, openai/gpt-5.4-mini-flex
```

Pass `--no-verify` to set it anyway.

### Change a model or judge

```bash
aros config set agents.claude.model sonnet
aros config set agents.opencode.model anthropic/claude-sonnet-4-5
aros config set judge.agent opencode
```

Or in the TUI (applies to the current session only):

```
/model claude sonnet
/judge opencode
```

OpenCode needs provider credentials (`opencode auth login`) and the Copilot CLI needs `copilot login`; Aros surfaces their errors verbatim when a call fails.

### Add Gemini via OpenCode

```toml
[agents.opencode-gemini]
enabled   = true
model     = "google/gemini-2.5-flash"
strengths = ["research", "analysis"]
```

---

## secondmem integration

> Full technical detail — call sites, timeouts, embeddings, retrieval tiers and
> known limits — is in [`docs/secondmem-integration.md`](docs/secondmem-integration.md).

Aros uses [secondmem](https://github.com/Rinil-Parmar/secondmem) as a shared knowledge layer across agents:

- Before planning, each agent queries secondmem for relevant context
- After plan approval, the plan is ingested
- After each task completes, the output is ingested
- Agents in the work phase get secondmem context injected into their prompt

This means agents build on prior work and knowledge across sessions.

---

## Project state

All state lives in `.aros/` inside your project directory:

```
.aros/
├── config.toml             # project-level config
├── active-session.json     # pointer to active session
└── sessions/
    └── <session-id>/
        ├── session.json    # session metadata
        ├── state.json      # current phase, task, approved plan
        ├── manifest.json   # task list with assignments and statuses
        ├── chat.jsonl      # the TUI conversation, append-only
        └── agent-sessions.jsonl  # vendor session ids this session created
```

State transitions are forward-only. Use `--force` to re-run a phase, or `aros phase <phase>` to jump anywhere. In the TUI, `/phase <phase> --force` also aborts a running phase (agent subprocesses are killed).

---

## Session history

The TUI conversation is saved to `chat.jsonl` inside the active session, so
reopening a session shows what you discussed with the judge and where the work
stopped:

```
── resumed · 42 earlier message(s) above ──
paramrut-api · phase plan
next:  divide    (break the approved plan into tasks)
```

- **Per session.** `/session use <id>` swaps the conversation over with it; a
  new session starts clean.
- **Append-only.** A streamed reply is stored line by line and merged back into
  one block on replay, so nothing is rewritten as output arrives. A torn final
  line from a crash costs that line, not the history.
- **Bounded.** The last 500 entries are replayed, and the file is compacted to
  that tail once it passes 2 MiB.
- `/clear` (and `Ctrl+K`) clears the view **and** the saved log.
- The CLI does not write it — there is no conversation to record there.

Everything else was already durable: `state.json` keeps the phase and approved
plan, `manifest.json` keeps every task's full output.

---

## Cleaning up

Every agent call creates a **new session inside the vendor's own CLI**, stored
outside your project — `~/.claude/projects/<slug>/<id>.jsonl`,
opencode's sqlite store, `~/.copilot/session-state/<id>/`. Aros is the only
thing that knows which of those were its own, so it records each id in
`agent-sessions.jsonl` and removes them with the session:

```bash
aros session rm <id>
# 2 agent session(s) removed, 115 KB reclaimed (of 2 recorded)
```

Each agent is cleaned the safest way available:

| Agent | How | Completeness |
|---|---|---|
| **opencode** | `opencode session delete <id>` — its own command, so its index stays consistent | complete |
| **claude** | deletes that one transcript file, found by id so the path slug is never guessed | complete |
| **copilot** | removes `session-state/<id>/`; `copilot sessions` offers only `import` | partial — the sqlite row remains, and Aros says so rather than writing into another tool's database |

Pass `--keep-agent-sessions` to leave them in place. Nothing outside the
recorded ids is ever touched, and anything Aros cannot remove safely is
reported, not guessed at.

---

## Human checkpoints

Aros never proceeds past a critical decision without your approval:

| Checkpoint | When |
|---|---|
| Plan approval | After judge synthesizes all agent plans |
| Revised plan approval | After you give feedback and judge revises |
| Assignment approval | After judge assigns tasks to agents |
| Agent decision | When an agent outputs `<<AROS_HUMAN>>` during work |

---

## Built with

- [Bubble Tea](https://github.com/charmbracelet/bubbletea) — TUI framework
- [Lip Gloss](https://github.com/charmbracelet/lipgloss) — terminal styling
- [Cobra](https://github.com/spf13/cobra) — CLI framework
- [Viper](https://github.com/spf13/viper) — config management
- [errgroup](https://pkg.go.dev/golang.org/x/sync/errgroup) — parallel agent execution
- [secondmem](https://github.com/Rinil-Parmar/secondmem) — local AI knowledge base

---

## Documentation

| Document | What it covers |
|---|---|
| [`docs/adr/`](docs/adr/README.md) | Architecture Decision Records — why Aros is built the way it is. Read ADR-0009 and ADR-0014 before touching the TUI, ADR-0013 before changing how agents are invoked. |
| [`docs/parallel-execution.md`](docs/parallel-execution.md) | How the work phase schedules tasks, how agent outputs are combined, and what happens when two agents edit the same file. |
| [`docs/secondmem-integration.md`](docs/secondmem-integration.md) | The memory seam (every `ask`/`ingest` call site) and secondmem's internals: storage model, embeddings, retrieval tiers, rebalance. |

---

## Testing

```bash
go test -race ./...          # unit tests + headless TUI flow test (uses a mock claude)
scripts/e2e-mock.sh          # full CLI flow with mock agents, including blocked-task retry
```

---

## Roadmap

- [ ] Gemini adapter (standalone, not via OpenCode)
- [ ] `aros retry <task-id>` — re-run a single failed task
- [ ] Per-run log of agent prompts and outputs (`.aros/runs/`)
- [ ] `aros export` — export full session as markdown report
- [ ] Persist `/model` and `/judge` TUI changes to config file
