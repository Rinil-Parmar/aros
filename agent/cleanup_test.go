package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A claude session is one self-contained transcript; cleanup must find it by id
// without depending on how claude derives its project-directory slug.
func TestCleanupClaudeFindsSessionByIDAnyProjectDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Two unrelated project dirs with different slug shapes.
	for _, slug := range []string{"-home-me-project", "-tmp-weird.path-with-dots"} {
		dir := filepath.Join(home, ".claude", "projects", slug)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "keep-me.jsonl"), []byte("other session\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(home, ".claude", "projects", "-tmp-weird.path-with-dots", "abc-123.jsonl")
	if err := os.WriteFile(target, []byte(strings.Repeat("x", 500)), 0644); err != nil {
		t.Fatal(err)
	}

	r := CleanupSession(context.Background(), VendorSession{Agent: "claude", ID: "abc-123"})
	if r.Removed != 1 {
		t.Fatalf("removed %d, want 1 (warnings: %v)", r.Removed, r.Warnings)
	}
	if r.Freed != 500 {
		t.Errorf("freed %d bytes, want 500", r.Freed)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Error("target transcript still present")
	}
	// Other sessions must be untouched.
	for _, slug := range []string{"-home-me-project", "-tmp-weird.path-with-dots"} {
		p := filepath.Join(home, ".claude", "projects", slug, "keep-me.jsonl")
		if _, err := os.Stat(p); err != nil {
			t.Errorf("unrelated session was deleted: %s", p)
		}
	}
}

// A session id we never recorded must be reported as skipped, never guessed at.
func TestCleanupClaudeMissingSessionIsSkipped(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	r := CleanupSession(context.Background(), VendorSession{Agent: "claude", ID: "not-there"})
	if r.Removed != 0 || r.Skipped != 1 {
		t.Fatalf("got %+v, want removed=0 skipped=1", r)
	}
}

// Copilot: the state directory goes, and the user is told the sqlite row stays.
func TestCleanupCopilotRemovesStateAndWarnsAboutTheDatabase(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".copilot", "session-state", "sess-9")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a"), []byte("1234567890"), 0644); err != nil {
		t.Fatal(err)
	}
	// A different session must survive.
	other := filepath.Join(home, ".copilot", "session-state", "sess-other")
	if err := os.MkdirAll(other, 0755); err != nil {
		t.Fatal(err)
	}

	r := CleanupSession(context.Background(), VendorSession{Agent: "copilot", ID: "sess-9"})
	if r.Removed != 1 || r.Freed != 10 {
		t.Fatalf("got %+v, want removed=1 freed=10", r)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("session-state dir still present")
	}
	if _, err := os.Stat(other); err != nil {
		t.Error("an unrelated copilot session was deleted")
	}
	if len(r.Warnings) == 0 || !strings.Contains(strings.ToLower(r.Warnings[0]), "session-store.db") {
		t.Errorf("the user must be told the database row remains: %v", r.Warnings)
	}
}

// opencode is deleted through its own command; without the binary we skip and say so.
func TestCleanupOpenCodeWithoutBinaryIsSkipped(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no opencode on PATH
	r := CleanupSession(context.Background(), VendorSession{Agent: "opencode", ID: "ses_1"})
	if r.Removed != 0 || r.Skipped != 1 {
		t.Fatalf("got %+v, want removed=0 skipped=1", r)
	}
	if len(r.Warnings) == 0 || !strings.Contains(r.Warnings[0], "opencode") {
		t.Errorf("no warning naming opencode: %v", r.Warnings)
	}
}

// An opencode-backed variant (opencode-gemini) routes through opencode too.
func TestCleanupUnknownAgentRoutesToOpenCode(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	r := CleanupSession(context.Background(), VendorSession{Agent: "opencode-gemini", ID: "ses_2"})
	if r.Skipped != 1 || len(r.Warnings) == 0 || !strings.Contains(r.Warnings[0], "opencode") {
		t.Fatalf("got %+v", r)
	}
}

func TestCleanupResultMergeAndSummary(t *testing.T) {
	var total CleanupResult
	total.Merge(CleanupResult{Removed: 2, Freed: 2048, Warnings: []string{"a"}})
	total.Merge(CleanupResult{Removed: 1, Skipped: 1, Freed: 1 << 21, Warnings: []string{"b"}})
	if total.Removed != 3 || total.Skipped != 1 || len(total.Warnings) != 2 {
		t.Fatalf("merge wrong: %+v", total)
	}
	s := total.Summary()
	for _, want := range []string{"3 agent session(s) removed", "MB", "1 could not be removed"} {
		if !strings.Contains(s, want) {
			t.Errorf("summary missing %q: %q", want, s)
		}
	}
}

// The sink is how adapters report without every call site threading a recorder.
func TestSessionSink(t *testing.T) {
	t.Cleanup(func() { SetSessionSink(nil) })

	type got struct{ name, id, dir string }
	var seen []got
	SetSessionSink(func(name, id, dir string) { seen = append(seen, got{name, id, dir}) })

	reportSession("claude", "s-1", "/work")
	reportSession("claude", "", "/work") // empty id must be ignored
	if len(seen) != 1 || seen[0] != (got{"claude", "s-1", "/work"}) {
		t.Fatalf("sink got %+v", seen)
	}

	SetSessionSink(nil)
	reportSession("claude", "s-2", "/work") // must not panic with no sink
	if len(seen) != 1 {
		t.Fatalf("sink fired after being cleared: %+v", seen)
	}
}
