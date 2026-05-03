package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Rinil-Parmar/aros/state"
	"golang.org/x/sync/errgroup"
)

func (m *Model) ingestApprovedPlanAsync(projectName, plan string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := m.mem.Ingest(ctx, "Approved plan for "+projectName+":\n"+plan); err != nil {
			send(streamLineMsg{agent: "aros", line: "warning: secondmem ingest failed: " + err.Error()})
		}
	}()
}

// runPlan runs the plan phase in a goroutine, sending bubbletea messages back.
func (m *Model) runPlan(task string) {
	if err := state.RequirePhase(m.project, false, state.PhaseInit, state.PhasePlan); err != nil {
		send(phaseResultMsg{err: err})
		return
	}

	agents := m.reg.Enabled(m.cfg.Judge.Agent)
	judge, err := m.reg.Judge(m.cfg.Judge.Agent)
	if err != nil {
		send(phaseResultMsg{err: err})
		return
	}

	send(streamLineMsg{agent: "aros", line: fmt.Sprintf("Querying %d agent(s) for plans...", len(agents))})

	memCtx := m.mem.Ask(context.Background(), task)

	plans := make([]agentPlan, len(agents))
	g, gctx := errgroup.WithContext(context.Background())
	for i, a := range agents {
		i, a := i, a
		g.Go(func() error {
			agentCfg := m.cfg.Agents[a.Name()]
			send(agentActivityMsg{agent: a.Name(), model: agentCfg.Model, status: "running", line: "thinking..."})

			tctx, cancel := context.WithTimeout(gctx, time.Duration(m.cfg.Work.AgentTimeoutSeconds)*time.Second)
			defer cancel()
			prompt := withDense(buildPlanPrompt(task, memCtx), agentCfg)
			r, err := a.Run(tctx, prompt)
			if err != nil {
				send(streamLineMsg{agent: a.Name(), line: "error: " + err.Error()})
				send(agentActivityMsg{agent: a.Name(), status: "error", line: err.Error()})
				plans[i] = agentPlan{name: a.Name()}
				return nil // non-fatal
			}
			plans[i] = agentPlan{name: a.Name(), output: r.Output}
			for _, line := range strings.Split(r.Output, "\n") {
				if strings.TrimSpace(line) != "" {
					send(streamLineMsg{agent: a.Name(), line: line})
					send(agentActivityMsg{agent: a.Name(), line: line})
				}
			}
			send(agentActivityMsg{agent: a.Name(), status: "done", line: "plan ready"})
			return nil
		})
	}
	_ = g.Wait()

	judgeCfg := m.cfg.Agents[m.cfg.Judge.Agent]
	send(streamLineMsg{agent: "judge", line: "synthesizing plans..."})
	send(agentActivityMsg{agent: "judge", model: judgeCfg.Model, status: "running", line: "synthesizing plans..."})

	judgePrompt := withDense(buildJudgePlanPrompt(task, plans, ""), judgeCfg)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	judgeResult, err := judge.Run(ctx, judgePrompt)
	if err != nil {
		send(agentActivityMsg{agent: "judge", status: "error", line: err.Error()})
		send(phaseResultMsg{err: fmt.Errorf("judge: %w", err)})
		return
	}

	for _, line := range strings.Split(judgeResult.Output, "\n") {
		if strings.TrimSpace(line) != "" {
			send(streamLineMsg{agent: "judge", line: line})
			send(agentActivityMsg{agent: "judge", line: line})
		}
	}
	send(agentActivityMsg{agent: "judge", status: "done", line: "synthesis ready"})

	synthesis := judgeResult.Output

	send(approvalMsg{
		question: "Approve this plan?",
		onYes: func() {
			m.project.Task = task
			m.project.ApprovedPlan = synthesis
			m.project.Phase = state.PhasePlan
			_ = state.SaveState(m.arosDir, m.project)
			send(phaseResultMsg{phase: "plan_done"})
			m.ingestApprovedPlanAsync(m.project.ProjectName, synthesis)
		},
		onNo: func() {
			send(freeInputMsg{
				prompt: "What should change? (feedback for the judge)",
				callback: func(feedback string) {
					// m.busy is set true in handleInput before this goroutine starts
					send(agentActivityMsg{agent: "judge", model: judgeCfg.Model, status: "running", line: "revising plan..."})
					retryPrompt := withDense(buildJudgePlanPrompt(task, plans, feedback), judgeCfg)
					ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Minute)
					defer cancel2()
					r2, err := judge.Run(ctx2, retryPrompt)
					if err != nil {
						send(agentActivityMsg{agent: "judge", status: "error", line: err.Error()})
						send(phaseResultMsg{err: err})
						return
					}
					for _, line := range strings.Split(r2.Output, "\n") {
						if strings.TrimSpace(line) != "" {
							send(streamLineMsg{agent: "judge", line: line})
							send(agentActivityMsg{agent: "judge", line: line})
						}
					}
					send(agentActivityMsg{agent: "judge", status: "done", line: "revision ready"})
					revised := r2.Output
					send(approvalMsg{
						question: "Approve revised plan?",
						onYes: func() {
							m.project.Task = task
							m.project.ApprovedPlan = revised
							m.project.Phase = state.PhasePlan
							_ = state.SaveState(m.arosDir, m.project)
							send(phaseResultMsg{phase: "plan_done"})
							m.ingestApprovedPlanAsync(m.project.ProjectName, revised)
						},
						onNo: func() {
							send(streamLineMsg{agent: "aros", line: "Cancelled. Type 'plan <task>' to start over."})
							send(phaseResultMsg{phase: ""})
						},
					})
				},
			})
		},
	})
}

// runDivide runs the divide phase in a goroutine.
func (m *Model) runDivide() {
	if err := state.RequirePhase(m.project, false, state.PhasePlan); err != nil {
		send(phaseResultMsg{err: err})
		return
	}
	judge, err := m.reg.Judge(m.cfg.Judge.Agent)
	if err != nil {
		send(phaseResultMsg{err: err})
		return
	}

	judgeCfg := m.cfg.Agents[m.cfg.Judge.Agent]
	send(streamLineMsg{agent: "judge", line: "breaking plan into tasks..."})
	send(agentActivityMsg{agent: "judge", model: judgeCfg.Model, status: "running", line: "breaking plan into tasks..."})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	dividePrompt := withDense(buildDividePrompt(m.project, m.cfg), judgeCfg)
	result, err := judge.Run(ctx, dividePrompt)
	if err != nil {
		send(agentActivityMsg{agent: "judge", status: "error", line: err.Error()})
		send(phaseResultMsg{err: err})
		return
	}
	send(agentActivityMsg{agent: "judge", status: "done", line: "tasks generated"})

	tasks, parseErr := parseTasks(result.Output)
	if parseErr != nil {
		send(streamLineMsg{agent: "judge", line: "parse failed, retrying with stricter prompt..."})
		divideRetryPrompt := buildDividePrompt(m.project, m.cfg) +
			"\n\nPrevious response could not be parsed as JSON. Return ONLY the JSON array — no markdown, no prose, no fences."
		retryPrompt := withDense(divideRetryPrompt, judgeCfg)
		retryResult, err2 := judge.Run(ctx, retryPrompt)
		if err2 != nil {
			send(phaseResultMsg{err: fmt.Errorf("judge retry failed: %w", err2)})
			return
		}
		tasks, parseErr = parseTasks(retryResult.Output)
		if parseErr != nil {
			send(phaseResultMsg{err: fmt.Errorf("could not parse task list after retry: %w", parseErr)})
			return
		}
	}

	assignIDs(tasks)
	if err := detectCycles(tasks); err != nil {
		send(phaseResultMsg{err: fmt.Errorf("dependency cycle: %w", err)})
		return
	}

	send(streamLineMsg{agent: "judge", line: fmt.Sprintf("Generated %d tasks:", len(tasks))})
	send(streamLineMsg{agent: "judge", line: fmt.Sprintf("%-12s %-28s %-14s %s", "ID", "Title", "Agent", "Deps")})
	send(streamLineMsg{agent: "judge", line: strings.Repeat("─", 65)})
	for _, t := range tasks {
		deps := strings.Join(t.Dependencies, ",")
		if deps == "" {
			deps = "—"
		}
		title := t.Title
		if len(title) > 27 {
			title = title[:24] + "..."
		}
		send(streamLineMsg{agent: "judge", line: fmt.Sprintf("%-12s %-28s %-14s %s", t.ID, title, t.AssignedTo, deps)})
	}

	manifest := &state.TaskManifest{Tasks: tasks}

	send(approvalMsg{
		question: fmt.Sprintf("Approve %d task assignments?", len(tasks)),
		onYes: func() {
			_ = state.SaveManifest(m.arosDir, manifest)
			m.project.Phase = state.PhaseDivide
			_ = state.SaveState(m.arosDir, m.project)
			send(phaseResultMsg{phase: "divide_done"})
		},
		onNo: func() {
			send(streamLineMsg{agent: "aros", line: "Cancelled. Type 'divide' to try again."})
			send(phaseResultMsg{phase: ""})
		},
	})
}

// runWork runs the work phase in a goroutine.
func (m *Model) runWork() {
	if err := state.RequirePhase(m.project, false, state.PhaseDivide, state.PhaseWork); err != nil {
		send(phaseResultMsg{err: err})
		return
	}
	manifest, err := state.LoadManifest(m.arosDir)
	if err != nil {
		send(phaseResultMsg{err: err})
		return
	}

	byID := make(map[string]*state.Task, len(manifest.Tasks))
	for i := range manifest.Tasks {
		byID[manifest.Tasks[i].ID] = &manifest.Tasks[i]
	}
	if len(byID) == 0 {
		send(phaseResultMsg{err: fmt.Errorf("no tasks to execute; run divide first")})
		return
	}

	m.project.Phase = state.PhaseWork
	_ = state.SaveState(m.arosDir, m.project)

	maxConcurrent := m.cfg.Work.MaxConcurrent
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	send(streamLineMsg{agent: "aros", line: fmt.Sprintf("Starting %d tasks (max %d concurrent)...", len(manifest.Tasks), maxConcurrent)})

	var mu sync.Mutex
	dispatched := make(map[string]bool)
	sem := make(chan struct{}, maxConcurrent)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		mu.Lock()
		done, blocked, total := 0, 0, len(byID)
		for _, t := range byID {
			switch t.Status {
			case state.TaskDone:
				done++
			case state.TaskBlocked:
				blocked++
			}
		}
		finished := done+blocked == total
		if !finished {
			for _, t := range byID {
				if dispatched[t.ID] || t.Status == state.TaskDone {
					continue
				}
				if depsComplete(t, byID) {
					dispatched[t.ID] = true
					t.Status = state.TaskInProgress
					t.BlockReason = ""
					_ = state.SaveManifest(m.arosDir, manifest)
					sem <- struct{}{}
					go func(task *state.Task) {
						defer func() { <-sem }()
						m.execTask(task, &mu, byID, manifest)
					}(t)
				}
			}
		}
		mu.Unlock()
		if finished {
			if blocked > 0 {
				_ = state.SaveManifest(m.arosDir, manifest)
				send(phaseResultMsg{err: fmt.Errorf("%d task(s) blocked, %d done. Fix blockers and run 'work' again to resume", blocked, done)})
				return
			}
			break
		}
		<-ticker.C
	}

	m.project.Phase = state.PhaseDone
	_ = state.SaveState(m.arosDir, m.project)
	send(phaseResultMsg{phase: "work_done"})
}

func (m *Model) execTask(task *state.Task, mu *sync.Mutex, byID map[string]*state.Task, manifest *state.TaskManifest) {
	a, ok := m.reg[task.AssignedTo]
	if !ok {
		for _, av := range m.reg {
			a = av
			break
		}
	}

	agentCfg := m.cfg.Agents[task.AssignedTo]
	send(streamLineMsg{agent: task.AssignedTo, line: fmt.Sprintf("[%s] ► %s", task.ID, task.Title)})
	send(agentActivityMsg{
		agent:  task.AssignedTo,
		model:  agentCfg.Model,
		status: "running",
		line:   fmt.Sprintf("[%s] %s", task.ID, task.Title),
	})

	mu.Lock()
	depOutputs := collectDepOutputs(task, byID)
	mu.Unlock()

	memCtx := m.mem.Ask(context.Background(), task.Title)

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(m.cfg.Work.AgentTimeoutSeconds)*time.Second)
	defer cancel()

	workPrompt := withDense(buildWorkPrompt(task, depOutputs, memCtx), agentCfg)
	result, err := a.Run(ctx, workPrompt)
	if err != nil {
		send(streamLineMsg{agent: task.AssignedTo, line: fmt.Sprintf("[%s] ✗ error: %v", task.ID, err)})
		send(agentActivityMsg{agent: task.AssignedTo, status: "error", line: err.Error()})
		mu.Lock()
		task.Status = state.TaskBlocked
		task.BlockReason = err.Error()
		_ = state.SaveManifest(m.arosDir, manifest)
		mu.Unlock()
		return
	}

	for _, line := range strings.Split(result.Output, "\n") {
		if strings.TrimSpace(line) != "" {
			send(streamLineMsg{agent: task.AssignedTo, line: line})
			send(agentActivityMsg{agent: task.AssignedTo, line: line})
		}
	}

	mu.Lock()
	task.Status = state.TaskDone
	task.Output = result.Output
	_ = state.SaveManifest(m.arosDir, manifest)
	mu.Unlock()
	send(streamLineMsg{agent: task.AssignedTo, line: fmt.Sprintf("[%s] ✓ done", task.ID)})
	send(agentActivityMsg{agent: task.AssignedTo, status: "done", line: fmt.Sprintf("[%s] done", task.ID)})
	// ingest after reporting done — never block task completion progress
	go func() {
		_ = m.mem.Ingest(context.Background(), fmt.Sprintf("Task %s (%s):\n%s", task.ID, task.Title, result.Output))
	}()
}
