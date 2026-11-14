# From working tool to product

What it would take for Aros to be something other people install and trust,
and the order to do it in. Every claim about the current state was checked
against the code; file:line references are given so this document can be
re-verified rather than believed.

Written 2026-11-14, against `74ad9cd`.

## 1. What Aros is — and what that decides

Aros is a **developer CLI that orchestrates other developer CLIs**. It holds no
credentials, opens no ports, runs no daemon, and stores nothing outside the
project directory and `~/.aros`.

That single sentence settles most of the product questions:

| Question | Answer, and why |
|---|---|
| Hosted service or local tool? | **Local.** Authentication is delegated to `claude`, `opencode` and `copilot`, whose OAuth logins are interactive and machine-bound. A hosted Aros would have to hold other people's subscription credentials. |
| How is it distributed? | **A single binary.** `go install`, plus cross-compiled release archives. No installer, no runtime. |
| What is the auth system? | **There isn't one, by design.** `aros doctor --probe` verifies the CLIs the user has already logged into. This is a feature: no secrets to store, no billing intermediary, no liability. |
| Who pays for inference? | **The user, directly to the vendor.** Aros never sits in the payment path. |

The README should say this outright: *Aros authenticates nothing. It
orchestrates CLIs you have already logged into.*

## 2. What "production" means for a tool like this

Not uptime. Seven properties, in the order a user encounters them:

1. Installs in one command
2. Tells you why it is broken, before you trust it with a repo
3. **Cannot silently lose your work**
4. Cost is predictable and visible
5. Can run unattended
6. Versioned, with an upgrade path
7. Runs where the users are

Current standing: **2 is done and genuinely good. 1 is half done. 3, 4, 5, 6
and 7 are not.** The rest of this document is about those.

## 3. What is already production-grade

Worth stating plainly, because the gap list below is long and would otherwise
read as "nothing works".

- **Crash-safe resume.** Task status is written to `.aros/manifest.json` at
  every transition — dispatch (`worker.go:144`), completion (`worker.go:346`),
  block (`worker.go:282`). `ResetForRetry` (`state/tasks.go:223`) returns
  `in_progress`/`blocked` to `pending` and leaves `done` alone, so `aros work`
  resumes without redoing finished work.
- **Real health checking.** `aros doctor --probe` makes live agent calls. A
  model can be listed by a vendor and still be refused by the user's
  credentials; only a real call proves otherwise.
- **Credential-aware model discovery.** `agent/models.go` never hardcodes model
  lists. `--verify` probes each model once and caches the verdict, and
  `Usable()` filters pickers to what the account can actually serve.
- **No orphaned processes.** Agents run in their own process group
  (`agent/agent.go:95`); timeout or cancel group-kills the whole tree, with the
  kill inside `cmd.Cancel` to avoid a PID-reuse hazard.
- **Memory that cannot take the run down.** secondmem is absent-tolerant:
  missing binary silently disables it, any error yields empty context, with
  bounded timeouts (30 s ask / 90 s ingest).
- **Approval gates on anything irreversible**, and `package git` has no `Push`
  function at all.
- **A decision record.** 19 ADRs, including the limitations.

## 4. The gap list — verified

Ranked by (user harm × likelihood), not by effort.

### G1 — Parallel agents can silently lose work · **blocking for v1.0**

ADR-0016. Parallel tasks share one working tree with no locking, no worktrees
and no conflict detection. Two tasks touching one file produce a **lost update
while both report success**. `max_concurrent` defaults to 2, so it is live.

This is the only gap that can destroy user data, and it does so quietly. For a
tool whose whole purpose is writing to your repository, that is disqualifying,
regardless of how good everything else is.

*Direction:* declare `assigned_files` per task at divide time, validate them,
and have the dispatcher refuse to co-schedule tasks with overlapping claims.
Per-task commits give recoverability when the lease model is wrong. Both tie
together: a commit per task is only meaningful if tasks do not interleave.

### G2 — Nothing can run unattended · **blocking for v0.2**

`aros plan` and `aros divide` call `human.Confirm` with no bypass. Only
`aros commit` has `--yes` (`cmd/commit_cmd.go:30`). Any container, CI job or
script stalls at the first gate.

*Direction:* `--yes` plus `AROS_ASSUME_YES`, and a non-zero exit code when a
gate would have blocked. The gate stays the default; automation opts out
explicitly.

### G3 — The Windows build is broken, not merely untested

`GOOS=windows go build ./...` fails:

```
agent/agent.go:95:41: unknown field Setpgid in struct literal of type syscall.SysProcAttr
agent/agent.go:107:21: undefined: syscall.Kill
```

`tui/input.go` would fail next, on `unix.Poll`. There are **no build tags
anywhere in the repo**. Two files need splitting into `_unix.go` / `_windows.go`
pairs; the Windows halves can be honest no-ops (no group kill, no readiness
wait) at a stated cost in robustness.

Until then, any claim of cross-platform support is false.

### G4 — No version, no tags, no CI, so no releases

- No `--version` flag anywhere in `cmd/`
- `git tag` is empty, so `go install …@latest` resolves to a commit hash
- No `.github/workflows`

A user cannot say which Aros they are running, and a bug report cannot be tied
to a build. This is cheap to fix and everything else is easier once it is.

### G5 — Cost is invisible, and the data is already on the wire

`claude --output-format json` reports `total_cost_usd`, `usage` and
`duration_ms`. `claudeResponse` (`agent/claude.go:24`) maps five fields and
**discards all of them**. Aros therefore cannot tell a user what a run cost,
and users will not run an agent swarm unattended against a metered account
without that number.

*Direction:* parse what is already returned, accumulate per task and per run,
report at the end, and persist it in the manifest. Then `aros work --dry-run`
can estimate before spending. This is a struct-field change, not an
integration.

### G6 — "Done" means the agent said so

A task is marked `done` when the agent returns without error
(`worker.go:346`). Nothing checks that the work happened. The task schema has
no acceptance criteria and there is no verification step.

*Direction:* an optional `verify` command per task — `go test ./...`, a build,
a script. A task whose verify fails is `blocked`, not `done`. This is what
separates a tool you can leave running from one you must watch.

### G7 — No streaming output

Agent output arrives whole, at the end. On a multi-minute task the UI shows a
spinner and nothing else, which reads as a hang. The activity panel helps but
does not substitute for seeing work happen.

### G8 — secondmem is a second install

The memory layer is a separate binary that must be on `PATH`, with its own
config and its own Ollama dependency for embeddings. Graceful degradation means
Aros still runs without it — but silently, with memory simply absent, which a
user will not notice.

*Direction:* `doctor` already checks it; make its absence a visible warning at
startup rather than a silent downgrade, and document the install as optional
rather than implied.

### G9 — Documentation is for the author

19 ADRs and three deep technical documents, no user-facing README worth the
name: no auth matrix, no install instructions, no "what do I type first".

## 5. Roadmap

Effort figures are estimates, not commitments.

### v0.1 — Installable · ~1 day

`--version` (ldflags-stamped), tag `v0.1.0`, GitHub Actions running
`go test -race ./...` and `scripts/e2e-mock.sh`, GoReleaser for linux/darwin
arm64+amd64, and a README with the auth matrix and a first-run walkthrough.

**Exit criterion:** a stranger on a clean Linux or macOS machine runs
`go install`, then `aros doctor --probe`, and either it works or the output
tells them exactly which CLI to log into.

### v0.2 — Automatable · ~1 day

`--yes` / `AROS_ASSUME_YES` (G2), `api_key` wired for the claude and copilot
adapters so the config field stops being a silent no-op, meaningful exit
codes, `--json` on `aros status`. Dockerfile last, once there is something to
drive non-interactively.

**Exit criterion:** the full `plan → divide → work` pipeline completes
unattended in a container with only environment variables for credentials.

### v0.3 — Safe · ~1 week

G1 and G6 together: `assigned_files` leases, dispatcher refusal on overlap,
per-task commits, and an optional `verify` command per task. A new ADR
superseding ADR-0016.

**Exit criterion:** two tasks that touch the same file cannot be co-scheduled,
and a task that claims success without passing its verify command is reported
as blocked. Both covered by tests — note that the existing mock agents do not
write files, so the fixtures have to change for this to be testable at all.

### v0.4 — Trustworthy · ~3 days

Cost metering (G5), a persisted per-run log, `--dry-run` estimation, and
streamed agent output (G7).

**Exit criterion:** after a run the user can see what each agent did, how long
it took and what it cost, without reading a transcript.

### v0.5 — Portable · ~2 days

Build tags for `agent/agent.go` and `tui/input.go` (G3), Windows CI job,
published Dockerfile.

### v1.0 — Supportable

Changelog, documented config schema with a stability promise, upgrade notes,
and secondmem positioned as an optional component rather than an assumption.

## 6. Authentication and distribution

Verified against each CLI's own `--help` on 2026-11-14:

| Agent | Interactive subscription | Non-interactive | BYOK key |
|---|---|---|---|
| claude | `/login` (Pro/Max) | `ANTHROPIC_API_KEY` | same |
| copilot | `/login` | `COPILOT_GITHUB_TOKEN` → `GH_TOKEN` → `GITHUB_TOKEN` | `COPILOT_PROVIDER_BASE_URL` + `COPILOT_PROVIDER_API_KEY` |
| opencode | `opencode providers` | `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, … | same |

Two consequences worth knowing:

- **Environment variables already work, with no code change.** `runCommand`
  (`agent/agent.go:84`) only overrides `cmd.Env` when `extraEnv` is non-empty;
  otherwise the child inherits the parent environment. A key exported in the
  shell reaches every agent. The `api_key` config field is a convenience, and
  it is currently ignored for claude and copilot (G2).
- **Copilot is the only agent whose subscription works headlessly**, because a
  GitHub token carries the entitlement. Claude Pro and ChatGPT Plus OAuth
  cannot be converted to a token: an ephemeral CI runner must use API keys and
  pay per token. This is a vendor constraint, not something Aros can design
  around.

## 7. Explicitly rejected

**A hosted, multi-user Aros.** Four independent reasons:

1. It would execute agents using other people's subscription credentials, which
   those CLIs' terms do not contemplate.
2. Agents share one working tree with no conflict protection (G1). Multi-tenancy
   turns a known single-user hazard into guaranteed data loss.
3. Running untrusted repositories requires real sandboxing — the same problem
   again, one layer down.
4. Claude Pro OAuth cannot be delegated, so every user needs an API key, which
   makes Aros a billing intermediary.

Cost: weeks of work plus legal exposure. Value over "the user installs a
binary": approximately zero.

**Baking credentials into a container image.** An image containing
`~/.claude/.credentials.json` is a published secret. Mounting them at runtime is
the only acceptable form, and it belongs in documentation, not in a Dockerfile.

**An API-key-only mode as the primary path.** It works, but it silently
converts a tool the user already pays a flat subscription for into a metered
one. Offer it for automation; never default to it.

## 8. Definition of done for v1.0

- `go install` then `aros doctor --probe` succeeds on a clean machine, or says
  precisely what is missing
- The full pipeline runs unattended with environment-variable credentials
- Two tasks cannot be co-scheduled onto the same file
- Every completed task has either a passing verify command or an explicit
  "unverified" marker
- Each run reports its cost
- `aros --version` identifies the build
- Linux, macOS and Windows all build in CI
- A README that gets a new user to a first successful plan without reading an ADR
