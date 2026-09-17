package memory

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Bounded timeouts: secondmem talks to a local LLM (Ollama). If that stalls,
// a phase must degrade to "no memory context" instead of hanging forever.
const (
	askTimeout    = 30 * time.Second
	ingestTimeout = 90 * time.Second
	// inline text above this size is passed via a temp file to stay clear of argv limits
	inlineLimit = 500
)

// SecondMem provides Ask and Ingest operations via the secondmem CLI binary.
type SecondMem struct {
	Binary  string
	Enabled bool
}

func New(binary string, enabled bool) *SecondMem {
	if binary == "" {
		binary = "secondmem"
	}
	if enabled {
		if _, err := exec.LookPath(binary); err != nil {
			enabled = false // binary missing → behave as disabled, never fail a phase
		}
	}
	return &SecondMem{Binary: binary, Enabled: enabled}
}

// Ask queries the secondmem knowledge base and returns the answer.
// Returns empty string if secondmem is disabled or an error occurs (non-fatal).
func (s *SecondMem) Ask(ctx context.Context, question string) string {
	if s == nil || !s.Enabled || strings.TrimSpace(question) == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, askTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.Binary, "ask", question)
	cmd.Stdin = nil
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Ingest stores text into the secondmem knowledge base.
// Non-fatal for callers: they log the error and continue.
func (s *SecondMem) Ingest(ctx context.Context, text string) error {
	if s == nil || !s.Enabled || strings.TrimSpace(text) == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, ingestTimeout)
	defer cancel()
	if len(text) > inlineLimit {
		return s.ingestViaFile(ctx, text)
	}
	cmd := exec.CommandContext(ctx, s.Binary, "ingest", text)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("secondmem ingest: %w\n%s", err, out)
	}
	return nil
}

func (s *SecondMem) ingestViaFile(ctx context.Context, text string) error {
	tmp := filepath.Join(os.TempDir(), "aros-"+uuid.New().String()+".txt")
	if err := os.WriteFile(tmp, []byte(text), 0600); err != nil {
		return fmt.Errorf("writing temp file: %w", err)
	}
	defer os.Remove(tmp)

	cmd := exec.CommandContext(ctx, s.Binary, "ingest", tmp)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("secondmem ingest file: %w\n%s", err, out)
	}
	return nil
}
