package state

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const agentSessionsFile = "agent-sessions.jsonl"

// AgentSession records one session a vendor CLI created while serving this Aros
// session. Every agent call creates a new vendor session, and the vendor keeps
// a transcript of it outside the project — so without this record those files
// are orphaned and untraceable when the Aros session is deleted.
type AgentSession struct {
	At      time.Time `json:"at"`
	Agent   string    `json:"agent"`    // "claude", "opencode", "copilot", …
	ID      string    `json:"id"`       // the vendor's session id
	WorkDir string    `json:"work_dir"` // where the call ran, for diagnostics
}

// agentSessionMu serialises appends: the work phase reports from several
// goroutines at once.
var agentSessionMu sync.Mutex

func sessionAgentsPath(arosDir, id string) string {
	return filepath.Join(sessionDir(arosDir, id), agentSessionsFile)
}

// RecordAgentSession appends one vendor session id to the active Aros session.
// Best-effort by design: failing to record is not worth interrupting a run for.
func RecordAgentSession(arosDir string, e AgentSession) error {
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

	agentSessionMu.Lock()
	defer agentSessionMu.Unlock()

	f, err := os.OpenFile(sessionAgentsPath(arosDir, id), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(data, '\n'))
	return err
}

// LoadAgentSessions returns the vendor sessions recorded for one Aros session,
// de-duplicated and in the order they were created. A missing file is not an
// error: a session that never ran an agent has none.
func LoadAgentSessions(arosDir, sessionID string) ([]AgentSession, error) {
	f, err := os.Open(sessionAgentsPath(arosDir, sessionID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	seen := make(map[string]bool)
	var out []AgentSession
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e AgentSession
		if err := json.Unmarshal(line, &e); err != nil {
			continue // skip a torn line rather than lose the rest
		}
		if e.ID == "" {
			continue
		}
		key := e.Agent + "\x00" + e.ID
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, e)
	}
	return out, nil
}
