package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// ErrAgentNotAvailable is returned when the required CLI tool is not found on PATH.
var ErrAgentNotAvailable = errors.New("agent binary not found on PATH")

// Agent is the interface every AI tool adapter must implement.
type Agent interface {
	Name() string
	Run(ctx context.Context, prompt string) (AgentResult, error)
}

// AgentResult holds the output from one agent call.
type AgentResult struct {
	AgentName string
	Output    string
	Stderr    string
	ExitCode  int
}

// runCommand executes name with args, capturing stdout and stderr.
// workDir may be empty (inherits caller's cwd).
func runCommand(ctx context.Context, name string, args []string, workDir string) (stdout, stderr string, exitCode int, err error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if workDir != "" {
		cmd.Dir = workDir
	}
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	runErr := cmd.Run()
	exitCode = -1
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	if runErr != nil && outBuf.Len() == 0 {
		return "", errBuf.String(), exitCode, fmt.Errorf("running %s: %w\nstderr: %s", name, runErr, errBuf.String())
	}
	return outBuf.String(), errBuf.String(), exitCode, nil
}
