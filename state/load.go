package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	stateFile    = "state.json"
	manifestFile = "manifest.json"
)

func stateFilePath(arosDir string) string    { return filepath.Join(arosDir, stateFile) }
func manifestFilePath(arosDir string) string { return filepath.Join(arosDir, manifestFile) }

func LoadState(arosDir string) (*ProjectState, error) {
	id, err := ensureActiveSessionID(arosDir)
	if err != nil {
		return nil, fmt.Errorf("reading state: %w", err)
	}
	data, err := os.ReadFile(sessionStatePath(arosDir, id))
	if err != nil {
		return nil, fmt.Errorf("reading state: %w", err)
	}
	var s ProjectState
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parsing state: %w", err)
	}
	return &s, nil
}

func SaveState(arosDir string, s *ProjectState) error {
	s.UpdatedAt = time.Now()
	id, err := ensureActiveSessionID(arosDir)
	if err != nil {
		return err
	}
	if err := writeJSON(sessionStatePath(arosDir, id), s); err != nil {
		return err
	}
	return updateSessionMeta(arosDir, id, s.ProjectName)
}

func LoadManifest(arosDir string) (*TaskManifest, error) {
	id, err := ensureActiveSessionID(arosDir)
	if err != nil {
		return nil, fmt.Errorf("reading manifest: %w", err)
	}
	data, err := os.ReadFile(sessionManifestPath(arosDir, id))
	if err != nil {
		return nil, fmt.Errorf("reading manifest: %w", err)
	}
	var m TaskManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing manifest: %w", err)
	}
	return &m, nil
}

func SaveManifest(arosDir string, m *TaskManifest) error {
	id, err := ensureActiveSessionID(arosDir)
	if err != nil {
		return err
	}
	if err := writeJSON(sessionManifestPath(arosDir, id), m); err != nil {
		return err
	}
	return updateSessionMeta(arosDir, id, "")
}

// writeJSON writes v as JSON to path using a unique temp file + rename for atomicity.
// Each call gets its own temp file so concurrent writes to different paths never collide.
func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling JSON: %w", err)
	}
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".aros-write-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpName := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmpName)
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("closing temp file: %w", err)
	}
	return os.Rename(tmpName, path)
}

// ArosDir returns the .aros directory for cwd.
func ArosDir() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(cwd, ".aros"), nil
}

// RequirePhase checks that the current phase is one of the allowed phases.
// Pass force=true to skip the check (--force flag).
func RequirePhase(s *ProjectState, force bool, allowed ...Phase) error {
	if force {
		return nil
	}
	for _, p := range allowed {
		if s.Phase == p {
			return nil
		}
	}
	return fmt.Errorf("project is in phase %q — run the preceding command first", s.Phase)
}
