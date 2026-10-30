package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// CleanupResult reports what removing an Aros session's vendor sessions achieved.
type CleanupResult struct {
	Removed  int      // vendor sessions actually deleted
	Freed    int64    // bytes reclaimed, where measurable
	Skipped  int      // sessions we have no safe way to delete
	Warnings []string // one line per thing a human should know
}

const cleanupTimeout = 20 * time.Second

// CleanupSession deletes one vendor session's stored data.
//
// Each agent needs a different mechanism, and only some are safe:
//
//   - opencode has `opencode session delete <id>` — its own command, so it
//     keeps its sqlite index consistent. Always preferred.
//   - claude stores a session as a single self-contained transcript at
//     ~/.claude/projects/<slug>/<id>.jsonl. Deleting that file is safe; there
//     is no separate index to corrupt. We glob for the id rather than
//     recomputing the path slug, so we never depend on how claude derives it.
//   - copilot's `sessions` subcommand offers only `import`, and its data lives
//     in a sqlite store. We remove ~/.copilot/session-state/<id>/ when present
//     and report that the database row remains, rather than writing into
//     another tool's database behind its back.
//
// Anything not recognised is skipped and reported, never guessed at.
func CleanupSession(ctx context.Context, s VendorSession) CleanupResult {
	switch s.Agent {
	case "claude":
		return cleanupClaude(s.ID)
	case "copilot":
		return cleanupCopilot(s.ID)
	default:
		// "opencode" and any opencode-backed variant (opencode-gemini, …).
		return cleanupOpenCode(ctx, s)
	}
}

// VendorSession is one vendor session to remove.
type VendorSession struct {
	Agent string
	ID    string
}

func cleanupClaude(id string) CleanupResult {
	var r CleanupResult
	home, err := os.UserHomeDir()
	if err != nil {
		r.Warnings = append(r.Warnings, "claude: cannot locate home directory: "+err.Error())
		r.Skipped++
		return r
	}
	// Glob by session id: slug-independent, and an id is random enough that a
	// match cannot belong to a different session.
	pattern := filepath.Join(home, ".claude", "projects", "*", id+".jsonl")
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		r.Skipped++
		return r
	}
	for _, path := range matches {
		var size int64
		if fi, err := os.Stat(path); err == nil {
			size = fi.Size()
		}
		if err := os.Remove(path); err != nil {
			r.Warnings = append(r.Warnings, "claude: "+err.Error())
			r.Skipped++
			continue
		}
		r.Removed++
		r.Freed += size
	}
	return r
}

func cleanupOpenCode(ctx context.Context, s VendorSession) CleanupResult {
	var r CleanupResult
	if _, err := exec.LookPath("opencode"); err != nil {
		r.Warnings = append(r.Warnings, "opencode not on PATH — session "+s.ID+" left in place")
		r.Skipped++
		return r
	}
	ctx, cancel := context.WithTimeout(ctx, cleanupTimeout)
	defer cancel()
	// Use opencode's own delete so its sqlite index stays consistent.
	out, err := exec.CommandContext(ctx, "opencode", "session", "delete", s.ID).CombinedOutput()
	if err != nil {
		r.Warnings = append(r.Warnings, fmt.Sprintf("opencode session delete %s: %v", s.ID, firstLine(out, err)))
		r.Skipped++
		return r
	}
	r.Removed++
	return r
}

func cleanupCopilot(id string) CleanupResult {
	var r CleanupResult
	home, err := os.UserHomeDir()
	if err != nil {
		r.Skipped++
		return r
	}
	dir := filepath.Join(home, ".copilot", "session-state", id)
	if _, err := os.Stat(dir); err != nil {
		r.Skipped++
		return r
	}
	size := dirSize(dir)
	if err := os.RemoveAll(dir); err != nil {
		r.Warnings = append(r.Warnings, "copilot: "+err.Error())
		r.Skipped++
		return r
	}
	r.Removed++
	r.Freed += size
	r.Warnings = append(r.Warnings,
		"copilot keeps session rows in ~/.copilot/session-store.db and offers no delete command; that row remains")
	return r
}

func dirSize(dir string) int64 {
	var total int64
	_ = filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}

// firstLine returns the first non-empty line of a command's output, falling
// back to the error when it printed nothing useful.
func firstLine(out []byte, fallback error) string {
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return fallback.Error()
}

// Merge folds another result into r.
func (r *CleanupResult) Merge(o CleanupResult) {
	r.Removed += o.Removed
	r.Freed += o.Freed
	r.Skipped += o.Skipped
	r.Warnings = append(r.Warnings, o.Warnings...)
}

// Summary renders a one-line report for the user.
func (r CleanupResult) Summary() string {
	s := fmt.Sprintf("%d agent session(s) removed", r.Removed)
	if r.Freed > 0 {
		s += fmt.Sprintf(", %s reclaimed", humanBytes(r.Freed))
	}
	if r.Skipped > 0 {
		s += fmt.Sprintf(", %d could not be removed", r.Skipped)
	}
	return s
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
