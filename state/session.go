package state

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	sessionsDirName   = "sessions"
	sessionMetaFile   = "session.json"
	activeSessionFile = "active-session.json"
)

type SessionMeta struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type activeSession struct {
	ID string `json:"active_session_id"`
}

func sessionsDir(arosDir string) string {
	return filepath.Join(arosDir, sessionsDirName)
}

func sessionDir(arosDir, id string) string {
	return filepath.Join(sessionsDir(arosDir), id)
}

func sessionMetaPath(arosDir, id string) string {
	return filepath.Join(sessionDir(arosDir, id), sessionMetaFile)
}

func sessionStatePath(arosDir, id string) string {
	return filepath.Join(sessionDir(arosDir, id), stateFile)
}

func sessionManifestPath(arosDir, id string) string {
	return filepath.Join(sessionDir(arosDir, id), manifestFile)
}

func activeSessionPath(arosDir string) string {
	return filepath.Join(arosDir, activeSessionFile)
}

func ActiveSessionID(arosDir string) (string, error) {
	data, err := os.ReadFile(activeSessionPath(arosDir))
	if err != nil {
		return "", err
	}
	var a activeSession
	if err := json.Unmarshal(data, &a); err != nil {
		return "", fmt.Errorf("parsing active session: %w", err)
	}
	if a.ID == "" {
		return "", fmt.Errorf("active session not set")
	}
	return a.ID, nil
}

func ActiveSession(arosDir string) (*SessionMeta, error) {
	id, err := ActiveSessionID(arosDir)
	if err != nil {
		return nil, err
	}
	return LoadSessionMeta(arosDir, id)
}

func SetActiveSession(arosDir, id string) error {
	if _, err := os.Stat(sessionDir(arosDir, id)); err != nil {
		return fmt.Errorf("session %q not found", id)
	}
	return writeJSON(activeSessionPath(arosDir), activeSession{ID: id})
}

func ClearActiveSession(arosDir string) error {
	if err := os.Remove(activeSessionPath(arosDir)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func CreateSession(arosDir, name string) (*SessionMeta, *ProjectState, error) {
	id := newSessionID()
	now := time.Now()
	dir := sessionDir(arosDir, id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, nil, fmt.Errorf("creating session directory: %w", err)
	}
	meta := &SessionMeta{
		ID:        id,
		Name:      name,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := writeJSON(sessionMetaPath(arosDir, id), meta); err != nil {
		return nil, nil, fmt.Errorf("writing session meta: %w", err)
	}
	s := &ProjectState{
		ProjectName: name,
		Phase:       PhaseInit,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := writeJSON(sessionStatePath(arosDir, id), s); err != nil {
		return nil, nil, fmt.Errorf("writing session state: %w", err)
	}
	return meta, s, nil
}

func LoadSessionMeta(arosDir, id string) (*SessionMeta, error) {
	data, err := os.ReadFile(sessionMetaPath(arosDir, id))
	if err == nil {
		var meta SessionMeta
		if err := json.Unmarshal(data, &meta); err != nil {
			return nil, fmt.Errorf("parsing session meta: %w", err)
		}
		return &meta, nil
	}
	// fallback: derive from state.json if session meta missing
	statePath := sessionStatePath(arosDir, id)
	stateData, err2 := os.ReadFile(statePath)
	if err2 != nil {
		return nil, fmt.Errorf("reading session meta: %w", err)
	}
	var s ProjectState
	if err := json.Unmarshal(stateData, &s); err != nil {
		return nil, fmt.Errorf("parsing session state: %w", err)
	}
	return &SessionMeta{
		ID:        id,
		Name:      s.ProjectName,
		CreatedAt: s.CreatedAt,
		UpdatedAt: s.UpdatedAt,
	}, nil
}

func ListSessions(arosDir string) ([]SessionMeta, error) {
	entries, err := os.ReadDir(sessionsDir(arosDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading sessions dir: %w", err)
	}
	sessions := make([]SessionMeta, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		meta, err := LoadSessionMeta(arosDir, e.Name())
		if err != nil {
			continue
		}
		sessions = append(sessions, *meta)
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
	return sessions, nil
}

func RemoveSession(arosDir, id string) error {
	if err := os.RemoveAll(sessionDir(arosDir, id)); err != nil {
		return fmt.Errorf("removing session: %w", err)
	}
	return nil
}

func updateSessionMeta(arosDir, id, name string) error {
	meta, err := LoadSessionMeta(arosDir, id)
	if err != nil {
		return nil
	}
	if name != "" {
		meta.Name = name
	}
	meta.UpdatedAt = time.Now()
	return writeJSON(sessionMetaPath(arosDir, id), meta)
}

func ensureActiveSessionID(arosDir string) (string, error) {
	if id, err := ActiveSessionID(arosDir); err == nil {
		return id, nil
	}

	if legacyStateExists(arosDir) {
		id, err := migrateLegacySession(arosDir)
		if err == nil {
			return id, nil
		}
	}

	sessions, err := ListSessions(arosDir)
	if err != nil {
		return "", err
	}
	if len(sessions) > 0 {
		_ = SetActiveSession(arosDir, sessions[0].ID)
		return sessions[0].ID, nil
	}
	return "", fmt.Errorf("no active session")
}

func legacyStateExists(arosDir string) bool {
	_, err := os.Stat(filepath.Join(arosDir, stateFile))
	return err == nil
}

func migrateLegacySession(arosDir string) (string, error) {
	legacyStatePath := filepath.Join(arosDir, stateFile)
	data, err := os.ReadFile(legacyStatePath)
	if err != nil {
		return "", err
	}
	var s ProjectState
	if err := json.Unmarshal(data, &s); err != nil {
		return "", fmt.Errorf("parsing legacy state: %w", err)
	}
	meta, _, err := CreateSession(arosDir, s.ProjectName)
	if err != nil {
		return "", err
	}
	if err := writeJSON(sessionStatePath(arosDir, meta.ID), &s); err != nil {
		return "", err
	}

	legacyManifestPath := filepath.Join(arosDir, manifestFile)
	if _, err := os.Stat(legacyManifestPath); err == nil {
		_ = os.Rename(legacyManifestPath, sessionManifestPath(arosDir, meta.ID))
	}

	_ = os.Remove(legacyStatePath)
	_ = SetActiveSession(arosDir, meta.ID)
	return meta.ID, nil
}

func newSessionID() string {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("sess-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("sess-%d-%s", time.Now().UnixNano(), hex.EncodeToString(buf))
}
