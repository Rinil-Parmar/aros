// Package git records what the agents actually changed.
//
// It deliberately has no Push, and never will: a local commit is private and
// undone with `git reset`, while a push is outward-facing, triggers CI and
// cannot be cleanly retracted. Publishing stays a decision the human makes by
// typing `git push` themselves.
package git

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const cmdTimeout = 30 * time.Second

// Repo is a git working tree Aros may commit to.
type Repo struct {
	dir string
}

// FileChange is one entry from `git status --porcelain`.
type FileChange struct {
	Status string // "M", "A", "D", "??" (untracked), "R" …
	Path   string
}

// Label renders the status as a word for the approval prompt.
func (f FileChange) Label() string {
	switch {
	case f.Status == "??":
		return "new"
	case strings.HasPrefix(f.Status, "D"):
		return "deleted"
	case strings.HasPrefix(f.Status, "R"):
		return "renamed"
	case strings.HasPrefix(f.Status, "A"):
		return "added"
	default:
		return "modified"
	}
}

// Open returns the repo for dir, or ok=false when git is unavailable or dir is
// not a working tree. Callers skip the git layer in that case rather than fail:
// not every project is a repository.
func Open(dir string) (repo *Repo, ok bool) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, false
	}
	r := &Repo{dir: dir}
	out, err := r.run("rev-parse", "--is-inside-work-tree")
	if err != nil || strings.TrimSpace(out) != "true" {
		return nil, false
	}
	// Make git ignore Aros's own state directory immediately. Best-effort
	// here: if it fails the repo is still usable, and CommitAll refuses to
	// commit without it. Doing it at open time keeps .aros/ out of every
	// status listing, so the user is never shown session files and asked
	// whether to commit them.
	_ = r.ExcludeArosDir()
	return r, true
}

func (r *Repo) run(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()
	full := append([]string{"-C", r.dir}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	var out, errBuf strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			msg = err.Error()
		}
		return out.String(), fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return out.String(), nil
}

// ExcludeArosDir makes git ignore Aros's own state directory, via
// .git/info/exclude so the user's committed .gitignore is left alone.
// Without it, `git add -A` would commit session state into their project.
func (r *Repo) ExcludeArosDir() error {
	gitDir, err := r.run("rev-parse", "--git-dir")
	if err != nil {
		return err
	}
	dir := strings.TrimSpace(gitDir)
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(r.dir, dir)
	}
	path := filepath.Join(dir, "info", "exclude")
	if data, err := os.ReadFile(path); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(line) == ".aros/" {
				return nil // already excluded
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString("\n# added by aros — session state is local, not project history\n.aros/\n")
	return err
}

// ChangedFiles lists every uncommitted change, including untracked files.
func (r *Repo) ChangedFiles() ([]FileChange, error) {
	out, err := r.run("status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	var changes []FileChange
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if len(line) < 4 {
			continue
		}
		status := strings.TrimSpace(line[:2])
		path := strings.TrimSpace(line[3:])
		// Renames read "old -> new"; keep the new path.
		if i := strings.Index(path, " -> "); i != -1 {
			path = path[i+4:]
		}
		changes = append(changes, FileChange{Status: status, Path: strings.Trim(path, `"`)})
	}
	return changes, sc.Err()
}

// IsClean reports whether the working tree has no uncommitted changes.
func (r *Repo) IsClean() (bool, error) {
	changes, err := r.ChangedFiles()
	if err != nil {
		return false, err
	}
	return len(changes) == 0, nil
}

// Head returns the current commit's short SHA, or "" in a repo with no commits.
func (r *Repo) Head() string {
	out, err := r.run("rev-parse", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// Branch returns the current branch name, or "" when detached.
func (r *Repo) Branch() string {
	out, err := r.run("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return ""
	}
	if b := strings.TrimSpace(out); b != "HEAD" {
		return b
	}
	return ""
}

// CommitAll stages every change (except Aros's own state) and commits it.
// The commit author is whatever the user's git config says — Aros never
// overrides it and adds no trailers.
// Returns the new short SHA. If there is nothing to commit it returns
// ErrNothingToCommit, which callers treat as "the agents changed nothing".
func (r *Repo) CommitAll(message string) (string, error) {
	changes, err := r.ChangedFiles()
	if err != nil {
		return "", err
	}
	if len(changes) == 0 {
		return "", ErrNothingToCommit
	}
	// Guarantee .aros is ignored before staging anything. If this fails we do
	// not commit: quietly writing session state into someone's project history
	// is worse than refusing. (Excluding it via a negative pathspec on `add`
	// does not work — git then complains that the path is ignored.)
	if err := r.ExcludeArosDir(); err != nil {
		return "", fmt.Errorf("excluding .aros from the repo: %w", err)
	}
	if _, err := r.run("add", "-A"); err != nil {
		return "", err
	}
	// Staging may have added nothing if every change was ignored.
	if out, err := r.run("diff", "--cached", "--name-only"); err == nil && strings.TrimSpace(out) == "" {
		return "", ErrNothingToCommit
	}
	if _, err := r.run("commit", "-m", message); err != nil {
		return "", err
	}
	return r.Head(), nil
}

// ErrNothingToCommit means the working tree held no committable change.
var ErrNothingToCommit = fmt.Errorf("nothing to commit")

// Summarize renders a change list for an approval prompt: a count line plus up
// to maxLines paths.
func Summarize(changes []FileChange, maxLines int) []string {
	if len(changes) == 0 {
		return []string{"no files changed"}
	}
	lines := []string{fmt.Sprintf("%d file(s) changed:", len(changes))}
	for i, c := range changes {
		if i >= maxLines {
			lines = append(lines, fmt.Sprintf("  … and %d more", len(changes)-maxLines))
			break
		}
		lines = append(lines, fmt.Sprintf("  %-8s %s", c.Label(), c.Path))
	}
	return lines
}
