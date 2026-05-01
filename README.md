# Aros

A CLI orchestrator that coordinates multiple AI coding agents — Claude Code, OpenCode, and others — to collaboratively plan, divide, and execute software projects.

```
aros init "my project"
aros plan "build a REST API with user auth and JWT"
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

## Supported agents

| Agent | Binary | Best for |
|---|---|---|
| Claude Code | `claude` | Architecture, reasoning, planning, docs |
| OpenCode | `opencode` | Implementation, debugging, testing |
| OpenCode + Gemini | `opencode -m google/gemini-2.0-flash` | Research, analysis |
| GitHub Copilot | `gh copilot` | *(stub — coming soon)* |

Aros calls each agent as a subprocess — no API keys managed here. Auth is handled by each tool independently.

---

## Requirements

- [Claude Code](https://claude.ai/code) (`claude` on PATH)
- [OpenCode](https://opencode.ai) (`opencode` on PATH)
- [secondmem](https://github.com/Rinil-Parmar/secondmem) — optional, for shared agent memory
- Go 1.23+

---

## Install

```bash
git clone https://github.com/Rinil-Parmar/aros
cd aros
go install .
```

---

## Usage

### Initialize a project

```bash
cd my-project
aros init "my project name"
```

Creates `.aros/` with project state and a config file you can edit.

### Plan

```bash
aros plan "build a CLI tool that tracks daily habits"
```

Every enabled agent generates a plan. The judge agent synthesizes them. You review and approve — or give feedback for another pass.

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

Tasks execute in dependency order, up to 2 agents in parallel. Each agent gets context from its dependencies and from secondmem. If an agent hits an ambiguous decision, it outputs `<<AROS_HUMAN>>` and pauses for your input.

### Check status

```bash
aros status
```

Shows current phase and task statuses at any point.

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

[secondmem]
enabled = true
binary  = "secondmem"

[work]
max_concurrent        = 2
agent_timeout_seconds = 300
```

### Change a model

```bash
aros config set agents.claude.model claude-opus-4-7
aros config set agents.opencode.model openai/gpt-4o
aros config set judge.agent opencode
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

This means agents can build on prior work and knowledge across sessions.

---

## Project state

All state lives in `.aros/` inside your project directory:

```
.aros/
├── config.toml     # project-level config
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
| Assignment approval | After judge assigns tasks to agents |
| Agent decision | When an agent outputs `<<AROS_HUMAN>>` during work |

---

## Built with

- [Cobra](https://github.com/spf13/cobra) — CLI framework
- [Viper](https://github.com/spf13/viper) — config management
- [errgroup](https://pkg.go.dev/golang.org/x/sync/errgroup) — parallel agent execution
- [secondmem](https://github.com/Rinil-Parmar/secondmem) — local AI knowledge base

---

## Roadmap

- [ ] Gemini adapter (standalone, not via OpenCode)
- [ ] GitHub Copilot headless support
- [ ] `aros retry <task-id>` — re-run a single failed task
- [ ] Per-run log of agent prompts and outputs (`.aros/runs/`)
- [ ] `aros export` — export full session as markdown report
