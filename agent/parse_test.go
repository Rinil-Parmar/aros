package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestParseClaudeJSON(t *testing.T) {
	ok := `{"type":"result","subtype":"success","is_error":false,"result":"pong","duration_ms":1599}`
	got, err := parseClaudeJSON(ok)
	if err != nil || got != "pong" {
		t.Fatalf("got %q, %v", got, err)
	}

	// is_error must surface as an error, never as a successful result.
	bad := `{"type":"result","subtype":"error_during_execution","is_error":true,"result":"Invalid API key"}`
	got, err = parseClaudeJSON(bad)
	if err == nil || !strings.Contains(err.Error(), "Invalid API key") {
		t.Fatalf("is_error not surfaced: got %q, err=%v", got, err)
	}
	if errors.Is(err, errNoResult) {
		t.Fatal("is_error must not be classified as parse failure")
	}

	// Multiple lines: last result wins, non-JSON lines ignored.
	multi := "some log line\n" + `{"type":"system"}` + "\n" + ok
	if got, err := parseClaudeJSON(multi); err != nil || got != "pong" {
		t.Fatalf("multi-line: got %q, %v", got, err)
	}

	if _, err := parseClaudeJSON("plain text output"); !errors.Is(err, errNoResult) {
		t.Fatalf("expected errNoResult, got %v", err)
	}
}

func TestParseOpenCodeEvents(t *testing.T) {
	stream := `{"type":"step_start","timestamp":1}
{"type":"text","part":{"type":"text","text":"Hello"}}
{"type":"text","part":{"type":"text","text":"World"}}
{"type":"step_finish"}`
	got, err := parseOpenCodeEvents(stream)
	if err != nil || got != "Hello\nWorld" {
		t.Fatalf("got %q, %v", got, err)
	}

	errStream := `{"type":"error","timestamp":1789618900925,"sessionID":"ses_x","error":{"name":"UnknownError","data":{"message":"Model not found: opencode/gpt-5-nano."}}}`
	if _, err := parseOpenCodeEvents(errStream); err == nil || !strings.Contains(err.Error(), "Model not found") {
		t.Fatalf("provider error not surfaced: %v", err)
	}

	if _, err := parseOpenCodeEvents(""); err == nil {
		t.Fatal("empty stream should be an error, not silent empty output")
	}
}

func TestParseCopilotJSON(t *testing.T) {
	stream := `{"type":"session.tools_updated","data":{"model":"x"}}
{"type":"assistant.message_delta","data":{"deltaContent":"po"}}
{"type":"assistant.message","data":{"messageId":"m1","content":"pong","toolRequests":[]}}
{"type":"result","exitCode":0}`
	got, err := parseCopilotJSON(stream)
	if err != nil || got != "pong" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := parseCopilotJSON(`{"type":"result","exitCode":0}`); err == nil {
		t.Fatal("missing assistant.message should error")
	}
}

// TestRunCommandKillsProcessGroup verifies a timed-out command takes its
// children with it instead of leaving them orphaned.
func TestRunCommandKillsProcessGroup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	// The parent shell spawns a child sleep; without process-group kill the
	// pipe stays open and Run would block for the full sleep.
	_, _, _, err := runCommand(ctx, "sh", []string{"-c", "sleep 30 & wait"}, "", nil)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 8*time.Second {
		t.Fatalf("command did not terminate promptly: %v", elapsed)
	}
}

func TestRunCommandStdin(t *testing.T) {
	out, _, code, err := runCommand(context.Background(), "cat", nil, "", strings.NewReader("via stdin"))
	if err != nil || code != 0 || out != "via stdin" {
		t.Fatalf("stdin not forwarded: out=%q code=%d err=%v", out, code, err)
	}
}
