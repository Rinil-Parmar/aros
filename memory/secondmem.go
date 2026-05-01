package memory

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
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
	return &SecondMem{Binary: binary, Enabled: enabled}
}

// Ask queries the secondmem knowledge base and returns the answer.
// Returns empty string if secondmem is disabled or an error occurs (non-fatal).
func (s *SecondMem) Ask(ctx context.Context, question string) string {
	if !s.Enabled {
		return ""
	}
	cmd := exec.CommandContext(ctx, s.Binary, "ask", question)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Ingest stores text into the secondmem knowledge base.
// Non-fatal: logs warning on error but does not return it.
func (s *SecondMem) Ingest(ctx context.Context, text string) error {
	if !s.Enabled {
		return nil
	}
	if len(text) > 500 {
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
