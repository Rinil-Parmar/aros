package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// ErrAgentNotAvailable is returned when the required CLI tool is not found on PATH.
var ErrAgentNotAvailable = errors.New("agent binary not found on PATH")

// Agent is the interface every AI tool adapter must implement.
type Agent interface {
	Name() string
	Run(ctx context.Context, prompt string) (AgentResult, error)
}

// ChatAgent is an optional interface for agents that support fast conversational replies
// without invoking tools (e.g. claude --tools "").
type ChatAgent interface {
	Chat(ctx context.Context, prompt string) (AgentResult, error)
}

// AgentResult holds the output from one agent call.
type AgentResult struct {
	AgentName string
	Output    string
	Stderr    string
	ExitCode  int
}

// killGracePeriod is how long a subprocess gets after ctx cancellation before
// its whole process group is killed.
const killGracePeriod = 5 * time.Second

// runCommand executes name with args, capturing stdout and stderr.
// workDir may be empty (inherits caller's cwd). stdin may be nil.
// extraEnv is appended to the current process environment (KEY=VALUE strings).
//
// The child is started in its own process group so that on timeout the whole
// tree (e.g. claude + its node helpers) is terminated, not just the direct child.
func runCommand(ctx context.Context, name string, args []string, workDir string, stdin io.Reader, extraEnv ...string) (stdout, stderr string, exitCode int, err error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if workDir != "" {
		cmd.Dir = workDir
	}
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	if stdin != nil {
		cmd.Stdin = stdin
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		// Negative pid targets the process group.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
	cmd.WaitDelay = killGracePeriod

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	runErr := cmd.Run()
	if runErr != nil && cmd.Process != nil {
		// Make sure nothing in the group survives (WaitDelay only kills the direct child).
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	exitCode = -1
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	if runErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return outBuf.String(), errBuf.String(), exitCode, fmt.Errorf("running %s: %w", name, ctxErr)
		}
		return outBuf.String(), errBuf.String(), exitCode, fmt.Errorf("running %s: %w%s", name, runErr, stderrSuffix(errBuf.String()))
	}
	return outBuf.String(), errBuf.String(), exitCode, nil
}

// stderrSuffix formats a trimmed stderr tail for error messages.
func stderrSuffix(stderr string) string {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return ""
	}
	const maxLen = 800
	if len(stderr) > maxLen {
		stderr = "…" + stderr[len(stderr)-maxLen:]
	}
	return "\nstderr: " + stderr
}
