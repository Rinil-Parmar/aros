package state

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	chatFile = "chat.jsonl"
	// MaxChatReplay bounds how many entries are replayed into the UI when a
	// session is reopened. Older entries stay in the file.
	MaxChatReplay = 500
	// maxChatBytes triggers compaction: the log is rewritten keeping its tail,
	// so a chatty project cannot grow the file without limit.
	maxChatBytes = 2 << 20 // 2 MiB
)

// Chat message kinds, kept as strings so the log stays readable and survives
// renumbering of the UI's internal kind constants.
const (
	ChatSystem  = "system"
	ChatAgent   = "agent"
	ChatHuman   = "human"
	ChatError   = "error"
	ChatSuccess = "success"
)

// ChatEntry is one line of the session's conversation log.
type ChatEntry struct {
	At    time.Time `json:"at"`
	Kind  string    `json:"kind"`
	Agent string    `json:"agent,omitempty"`
	Model string    `json:"model,omitempty"`
	Body  string    `json:"body"`
}

func sessionChatPath(arosDir, id string) string {
	return filepath.Join(sessionDir(arosDir, id), chatFile)
}

// AppendChat adds one entry to the active session's conversation log.
// Append-only: a streamed agent reply is stored line by line and merged back
// into one block on replay, so nothing has to be rewritten as output arrives.
func AppendChat(arosDir string, e ChatEntry) error {
	id, err := ensureActiveSessionID(arosDir)
	if err != nil {
		return err
	}
	if e.At.IsZero() {
		e.At = time.Now()
	}
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	path := sessionChatPath(arosDir, id)
	if fi, err := os.Stat(path); err == nil && fi.Size() > maxChatBytes {
		_ = compactChat(path)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

// LoadChat returns the active session's conversation, at most the last `limit`
// entries (0 or less means MaxChatReplay). A missing log is not an error: a
// session simply has no history yet.
func LoadChat(arosDir string, limit int) ([]ChatEntry, error) {
	if limit <= 0 {
		limit = MaxChatReplay
	}
	id, err := ensureActiveSessionID(arosDir)
	if err != nil {
		return nil, nil // no session yet
	}
	entries, err := readChatFile(sessionChatPath(arosDir, id))
	if err != nil {
		return nil, err
	}
	if len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	return entries, nil
}

// ClearChat empties the active session's conversation log.
func ClearChat(arosDir string) error {
	id, err := ensureActiveSessionID(arosDir)
	if err != nil {
		return nil
	}
	if err := os.Remove(sessionChatPath(arosDir, id)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func readChatFile(path string) ([]ChatEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var entries []ChatEntry
	sc := bufio.NewScanner(f)
	// Agent output lines can be long; the default 64KiB token limit would
	// silently stop the scan partway through the log.
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e ChatEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue // skip a torn line rather than lose the whole history
		}
		entries = append(entries, e)
	}
	if err := sc.Err(); err != nil {
		// Return what was read: a partial history beats none.
		return entries, nil
	}
	return entries, nil
}

// compactChat rewrites path keeping the most recent MaxChatReplay entries.
func compactChat(path string) error {
	entries, err := readChatFile(path)
	if err != nil {
		return err
	}
	if len(entries) > MaxChatReplay {
		entries = entries[len(entries)-MaxChatReplay:]
	}
	var b strings.Builder
	for _, e := range entries {
		data, err := json.Marshal(e)
		if err != nil {
			continue
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
