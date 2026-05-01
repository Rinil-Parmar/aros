package worker

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Rinil-Parmar/aros/agent"
	"github.com/Rinil-Parmar/aros/human"
	"github.com/Rinil-Parmar/aros/memory"
	"github.com/Rinil-Parmar/aros/state"
)

const humanSentinel = "<<AROS_HUMAN>>"
const maxHumanQuestionsPerTask = 5
const pollInterval = 2 * time.Second

// Run executes all tasks in dependency order, up to maxConcurrent in parallel.
// Tasks are dispatched as their dependencies complete (polling every 2s).
func Run(ctx context.Context, manifest *state.TaskManifest, arosDir string, reg agent.Registry, mem *memory.SecondMem, maxConcurrent, timeoutSec int) error {
	tasks := manifest.Tasks
	byID := make(map[string]*state.Task, len(tasks))
	for i := range tasks {
		byID[tasks[i].ID] = &tasks[i]
	}

	fmt.Printf("\n=== WORK PHASE ===\n%d tasks to execute (max %d concurrent)\n\n", len(tasks), maxConcurrent)

	var mu sync.Mutex
	dispatched := make(map[string]bool)
	sem := make(chan struct{}, maxConcurrent)
	errCh := make(chan error, len(tasks))
	var wg sync.WaitGroup

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		// Check if all done
		mu.Lock()
		allDone := true
		for _, t := range byID {
			if t.Status != state.TaskDone {
				allDone = false
				break
			}
		}
		mu.Unlock()
		if allDone {
			break
		}

		// Dispatch newly-ready tasks
		mu.Lock()
		for _, t := range byID {
			if dispatched[t.ID] || t.Status == state.TaskDone {
				continue
			}
			if depsComplete(t, byID) {
				dispatched[t.ID] = true
				t.Status = state.TaskInProgress
				_ = state.SaveManifest(arosDir, manifest)

				wg.Add(1)
				sem <- struct{}{}
				go func(task *state.Task) {
					defer wg.Done()
					defer func() { <-sem }()

					if err := executeTask(ctx, task, byID, reg, mem, arosDir, manifest, timeoutSec); err != nil {
						errCh <- fmt.Errorf("task %s: %w", task.ID, err)
					}
				}(t)
			}
		}
		mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errCh:
			return err
		case <-ticker.C:
		}
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			return err
		}
	}

	fmt.Println("\n=== All tasks complete ===")
	return nil
}

func depsComplete(t *state.Task, byID map[string]*state.Task) bool {
	for _, dep := range t.Dependencies {
		d, ok := byID[dep]
		if !ok || d.Status != state.TaskDone {
			return false
		}
	}
	return true
}

func executeTask(ctx context.Context, task *state.Task, byID map[string]*state.Task, reg agent.Registry, mem *memory.SecondMem, arosDir string, manifest *state.TaskManifest, timeoutSec int) error {
	a, ok := reg[task.AssignedTo]
	if !ok {
		// Fallback to first available agent
		for _, av := range reg {
			a = av
			break
		}
	}

	fmt.Printf("[%s] starting → %s (%s)\n", task.ID, task.Title, a.Name())

	depOutputs := collectDepOutputs(task, byID)
	memCtx := mem.Ask(ctx, task.Title)

	prompt := task.Description
	questions := 0

	for {
		fullPrompt := buildWorkPrompt(task, prompt, depOutputs, memCtx)

		tctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
		result, err := a.Run(tctx, fullPrompt)
		cancel()
		if err != nil {
			task.Status = state.TaskBlocked
			task.BlockReason = err.Error()
			_ = state.SaveManifest(arosDir, manifest)
			return fmt.Errorf("agent %s failed: %w", a.Name(), err)
		}

		// Check for human decision sentinel
		if strings.Contains(result.Output, humanSentinel) {
			if questions >= maxHumanQuestionsPerTask {
				task.Status = state.TaskBlocked
				task.BlockReason = "exceeded max human questions"
				_ = state.SaveManifest(arosDir, manifest)
				return fmt.Errorf("task %s: too many human decisions required", task.ID)
			}
			question := extractHumanQuestion(result.Output)
			fmt.Printf("\n[%s] agent needs human input:\n%s\n", task.ID, question)
			answer, err := human.AskInput("Your answer")
			if err != nil {
				return err
			}
			prompt = task.Description + "\n\nPrevious attempt:\n" + result.Output +
				"\n\nHuman answer to your question:\n" + answer + "\n\nContinue and complete the task."
			questions++
			continue
		}

		task.Status = state.TaskDone
		task.Output = result.Output
		_ = state.SaveManifest(arosDir, manifest)
		_ = mem.Ingest(ctx, fmt.Sprintf("Task %s (%s) result:\n%s", task.ID, task.Title, result.Output))
		fmt.Printf("[%s] done ✓\n", task.ID)
		return nil
	}
}

func buildWorkPrompt(task *state.Task, basePrompt, depOutputs, memCtx string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("You are working on task [%s]: %s\n\n", task.ID, task.Title))
	sb.WriteString("TASK DESCRIPTION:\n")
	sb.WriteString(basePrompt)
	sb.WriteString("\n\n")

	if depOutputs != "" {
		sb.WriteString("OUTPUTS FROM PREREQUISITE TASKS:\n")
		sb.WriteString(depOutputs)
		sb.WriteString("\n\n")
	}
	if memCtx != "" {
		sb.WriteString("RELEVANT CONTEXT FROM SHARED MEMORY:\n")
		sb.WriteString(memCtx)
		sb.WriteString("\n\n")
	}

	sb.WriteString("Complete this task fully. If you need a human decision that you cannot proceed without, ")
	sb.WriteString("output exactly '<<AROS_HUMAN>>' on its own line, followed by your specific question, then stop. ")
	sb.WriteString("Otherwise complete the task without interruption.")
	return sb.String()
}

func collectDepOutputs(task *state.Task, byID map[string]*state.Task) string {
	var parts []string
	for _, dep := range task.Dependencies {
		d, ok := byID[dep]
		if !ok || d.Output == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("=== %s [%s] ===\n%s", d.Title, d.ID, d.Output))
	}
	return strings.Join(parts, "\n\n")
}

func extractHumanQuestion(output string) string {
	idx := strings.Index(output, humanSentinel)
	if idx == -1 {
		return output
	}
	q := strings.TrimSpace(output[idx+len(humanSentinel):])
	if q == "" {
		return "(agent needs input but did not specify a question)"
	}
	return q
}
