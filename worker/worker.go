// Package worker executes a task manifest in dependency order with bounded
// concurrency. It is UI-agnostic: the CLI and the TUI both drive it through Hooks.
package worker

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Rinil-Parmar/aros/agent"
	"github.com/Rinil-Parmar/aros/human"
	"github.com/Rinil-Parmar/aros/memory"
	"github.com/Rinil-Parmar/aros/state"
)

// HumanSentinel is the marker an agent emits when it needs a human decision.
const HumanSentinel = "<<AROS_HUMAN>>"

const (
	maxHumanQuestionsPerTask = 5
	defaultPollInterval      = 2 * time.Second
)

// ErrTasksBlocked is returned when the run finished but some tasks are blocked.
var ErrTasksBlocked = errors.New("some tasks are blocked")

// Hooks lets the caller observe progress and answer agent questions.
// Every field is optional.
type Hooks struct {
	Log         func(line string)
	TaskStart   func(t *state.Task, agentName string)
	TaskOutput  func(t *state.Task, agentName, output string)
	TaskDone    func(t *state.Task, agentName string)
	TaskBlocked func(t *state.Task, agentName, reason string)
	// AskHuman is called (never concurrently) when an agent emits HumanSentinel.
	// Defaults to a stdin prompt.
	AskHuman func(t *state.Task, question string) (string, error)
	// Prompt overrides the default work prompt (e.g. to add dense-mode preambles).
	Prompt func(agentName string, t *state.Task, basePrompt, depOutputs, memCtx string) string
}

// Options tunes scheduling.
type Options struct {
	MaxConcurrent int
	TimeoutSec    int
	PollInterval  time.Duration
	// FallbackAgent runs tasks assigned to an agent that is not in the registry.
	// Empty = first registry agent (sorted).
	FallbackAgent string
}

// Result summarizes a run.
type Result struct {
	Done    int
	Blocked int
	Total   int
}

type runner struct {
	ctx      context.Context
	manifest *state.TaskManifest
	byID     map[string]*state.Task
	arosDir  string
	reg      agent.Registry
	mem      *memory.SecondMem
	opts     Options
	hooks    Hooks

	mu      sync.Mutex // guards every task field and manifest saves
	humanMu sync.Mutex // one human question at a time
	wake    chan struct{}
}

// Run executes all tasks in dependency order, up to opts.MaxConcurrent in parallel.
// Blocked and in-progress tasks from a previous run are retried. Tasks whose
// dependency is blocked are blocked too (so the run always terminates).
// Returns ErrTasksBlocked (wrapped) if any task ended blocked.
func Run(ctx context.Context, manifest *state.TaskManifest, arosDir string, reg agent.Registry, mem *memory.SecondMem, opts Options, hooks Hooks) (Result, error) {
	if opts.MaxConcurrent < 1 {
		opts.MaxConcurrent = 1
	}
	if opts.TimeoutSec < 1 {
		opts.TimeoutSec = 300
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = defaultPollInterval
	}
	if hooks.AskHuman == nil {
		hooks.AskHuman = func(t *state.Task, q string) (string, error) {
			fmt.Printf("\n[%s] agent needs human input:\n%s\n", t.ID, q)
			return human.AskInput("Your answer")
		}
	}
	if hooks.Log == nil {
		hooks.Log = func(string) {}
	}

	r := &runner{
		ctx: ctx, manifest: manifest, arosDir: arosDir, reg: reg, mem: mem,
		opts: opts, hooks: hooks,
		byID: make(map[string]*state.Task, len(manifest.Tasks)),
		wake: make(chan struct{}, 1),
	}
	state.AssignIDs(manifest.Tasks) // empty status → pending, missing ids → generated
	for i := range manifest.Tasks {
		r.byID[manifest.Tasks[i].ID] = &manifest.Tasks[i]
	}
	if len(r.byID) != len(manifest.Tasks) {
		return Result{}, fmt.Errorf("manifest has duplicate task ids")
	}

	if n := state.ResetForRetry(manifest); n > 0 {
		hooks.Log(fmt.Sprintf("retrying %d previously blocked/unfinished task(s)", n))
	}
	r.save()

	sem := make(chan struct{}, opts.MaxConcurrent)
	var wg sync.WaitGroup
	ticker := time.NewTicker(opts.PollInterval)
	defer ticker.Stop()

	for {
		r.mu.Lock()
		cascaded := r.cascadeBlocked()
		res := r.counts()
		finished := res.Done+res.Blocked == res.Total
		if !finished && ctx.Err() == nil {
		dispatch:
			for _, t := range r.sortedTasks() {
				if t.Status != state.TaskPending || !state.DepsComplete(t, r.byID) {
					continue
				}
				// Non-blocking acquire: never sleep on the semaphore while holding mu,
				// because finishing tasks need mu to record their result.
				select {
				case sem <- struct{}{}:
				default:
					break dispatch
				}
				t.Status = state.TaskInProgress
				t.BlockReason = ""
				r.saveLocked()
				wg.Add(1)
				go func(task *state.Task) {
					defer wg.Done()
					defer func() { <-sem }()
					r.execute(task)
					r.signal()
				}(t)
			}
		}
		r.mu.Unlock()
		for _, t := range cascaded {
			if hooks.TaskBlocked != nil {
				hooks.TaskBlocked(t, t.AssignedTo, t.BlockReason)
			}
		}

		if finished {
			break
		}
		if ctx.Err() != nil {
			// Let in-flight agents observe cancellation and record their state.
			wg.Wait()
			r.save()
			return r.snapshot(), ctx.Err()
		}
		select {
		case <-ctx.Done():
		case <-r.wake:
		case <-ticker.C:
		}
	}

	wg.Wait()
	r.save()
	res := r.snapshot()
	if res.Blocked > 0 {
		return res, fmt.Errorf("%w: %d blocked, %d done of %d", ErrTasksBlocked, res.Blocked, res.Done, res.Total)
	}
	return res, nil
}

func (r *runner) signal() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// counts must be called with mu held.
func (r *runner) counts() Result {
	res := Result{Total: len(r.byID)}
	for _, t := range r.byID {
		switch t.Status {
		case state.TaskDone:
			res.Done++
		case state.TaskBlocked:
			res.Blocked++
		}
	}
	return res
}

func (r *runner) snapshot() Result {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.counts()
}

// cascadeBlocked marks pending tasks whose dependency is blocked and returns
// them so the caller can notify hooks after releasing mu. Must hold mu.
func (r *runner) cascadeBlocked() []*state.Task {
	var out []*state.Task
	for changed := true; changed; {
		changed = false
		for _, t := range r.sortedTasks() {
			if t.Status != state.TaskPending {
				continue
			}
			if dep := state.BlockedDep(t, r.byID); dep != "" {
				t.Status = state.TaskBlocked
				t.BlockReason = "dependency " + dep + " is blocked"
				changed = true
				out = append(out, t)
			}
		}
	}
	if len(out) > 0 {
		r.saveLocked()
	}
	return out
}

// sortedTasks returns tasks in a stable order so dispatch is deterministic. Must hold mu.
func (r *runner) sortedTasks() []*state.Task {
	out := make([]*state.Task, 0, len(r.byID))
	for _, t := range r.byID {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (r *runner) save() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.saveLocked()
}

func (r *runner) saveLocked() {
	if r.arosDir == "" {
		return
	}
	if err := state.SaveManifest(r.arosDir, r.manifest); err != nil {
		r.hooks.Log("warning: could not save manifest: " + err.Error())
	}
}

func (r *runner) pickAgent(task *state.Task) (agent.Agent, string) {
	if a, ok := r.reg[task.AssignedTo]; ok {
		return a, task.AssignedTo
	}
	name := r.opts.FallbackAgent
	if _, ok := r.reg[name]; !ok {
		names := r.reg.Names()
		if len(names) == 0 {
			return nil, ""
		}
		name = names[0]
	}
	r.hooks.Log(fmt.Sprintf("[%s] agent %q not available — using %s", task.ID, task.AssignedTo, name))
	return r.reg[name], name
}

func (r *runner) block(task *state.Task, agentName, reason string) {
	r.mu.Lock()
	task.Status = state.TaskBlocked
	task.BlockReason = reason
	r.saveLocked()
	r.mu.Unlock()
	if r.hooks.TaskBlocked != nil {
		r.hooks.TaskBlocked(task, agentName, reason)
	}
}

func (r *runner) execute(task *state.Task) {
	a, agentName := r.pickAgent(task)
	if a == nil {
		r.block(task, task.AssignedTo, "no agent available")
		return
	}
	if r.hooks.TaskStart != nil {
		r.hooks.TaskStart(task, agentName)
	}

	r.mu.Lock()
	depOutputs := state.CollectDepOutputs(task, r.byID)
	description := task.Description
	r.mu.Unlock()

	memCtx := r.mem.Ask(r.ctx, task.Title)

	basePrompt := description
	questions := 0
	for {
		var prompt string
		if r.hooks.Prompt != nil {
			prompt = r.hooks.Prompt(agentName, task, basePrompt, depOutputs, memCtx)
		} else {
			prompt = BuildWorkPrompt(task, basePrompt, depOutputs, memCtx)
		}

		tctx, cancel := context.WithTimeout(r.ctx, time.Duration(r.opts.TimeoutSec)*time.Second)
		result, err := a.Run(tctx, prompt)
		cancel()
		if err != nil {
			r.block(task, agentName, fmt.Sprintf("agent %s failed: %v", agentName, err))
			return
		}

		if strings.Contains(result.Output, HumanSentinel) {
			if questions >= maxHumanQuestionsPerTask {
				r.block(task, agentName, "exceeded max human questions")
				return
			}
			question := ExtractHumanQuestion(result.Output)
			r.humanMu.Lock()
			answer, err := r.hooks.AskHuman(task, question)
			r.humanMu.Unlock()
			if err != nil {
				r.block(task, agentName, "human input unavailable: "+err.Error())
				return
			}
			basePrompt = description + "\n\nPrevious attempt:\n" + result.Output +
				"\n\nHuman answer to your question:\n" + answer + "\n\nContinue and complete the task."
			questions++
			continue
		}

		r.mu.Lock()
		task.Status = state.TaskDone
		task.Output = result.Output
		task.BlockReason = ""
		r.saveLocked()
		r.mu.Unlock()

		if r.hooks.TaskOutput != nil {
			r.hooks.TaskOutput(task, agentName, result.Output)
		}
		if r.hooks.TaskDone != nil {
			r.hooks.TaskDone(task, agentName)
		}
		// Ingest asynchronously — never hold up scheduling on the memory backend.
		go func(id, title, output string) {
			_ = r.mem.Ingest(context.Background(), fmt.Sprintf("Task %s (%s) result:\n%s", id, title, output))
		}(task.ID, task.Title, result.Output)
		return
	}
}

// BuildWorkPrompt is the default prompt for one task.
func BuildWorkPrompt(task *state.Task, basePrompt, depOutputs, memCtx string) string {
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
	sb.WriteString("output exactly '" + HumanSentinel + "' on its own line, followed by your specific question, then stop. ")
	sb.WriteString("Otherwise complete the task without interruption.")
	return sb.String()
}

// ExtractHumanQuestion returns the text following HumanSentinel.
func ExtractHumanQuestion(output string) string {
	idx := strings.Index(output, HumanSentinel)
	if idx == -1 {
		return output
	}
	q := strings.TrimSpace(output[idx+len(HumanSentinel):])
	if q == "" {
		return "(agent needs input but did not specify a question)"
	}
	return q
}
