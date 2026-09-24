package tui

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"time"

	"github.com/Rinil-Parmar/aros/agent"
	"github.com/Rinil-Parmar/aros/state"
	"github.com/Rinil-Parmar/aros/worker"
	"golang.org/x/sync/errgroup"
)

// Phase goroutines only receive a phaseRun snapshot and talk back via send().
// They never read or write Model fields — see messages.go for the rule.

const judgeTimeout = 10 * time.Minute

// safeGo runs a phase goroutine that can never take the program down: an
// unrecovered panic anywhere would kill the process and leave the terminal in
// the alternate screen. A panic is reported as a phase error instead, which
// also releases the busy flag so the UI stays usable.
// gen is the generation the goroutine belongs to (0 = ungated).
func safeGo(gen int, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				send(phaseResultMsg{gen: gen, err: panicErr(r)})
			}
		}()
		fn()
	}()
}

// safeGoQuiet is safeGo for background work that owns no phase (e.g. memory
// ingest): a panic is logged, not turned into a phase result.
func safeGoQuiet(fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				send(streamLineMsg{agent: "aros", line: "warning: " + panicErr(r).Error()})
			}
		}()
		fn()
	}()
}

func panicErr(r any) error {
	return fmt.Errorf("internal error: %v\n%s", r, truncate(string(debug.Stack()), 600))
}

func streamOutput(gen int, agentName, output string) {
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) != "" {
			send(streamLineMsg{gen: gen, agent: agentName, line: line})
		}
	}
}

func ingestAsync(pr phaseRun, text string) {
	safeGoQuiet(func() {
		if err := pr.mem.Ingest(context.Background(), text); err != nil {
			send(streamLineMsg{gen: pr.gen, agent: "aros", line: "warning: secondmem ingest failed: " + err.Error()})
		}
	})
}

// runPlan runs the plan phase in a goroutine, sending bubbletea messages back.
func runPlan(pr phaseRun, task string) {
	if err := state.RequirePhase(&pr.project, false, state.PhaseInit, state.PhasePlan); err != nil {
		send(phaseResultMsg{gen: pr.gen, err: err})
		return
	}

	judgeName := pr.cfg.Judge.Agent
	judge, err := pr.reg.Judge(judgeName)
	if err != nil {
		send(phaseResultMsg{gen: pr.gen, err: err})
		return
	}
	agents := pr.reg.Enabled(judgeName)
	if len(agents) == 0 {
		// Judge is the only agent: it drafts the plan itself, then synthesizes.
		agents = []agent.Agent{judge}
	}

	send(streamLineMsg{gen: pr.gen, agent: "aros", line: fmt.Sprintf("Querying %d agent(s) for plans...", len(agents))})

	memCtx := pr.mem.Ask(pr.ctx, task)

	plans := make([]agentPlan, len(agents))
	var g errgroup.Group
	for i, a := range agents {
		g.Go(func() error {
			agentCfg := pr.cfg.Agents[a.Name()]
			send(agentActivityMsg{gen: pr.gen, agent: a.Name(), model: agentCfg.Model, status: "running", line: "thinking..."})

			tctx, cancel := context.WithTimeout(pr.ctx, time.Duration(pr.cfg.Work.AgentTimeoutSeconds)*time.Second)
			defer cancel()
			prompt := withDense(buildPlanPrompt(task, memCtx), agentCfg)
			r, err := agent.Reason(tctx, a, prompt)
			if err != nil {
				send(streamLineMsg{gen: pr.gen, agent: a.Name(), line: "error: " + err.Error()})
				send(agentActivityMsg{gen: pr.gen, agent: a.Name(), status: "error", line: err.Error()})
				return nil // non-fatal: other agents may still deliver
			}
			if strings.TrimSpace(r.Output) == "" {
				send(agentActivityMsg{gen: pr.gen, agent: a.Name(), status: "error", line: "empty plan"})
				return nil
			}
			plans[i] = agentPlan{name: a.Name(), output: r.Output}
			streamOutput(pr.gen, a.Name(), r.Output)
			send(agentActivityMsg{gen: pr.gen, agent: a.Name(), status: "done", line: "plan ready"})
			return nil
		})
	}
	_ = g.Wait()

	var good []agentPlan
	for _, p := range plans {
		if p.output != "" {
			good = append(good, p)
		}
	}
	if len(good) == 0 {
		send(phaseResultMsg{gen: pr.gen, err: fmt.Errorf("every agent failed to produce a plan — check credentials/models with /agents")})
		return
	}
	if pr.ctx.Err() != nil {
		send(phaseResultMsg{gen: pr.gen, err: pr.ctx.Err()})
		return
	}

	judgeCfg := pr.cfg.Agents[judgeName]
	synthesize := func(feedback string) (string, error) {
		label := "synthesizing plans..."
		if feedback != "" {
			label = "revising plan..."
		}
		send(streamLineMsg{gen: pr.gen, agent: "judge", line: label})
		send(agentActivityMsg{gen: pr.gen, agent: "judge", model: judgeCfg.Model, status: "running", line: label})
		ctx, cancel := context.WithTimeout(pr.ctx, judgeTimeout)
		defer cancel()
		r, err := agent.Reason(ctx, judge, withDense(buildJudgePlanPrompt(task, good, feedback), judgeCfg))
		if err != nil {
			send(agentActivityMsg{gen: pr.gen, agent: "judge", status: "error", line: err.Error()})
			return "", fmt.Errorf("judge: %w", err)
		}
		streamOutput(pr.gen, "judge", r.Output)
		send(agentActivityMsg{gen: pr.gen, agent: "judge", status: "done", line: "synthesis ready"})
		return r.Output, nil
	}

	approve := func(plan string) {
		proj := pr.project // copy
		proj.Task = task
		proj.ApprovedPlan = plan
		proj.Phase = state.PhasePlan
		if err := state.SaveState(pr.arosDir, &proj); err != nil {
			send(phaseResultMsg{gen: pr.gen, err: fmt.Errorf("saving state: %w", err)})
			return
		}
		send(phaseResultMsg{gen: pr.gen, phase: "plan_done", project: &proj})
		ingestAsync(pr, "Approved plan for "+proj.ProjectName+":\n"+plan)
	}

	// Up to 3 synthesis rounds: initial + 2 revisions.
	var round func(feedback string, attempt int)
	round = func(feedback string, attempt int) {
		plan, err := synthesize(feedback)
		if err != nil {
			send(phaseResultMsg{gen: pr.gen, err: err})
			return
		}
		q := "Approve this plan?"
		if attempt > 1 {
			q = "Approve revised plan?"
		}
		send(approvalMsg{gen: pr.gen,
			question: q,
			onYes:    func() { approve(plan) },
			onNo: func() {
				if attempt >= 3 {
					send(streamLineMsg{gen: pr.gen, agent: "aros", line: "Plan not approved after 3 rounds. Type 'plan <task>' to start over."})
					send(phaseResultMsg{gen: pr.gen, phase: ""})
					return
				}
				send(freeInputMsg{gen: pr.gen,
					prompt:   "What should change? (feedback for the judge)",
					callback: func(fb string) { round(fb, attempt+1) },
				})
			},
		})
	}
	round("", 1)
}

// runDivide runs the divide phase in a goroutine.
func runDivide(pr phaseRun) {
	if err := state.RequirePhase(&pr.project, false, state.PhasePlan); err != nil {
		send(phaseResultMsg{gen: pr.gen, err: err})
		return
	}
	if strings.TrimSpace(pr.project.ApprovedPlan) == "" {
		send(phaseResultMsg{gen: pr.gen, err: fmt.Errorf("no approved plan — run 'plan <task>' first")})
		return
	}
	judgeName := pr.cfg.Judge.Agent
	judge, err := pr.reg.Judge(judgeName)
	if err != nil {
		send(phaseResultMsg{gen: pr.gen, err: err})
		return
	}

	judgeCfg := pr.cfg.Agents[judgeName]
	send(streamLineMsg{gen: pr.gen, agent: "judge", line: "breaking plan into tasks..."})
	send(agentActivityMsg{gen: pr.gen, agent: "judge", model: judgeCfg.Model, status: "running", line: "breaking plan into tasks..."})

	ctx, cancel := context.WithTimeout(pr.ctx, judgeTimeout)
	defer cancel()

	available := pr.reg.Names()
	basePrompt := buildDividePrompt(&pr.project, pr.cfg, available)

	var tasks []state.Task
	feedback := ""
	for attempt := 1; attempt <= 3; attempt++ {
		prompt := basePrompt
		if feedback != "" {
			prompt += "\n\n" + feedback
		}
		result, err := agent.Reason(ctx, judge, withDense(prompt, judgeCfg))
		if err != nil {
			send(agentActivityMsg{gen: pr.gen, agent: "judge", status: "error", line: err.Error()})
			send(phaseResultMsg{gen: pr.gen, err: fmt.Errorf("judge: %w", err)})
			return
		}
		parsed, perr := state.ParseTasks(result.Output)
		if perr != nil {
			send(streamLineMsg{gen: pr.gen, agent: "judge", line: fmt.Sprintf("could not parse task JSON (%v), retrying %d/3...", perr, attempt)})
			feedback = "Previous response could not be parsed as JSON. Return ONLY the JSON array — no markdown, no prose, no fences."
			continue
		}
		state.AssignIDs(parsed)
		if verr := state.ValidateTasks(parsed); verr != nil {
			send(streamLineMsg{gen: pr.gen, agent: "judge", line: fmt.Sprintf("invalid task graph (%v), retrying %d/3...", verr, attempt)})
			feedback = fmt.Sprintf("The previous task list was invalid: %v. Fix it and return ONLY the JSON array.", verr)
			continue
		}
		tasks = parsed
		break
	}
	if tasks == nil {
		send(agentActivityMsg{gen: pr.gen, agent: "judge", status: "error", line: "no valid task list"})
		send(phaseResultMsg{gen: pr.gen, err: fmt.Errorf("judge did not produce a valid task list after 3 attempts")})
		return
	}
	send(agentActivityMsg{gen: pr.gen, agent: "judge", status: "done", line: "tasks generated"})

	if reassigned := state.NormalizeAssignments(tasks, available, judgeName); len(reassigned) > 0 {
		send(streamLineMsg{gen: pr.gen, agent: "aros", line: fmt.Sprintf("note: %d task(s) named an unavailable agent — reassigned to %s: %s",
			len(reassigned), judgeName, strings.Join(reassigned, ", "))})
	}

	send(streamLineMsg{gen: pr.gen, agent: "judge", line: fmt.Sprintf("Generated %d tasks:", len(tasks))})
	send(streamLineMsg{gen: pr.gen, agent: "judge", line: fmt.Sprintf("%-12s %-28s %-14s %s", "ID", "Title", "Agent", "Deps")})
	send(streamLineMsg{gen: pr.gen, agent: "judge", line: strings.Repeat("─", 65)})
	for _, t := range tasks {
		deps := strings.Join(t.Dependencies, ",")
		if deps == "" {
			deps = "—"
		}
		send(streamLineMsg{gen: pr.gen, agent: "judge", line: fmt.Sprintf("%-12s %-28s %-14s %s", t.ID, truncate(t.Title, 27), t.AssignedTo, deps)})
	}

	manifest := &state.TaskManifest{Tasks: tasks}

	send(approvalMsg{gen: pr.gen,
		question: fmt.Sprintf("Approve %d task assignments?", len(tasks)),
		onYes: func() {
			if err := state.SaveManifest(pr.arosDir, manifest); err != nil {
				send(phaseResultMsg{gen: pr.gen, err: fmt.Errorf("saving manifest: %w", err)})
				return
			}
			proj := pr.project
			proj.Phase = state.PhaseDivide
			if err := state.SaveState(pr.arosDir, &proj); err != nil {
				send(phaseResultMsg{gen: pr.gen, err: fmt.Errorf("saving state: %w", err)})
				return
			}
			send(manifestChangedMsg{})
			send(phaseResultMsg{gen: pr.gen, phase: "divide_done", project: &proj})
		},
		onNo: func() {
			send(streamLineMsg{gen: pr.gen, agent: "aros", line: "Cancelled. Type 'divide' to try again."})
			send(phaseResultMsg{gen: pr.gen, phase: ""})
		},
	})
}

// runWork runs the work phase in a goroutine via the shared worker scheduler.
func runWork(pr phaseRun) {
	if err := state.RequirePhase(&pr.project, false, state.PhaseDivide, state.PhaseWork); err != nil {
		send(phaseResultMsg{gen: pr.gen, err: err})
		return
	}
	manifest, err := state.LoadManifest(pr.arosDir)
	if err != nil {
		send(phaseResultMsg{gen: pr.gen, err: err})
		return
	}
	if len(manifest.Tasks) == 0 {
		send(phaseResultMsg{gen: pr.gen, err: fmt.Errorf("no tasks to execute; run divide first")})
		return
	}

	// Each message gets its own copy: the model keeps the pointer it receives.
	started := pr.project
	started.Phase = state.PhaseWork
	_ = state.SaveState(pr.arosDir, &started)
	send(phaseResultMsg{gen: pr.gen, phase: "work_started", project: &started})

	maxConcurrent := pr.cfg.Work.MaxConcurrent
	send(streamLineMsg{gen: pr.gen, agent: "aros", line: fmt.Sprintf("Starting %d tasks (max %d concurrent)...", len(manifest.Tasks), maxConcurrent)})

	opts := worker.Options{
		MaxConcurrent: maxConcurrent,
		TimeoutSec:    pr.cfg.Work.AgentTimeoutSeconds,
		FallbackAgent: pr.cfg.Judge.Agent,
	}
	hooks := worker.Hooks{
		Log: func(line string) { send(streamLineMsg{gen: pr.gen, agent: "aros", line: line}) },
		TaskStart: func(t *state.Task, agentName string) {
			send(streamLineMsg{gen: pr.gen, agent: agentName, line: fmt.Sprintf("[%s] ► %s", t.ID, t.Title)})
			send(agentActivityMsg{gen: pr.gen, agent: agentName, model: pr.cfg.Agents[agentName].Model, status: "running", line: fmt.Sprintf("[%s] %s", t.ID, t.Title)})
			send(manifestChangedMsg{})
		},
		TaskOutput: func(t *state.Task, agentName, output string) {
			streamOutput(pr.gen, agentName, output)
		},
		TaskDone: func(t *state.Task, agentName string) {
			send(streamLineMsg{gen: pr.gen, agent: agentName, line: fmt.Sprintf("[%s] ✓ done", t.ID)})
			send(agentActivityMsg{gen: pr.gen, agent: agentName, status: "done", line: fmt.Sprintf("[%s] done", t.ID)})
			send(manifestChangedMsg{})
		},
		TaskBlocked: func(t *state.Task, agentName, reason string) {
			send(streamLineMsg{gen: pr.gen, agent: agentName, line: fmt.Sprintf("[%s] ✗ blocked: %s", t.ID, reason)})
			send(agentActivityMsg{gen: pr.gen, agent: agentName, status: "error", line: truncate(reason, 60)})
			send(manifestChangedMsg{})
		},
		AskHuman: func(t *state.Task, question string) (string, error) {
			send(streamLineMsg{gen: pr.gen, agent: t.AssignedTo, line: fmt.Sprintf("[%s] needs input: %s", t.ID, question)})
			answerCh := make(chan string, 1)
			send(freeInputMsg{gen: pr.gen,
				prompt:   fmt.Sprintf("[%s] %s", t.ID, question),
				callback: func(text string) { answerCh <- text },
			})
			select {
			case a := <-answerCh:
				return a, nil
			case <-pr.ctx.Done():
				return "", pr.ctx.Err()
			}
		},
		Prompt: func(agentName string, t *state.Task, base, depOutputs, memCtx string) string {
			return withDense(worker.BuildWorkPrompt(t, base, depOutputs, memCtx), pr.cfg.Agents[agentName])
		},
	}

	res, err := worker.Run(pr.ctx, manifest, pr.arosDir, pr.reg, pr.mem, opts, hooks)
	send(manifestChangedMsg{})
	if err != nil {
		if errors.Is(err, worker.ErrTasksBlocked) {
			send(phaseResultMsg{gen: pr.gen, err: fmt.Errorf("%d of %d done, %d blocked — fix the blockers (see status) and type 'work' to retry them", res.Done, res.Total, res.Blocked)})
			return
		}
		send(phaseResultMsg{gen: pr.gen, err: err})
		return
	}

	finished := pr.project
	finished.Phase = state.PhaseDone
	_ = state.SaveState(pr.arosDir, &finished)
	send(phaseResultMsg{gen: pr.gen, phase: "work_done", project: &finished})
}
