package git

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newRepo makes a real git repo in a temp dir with a deterministic identity.
func newRepo(t *testing.T) (*Repo, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.name", "Test User"},
		{"config", "user.email", "test@example.com"},
		{"config", "commit.gpgsign", "false"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	r, ok := Open(dir)
	if !ok {
		t.Fatal("Open said this is not a repo")
	}
	return r, dir
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestOpenRejectsNonRepo(t *testing.T) {
	if _, ok := Open(t.TempDir()); ok {
		t.Fatal("a plain directory must not be treated as a repo")
	}
}

func TestCleanDirtyAndChangedFiles(t *testing.T) {
	r, dir := newRepo(t)

	clean, err := r.IsClean()
	if err != nil || !clean {
		t.Fatalf("fresh repo should be clean: clean=%v err=%v", clean, err)
	}

	write(t, dir, "hello.txt", "hello\n")
	write(t, dir, "pkg/deep.go", "package pkg\n")
	clean, err = r.IsClean()
	if err != nil || clean {
		t.Fatalf("repo with new files should be dirty: clean=%v err=%v", clean, err)
	}

	changes, err := r.ChangedFiles()
	if err != nil {
		t.Fatal(err)
	}
	// Untracked files must be listed individually, not as a bare directory.
	paths := map[string]string{}
	for _, c := range changes {
		paths[c.Path] = c.Label()
	}
	if paths["hello.txt"] != "new" {
		t.Errorf("hello.txt not reported as new: %+v", changes)
	}
	if paths["pkg/deep.go"] != "new" {
		t.Errorf("nested untracked file not listed individually: %+v", changes)
	}
}

func TestCommitAllUsesRepoIdentityAndNoTrailers(t *testing.T) {
	r, dir := newRepo(t)
	write(t, dir, "a.txt", "one\n")

	sha, err := r.CommitAll("aros: first task")
	if err != nil {
		t.Fatal(err)
	}
	if sha == "" {
		t.Fatal("no sha returned")
	}
	if clean, _ := r.IsClean(); !clean {
		t.Error("tree should be clean after CommitAll")
	}

	out, err := exec.Command("git", "-C", dir, "log", "-1", "--format=%an|%ae|%s|%b").Output()
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.HasPrefix(got, "Test User|test@example.com|aros: first task|") {
		t.Fatalf("commit author/subject wrong: %q", got)
	}
	// The user's history must not be annotated with anything but their message.
	if strings.Contains(strings.ToLower(got), "co-authored") {
		t.Fatalf("commit carries a Co-Authored-By trailer: %q", got)
	}
}

func TestCommitAllOnEmptyTree(t *testing.T) {
	r, _ := newRepo(t)
	if _, err := r.CommitAll("nothing"); !errors.Is(err, ErrNothingToCommit) {
		t.Fatalf("expected ErrNothingToCommit, got %v", err)
	}
}

// Aros's own session directory must never land in the user's project history.
func TestArosDirIsNeverCommitted(t *testing.T) {
	r, dir := newRepo(t)
	// Note: no explicit ExcludeArosDir call — CommitAll must do it itself.
	write(t, dir, ".aros/sessions/x/state.json", `{"phase":"work"}`)
	write(t, dir, "real.txt", "work output\n")

	if _, err := r.CommitAll("aros: run"); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("git", "-C", dir, "ls-files").Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), ".aros") {
		t.Fatalf(".aros was committed into the project:\n%s", out)
	}
	if !strings.Contains(string(out), "real.txt") {
		t.Fatalf("real work was not committed:\n%s", out)
	}

	// And .aros must not show up as a pending change afterwards either.
	changes, err := r.ChangedFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range changes {
		if strings.HasPrefix(c.Path, ".aros") {
			t.Fatalf(".aros still reported as a change: %+v", changes)
		}
	}
}

// ExcludeArosDir must be idempotent — the work phase may call it every run.
func TestExcludeArosDirIsIdempotent(t *testing.T) {
	r, dir := newRepo(t)
	for i := 0; i < 3; i++ {
		if err := r.ExcludeArosDir(); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, ".git", "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), ".aros/"); n != 1 {
		t.Fatalf("exclude entry written %d times, want 1", n)
	}
}

func TestHeadAndBranch(t *testing.T) {
	r, dir := newRepo(t)
	if h := r.Head(); h != "" {
		t.Errorf("repo with no commits should have empty Head, got %q", h)
	}
	write(t, dir, "a.txt", "x\n")
	if _, err := r.CommitAll("first"); err != nil {
		t.Fatal(err)
	}
	if r.Head() == "" {
		t.Error("Head empty after a commit")
	}
	if b := r.Branch(); b == "" {
		t.Error("Branch empty on a normal checkout")
	}
}

func TestSummarize(t *testing.T) {
	changes := []FileChange{
		{Status: "M", Path: "a.go"},
		{Status: "??", Path: "b.go"},
		{Status: "D", Path: "c.go"},
	}
	lines := Summarize(changes, 2)
	if !strings.Contains(lines[0], "3 file(s)") {
		t.Fatalf("count line wrong: %q", lines[0])
	}
	if !strings.Contains(lines[2], "new") {
		t.Fatalf("untracked not labelled: %q", lines[2])
	}
	if !strings.Contains(lines[len(lines)-1], "1 more") {
		t.Fatalf("overflow line missing: %v", lines)
	}
	if got := Summarize(nil, 5); got[0] != "no files changed" {
		t.Fatalf("empty summary wrong: %v", got)
	}
}
