package state

import "time"

type Phase string

const (
	PhaseInit   Phase = "init"
	PhasePlan   Phase = "plan"
	PhaseDivide Phase = "divide"
	PhaseWork   Phase = "work"
	PhaseDone   Phase = "done"
)

// PhaseOrder maps phases to numeric order for forward-only validation.
var PhaseOrder = map[Phase]int{
	PhaseInit:   0,
	PhasePlan:   1,
	PhaseDivide: 2,
	PhaseWork:   3,
	PhaseDone:   4,
}

var KnownPhases = []Phase{
	PhaseInit,
	PhasePlan,
	PhaseDivide,
	PhaseWork,
	PhaseDone,
}

func ParsePhase(v string) (Phase, bool) {
	p := Phase(v)
	_, ok := PhaseOrder[p]
	return p, ok
}

type ProjectState struct {
	ProjectName  string    `json:"project_name"`
	Phase        Phase     `json:"phase"`
	Task         string    `json:"task"`
	ApprovedPlan string    `json:"approved_plan"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type TaskStatus string

const (
	TaskPending    TaskStatus = "pending"
	TaskInProgress TaskStatus = "in_progress"
	TaskDone       TaskStatus = "done"
	TaskBlocked    TaskStatus = "blocked"
)

type Task struct {
	ID           string     `json:"id"`
	Title        string     `json:"title"`
	Description  string     `json:"description"`
	AssignedTo   string     `json:"assigned_to"`
	Dependencies []string   `json:"dependencies"`
	Status       TaskStatus `json:"status"`
	Output       string     `json:"output"`
	BlockReason  string     `json:"block_reason"`
}

type TaskManifest struct {
	Tasks []Task `json:"tasks"`
}
