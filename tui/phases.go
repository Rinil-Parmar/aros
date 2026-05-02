package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Rinil-Parmar/aros/state"
	"golang.org/x/sync/errgroup"
)

// runPlan runs the plan phase in a goroutine, sending bubbletea messages back.
func (m *Model) runPlan(task string) {
	if err := state.RequirePhase(m.project, false, state.PhaseInit, state.PhasePlan); err != nil {
		send(phaseResultMsg{err: err})
		return
	}

	agents := m.reg.Enabled()
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
			agentModel := m.cfg.Agents[a.Name()].Model
			send(agentActivityMsg{agent: a.Name(), model: agentModel, status: "running", line: "thinking..."})

			tctx, cancel := context.WithTimeout(gctx, time.Duration(m.cfg.Work.AgentTimeoutSeconds)*time.Second)
			defer cancel()
			r, err := a.Run(tctx, buildPlanPrompt(task, memCtx))
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

	judgeModel := m.cfg.Agents[m.cfg.Judge.Agent].Model
	send(streamLineMsg{agent: "judge", line: "synthesizing plans..."})
	send(agentActivityMsg{agent: "judge", model: judgeModel, status: "running", line: "synthesizing plans..."})

	judgePrompt := buildJudgePlanPrompt(task, plans, "")
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
			_ = m.mem.Ingest(context.Background(), "Approved plan for "+m.project.ProjectName+":\n"+synthesis)
			send(phaseResultMsg{phase: "plan_done"})
		},
		onNo: func() {
			send(freeInputMsg{
				prompt: "What should change? (feedback for the judge)",
				callback: func(feedback string) {
					m.busy = true
					send(agentActivityMsg{agent: "judge", model: judgeModel, status: "running", line: "revising plan..."})
					retryPrompt := buildJudgePlanPrompt(task, plans, feedback)
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

	judgeModel := m.cfg.Agents[m.cfg.Judge.Agent].Model
	send(streamLineMsg{agent: "judge", line: "breaking plan into tasks..."})
	send(agentActivityMsg{agent: "judge", model: judgeModel, status: "running", line: "breaking plan into tasks..."})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	result, err := judge.Run(ctx, buildDividePrompt(m.project, m.cfg))
	if err != nil {
		send(agentActivityMsg{agent: "judge", status: "error", line: err.Error()})
		send(phaseResultMsg{err: err})
		return
	}
	send(agentActivityMsg{agent: "judge", status: "done", line: "tasks generated"})

	tasks, err := parseTasks(result.Output)
	if err != nil {
		// Retry once
		retryResult, err2 := judge.Run(ctx, buildDividePrompt(m.project, m.cfg)+
			"\n\nReturn ONLY a JSON array. No prose. No fences.")
		if err2 != nil || func() bool { tasks, err = parseTasks(retryResult.Output); return err != nil }() {
			send(phaseResultMsg{err: fmt.Errorf("could not parse task list: %w", err)})
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
	if err := state.RequirePhase(m.project, false, state.PhaseDivide); err != nil {
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

	maxConcurrent := m.cfg.Work.MaxConcurrent
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	send(streamLineMsg{agent: "aros", line: fmt.Sprintf("Starting %d tasks (max %d concurrent)...", len(manifest.Tasks), maxConcurrent)})

	dispatched := make(map[string]bool)
	sem := make(chan struct{}, maxConcurrent)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		allDone := true
		for _, t := range byID {
			if t.Status != state.TaskDone {
				allDone = false
				break
			}
		}
		if allDone {
			break
		}

		for _, t := range byID {
			if dispatched[t.ID] || t.Status == state.TaskDone {
				continue
			}
			if depsComplete(t, byID) {
				dispatched[t.ID] = true
				t.Status = state.TaskInProgress
				_ = state.SaveManifest(m.arosDir, manifest)
				sem <- struct{}{}
				go func(task *state.Task) {
					defer func() { <-sem }()
					m.execTask(task, byID, manifest)
				}(t)
			}
		}
		<-ticker.C
	}

	m.project.Phase = state.PhaseDone
	_ = state.SaveState(m.arosDir, m.project)
	send(phaseResultMsg{phase: "work_done"})
}

func (m *Model) execTask(task *state.Task, byID map[string]*state.Task, manifest *state.TaskManifest) {
	a, ok := m.reg[task.AssignedTo]
	if !ok {
		for _, av := range m.reg {
			a = av
			break
		}
	}

	agentModel := ""
	if ac, ok := m.cfg.Agents[task.AssignedTo]; ok {
		agentModel = ac.Model
	}

	send(streamLineMsg{agent: task.AssignedTo, line: fmt.Sprintf("[%s] ► %s", task.ID, task.Title)})
	send(agentActivityMsg{
		agent:  task.AssignedTo,
		model:  agentModel,
		status: "running",
		line:   fmt.Sprintf("[%s] %s", task.ID, task.Title),
	})

	depOutputs := collectDepOutputs(task, byID)
	memCtx := m.mem.Ask(context.Background(), task.Title)

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(m.cfg.Work.AgentTimeoutSeconds)*time.Second)
	defer cancel()

	result, err := a.Run(ctx, buildWorkPrompt(task, depOutputs, memCtx))
	if err != nil {
		send(streamLineMsg{agent: task.AssignedTo, line: fmt.Sprintf("[%s] ✗ error: %v", task.ID, err)})
		send(agentActivityMsg{agent: task.AssignedTo, status: "error", line: err.Error()})
		task.Status = state.TaskBlocked
		task.BlockReason = err.Error()
		_ = state.SaveManifest(m.arosDir, manifest)
		return
	}

	for _, line := range strings.Split(result.Output, "\n") {
		if strings.TrimSpace(line) != "" {
			send(streamLineMsg{agent: task.AssignedTo, line: line})
			send(agentActivityMsg{agent: task.AssignedTo, line: line})
		}
	}

	task.Status = state.TaskDone
	task.Output = result.Output
	_ = state.SaveManifest(m.arosDir, manifest)
	_ = m.mem.Ingest(context.Background(), fmt.Sprintf("Task %s (%s):\n%s", task.ID, task.Title, result.Output))
	send(streamLineMsg{agent: task.AssignedTo, line: fmt.Sprintf("[%s] ✓ done", task.ID)})
	send(agentActivityMsg{agent: task.AssignedTo, status: "done", line: fmt.Sprintf("[%s] done", task.ID)})
}
