package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// chatDir sets up an .aros with one active session.
func chatDir(t *testing.T) string {
	t.Helper()
	arosDir := filepath.Join(t.TempDir(), ".aros")
	if err := os.MkdirAll(arosDir, 0755); err != nil {
		t.Fatal(err)
	}
	meta, _, err := CreateSession(arosDir, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if err := SetActiveSession(arosDir, meta.ID); err != nil {
		t.Fatal(err)
	}
	return arosDir
}

func TestChatRoundTrip(t *testing.T) {
	dir := chatDir(t)

	if got, err := LoadChat(dir, 0); err != nil || len(got) != 0 {
		t.Fatalf("a new session should have no chat: %v %v", got, err)
	}

	want := []ChatEntry{
		{Kind: ChatHuman, Body: "what should I build?"},
		{Kind: ChatAgent, Agent: "claude", Body: "line one"},
		{Kind: ChatAgent, Agent: "claude", Body: "line two"},
		{Kind: ChatSuccess, Body: "Plan approved."},
		{Kind: ChatError, Body: "task-002 blocked"},
	}
	for _, e := range want {
		if err := AppendChat(dir, e); err != nil {
			t.Fatal(err)
		}
	}

	got, err := LoadChat(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Kind != want[i].Kind || got[i].Body != want[i].Body || got[i].Agent != want[i].Agent {
			t.Errorf("entry %d: got %+v, want %+v", i, got[i], want[i])
		}
		if got[i].At.IsZero() {
			t.Errorf("entry %d has no timestamp", i)
		}
	}
}

// The log belongs to the session, so switching sessions must switch history.
func TestChatIsPerSession(t *testing.T) {
	dir := chatDir(t)
	if err := AppendChat(dir, ChatEntry{Kind: ChatHuman, Body: "first session"}); err != nil {
		t.Fatal(err)
	}

	second, _, err := CreateSession(dir, "other")
	if err != nil {
		t.Fatal(err)
	}
	if err := SetActiveSession(dir, second.ID); err != nil {
		t.Fatal(err)
	}

	got, err := LoadChat(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("the new session inherited %d message(s): %+v", len(got), got)
	}
	if err := AppendChat(dir, ChatEntry{Kind: ChatHuman, Body: "second session"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := LoadChat(dir, 0); len(got) != 1 || got[0].Body != "second session" {
		t.Fatalf("second session chat wrong: %+v", got)
	}
}

func TestChatReplayLimit(t *testing.T) {
	dir := chatDir(t)
	for i := 0; i < 20; i++ {
		if err := AppendChat(dir, ChatEntry{Kind: ChatSystem, Body: string(rune('a' + i%26))}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := LoadChat(dir, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 {
		t.Fatalf("limit ignored: got %d", len(got))
	}
	// The limit must keep the most recent entries, not the first five.
	if got[4].Body != string(rune('a'+19%26)) {
		t.Fatalf("limit kept the wrong end: %+v", got)
	}
}

func TestChatClear(t *testing.T) {
	dir := chatDir(t)
	if err := AppendChat(dir, ChatEntry{Kind: ChatHuman, Body: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := ClearChat(dir); err != nil {
		t.Fatal(err)
	}
	if got, _ := LoadChat(dir, 0); len(got) != 0 {
		t.Fatalf("clear left %d entries", len(got))
	}
	// Clearing twice must not error.
	if err := ClearChat(dir); err != nil {
		t.Fatalf("second clear errored: %v", err)
	}
}

// A long agent line must survive: bufio's default token limit would truncate
// the scan and silently drop the rest of the history.
func TestChatHandlesVeryLongLines(t *testing.T) {
	dir := chatDir(t)
	long := strings.Repeat("x", 200_000)
	if err := AppendChat(dir, ChatEntry{Kind: ChatAgent, Agent: "claude", Body: long}); err != nil {
		t.Fatal(err)
	}
	if err := AppendChat(dir, ChatEntry{Kind: ChatHuman, Body: "after"}); err != nil {
		t.Fatal(err)
	}
	got, err := LoadChat(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("long line broke the scan: got %d entries", len(got))
	}
	if len(got[0].Body) != len(long) || got[1].Body != "after" {
		t.Fatal("content lost around the long line")
	}
}

// A torn final line (a crash mid-write) must not lose the whole history.
func TestChatSurvivesTornLine(t *testing.T) {
	dir := chatDir(t)
	for _, b := range []string{"one", "two"} {
		if err := AppendChat(dir, ChatEntry{Kind: ChatHuman, Body: b}); err != nil {
			t.Fatal(err)
		}
	}
	id, err := ActiveSessionID(dir)
	if err != nil {
		t.Fatal(err)
	}
	path := sessionChatPath(dir, id)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"kind":"human","body":"tr`) // no newline, invalid JSON
	f.Close()

	got, err := LoadChat(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("torn line cost us history: got %d entries", len(got))
	}
}

func TestChatWithNoActiveSession(t *testing.T) {
	arosDir := filepath.Join(t.TempDir(), ".aros")
	if err := os.MkdirAll(arosDir, 0755); err != nil {
		t.Fatal(err)
	}
	// No session at all: loading is a no-op, not an error.
	if got, err := LoadChat(arosDir, 0); err != nil || got != nil {
		t.Fatalf("got %v, %v", got, err)
	}
	if err := ClearChat(arosDir); err != nil {
		t.Fatalf("clear errored with no session: %v", err)
	}
}
