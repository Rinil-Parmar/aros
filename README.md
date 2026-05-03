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
┌─────────────────────────────────────────────────────────┐
│  PLAN                                                   │
│  Each agent generates a plan in parallel                │
│  → Judge synthesizes the best approach                  │
│  → You approve (or give feedback and re-run)            │
└────────────────────┬────────────────────────────────────┘
                     │
┌────────────────────▼────────────────────────────────────┐
│  DIVIDE                                                 │
│  Judge breaks the plan into concrete tasks              │
│  → Assigns each task to the best-fit agent              │
│  → You approve the assignment table                     │
└────────────────────┬────────────────────────────────────┘
                     │
┌────────────────────▼────────────────────────────────────┐
│  WORK                                                   │
│  Agents execute tasks in dependency order               │
│  → Parallel execution (configurable concurrency)        │
│  → Agents pause and ask you for critical decisions      │
│  → Results stored in secondmem for shared context       │
└─────────────────────────────────────────────────────────┘
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
  Ctrl+H help  ·  Ctrl+K clear  ·  Ctrl+S status  ·  pgup/dn scroll
```

**Layout:**
- **Left panel** — scrollable conversation history with all agent output
- **Right panel** — live agent activity with spinner, model name, and latest line; configured agents list; current project and phase
- **Approval card** — appears inline when a decision is needed; dismissed by `y` or `n`
- **Input box** — rounded border (brand purple when active, muted when busy); spinner replaces `❯` during processing
- **Shortcuts bar** — always-visible keyboard reference at the bottom

**TUI keyboard shortcuts:**

| Key | Action |
|---|---|
| `Enter` | Send message / confirm |
| `y` / `n` | Answer approval prompts |
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
| `/phase <phase>` | Set phase (init|plan|divide|work|done) |
| `help` | Show all commands |

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
model     = "claude-sonnet-4-5"
strengths = ["architecture", "reasoning", "docs", "planning"]

[agents.opencode]
enabled   = true
model     = "anthropic/claude-sonnet-4-5"
strengths = ["implementation", "debugging", "testing"]

[agents.copilot]
enabled   = true
model     = "claude-sonnet-4.5"
strengths = ["implementation", "debugging", "refactoring"]

[secondmem]
enabled = true
binary  = "secondmem"

[work]
max_concurrent        = 2
agent_timeout_seconds = 300
```

### Change a model or judge

```bash
aros config set agents.claude.model claude-opus-4-7
aros config set agents.opencode.model openai/gpt-4o
aros config set judge.agent opencode
```

Or in the TUI:

```
/model claude claude-opus-4-7
/judge opencode
```

### Add Gemini via OpenCode

```toml
[agents.opencode-gemini]
enabled   = true
model     = "google/gemini-2.0-flash"
strengths = ["research", "analysis"]
```

---

## secondmem integration

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
        └── manifest.json   # task list with assignments and statuses
```

State transitions are forward-only. Use `--force` to re-run a phase.

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

## Roadmap

- [ ] Gemini adapter (standalone, not via OpenCode)
- [ ] `aros retry <task-id>` — re-run a single failed task
- [ ] Per-run log of agent prompts and outputs (`.aros/runs/`)
- [ ] `aros export` — export full session as markdown report
- [ ] Persist `/model` and `/judge` TUI changes to config file
