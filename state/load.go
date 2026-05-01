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
	data, err := os.ReadFile(stateFilePath(arosDir))
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
	return writeJSON(stateFilePath(arosDir), s)
}

func LoadManifest(arosDir string) (*TaskManifest, error) {
	data, err := os.ReadFile(manifestFilePath(arosDir))
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
	return writeJSON(manifestFilePath(arosDir), m)
}

// writeJSON writes v as JSON to path using a write-then-rename for atomicity.
func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling JSON: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("writing temp file: %w", err)
	}
	return os.Rename(tmp, path)
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
