package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/pelletier/go-toml/v2"
)

// Scope says which config file a change is written to.
type Scope string

const (
	// ScopeProject writes ./.aros/config.toml — the usual place for a model
	// choice, since it belongs to the project you are working on.
	ScopeProject Scope = "project"
	// ScopeGlobal writes ~/.aros/config.toml, affecting every project.
	ScopeGlobal Scope = "global"
)

var writeMu sync.Mutex

// ProjectPath is the project-level config for dir.
func ProjectPath(dir string) string {
	return filepath.Join(dir, ".aros", "config.toml")
}

// GlobalConfigPath is the user-level config.
func GlobalConfigPath() string {
	return filepath.Join(DefaultGlobalPath(), "config.toml")
}

// SetValue writes one dotted key into a TOML file, leaving every other key
// alone and creating the file if needed.
//
// It deliberately does not use viper's WriteConfigAs, which serialises the
// whole resolved configuration — defaults included. That pins today's defaults
// into the user's file forever and, in a project config, silently overrides
// their global settings for every key rather than the one they changed.
func SetValue(path, key string, value any) error {
	parts := strings.Split(key, ".")
	if len(parts) == 0 || key == "" {
		return fmt.Errorf("empty config key")
	}

	writeMu.Lock()
	defer writeMu.Unlock()

	tree := map[string]any{}
	header := ""
	if data, err := os.ReadFile(path); err == nil {
		if err := toml.Unmarshal(data, &tree); err != nil {
			return fmt.Errorf("parsing %s: %w", path, err)
		}
		// A TOML round-trip through a map drops comments. Keep the file's
		// leading comment block at least, since that is where the explanation
		// of the file lives.
		header = leadingComments(string(data))
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	// Walk to the parent table, creating tables as needed.
	cur := tree
	for _, p := range parts[:len(parts)-1] {
		next, ok := cur[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[p] = next
		}
		cur = next
	}
	cur[parts[len(parts)-1]] = value

	body, err := toml.Marshal(tree)
	if err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}
	data := body
	if header != "" {
		data = append([]byte(header), body...)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	// Write via a temp file so a crash cannot truncate the user's config.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	return nil
}

// leadingComments returns the run of comment and blank lines at the top of a
// TOML file, so rewriting the file does not throw away its explanation.
func leadingComments(content string) string {
	var kept []string
	for _, line := range strings.Split(content, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			kept = append(kept, line)
			continue
		}
		break
	}
	// Trim trailing blanks, then separate the header from the body with one.
	for len(kept) > 0 && strings.TrimSpace(kept[len(kept)-1]) == "" {
		kept = kept[:len(kept)-1]
	}
	if len(kept) == 0 {
		return ""
	}
	return strings.Join(kept, "\n") + "\n\n"
}

// SetAgentModel persists an agent's model to the chosen scope and returns the
// file it wrote, so the UI can say where the setting now lives.
func SetAgentModel(projectDir, agentName, model string, scope Scope) (string, error) {
	path := GlobalConfigPath()
	if scope == ScopeProject && projectDir != "" {
		path = ProjectPath(projectDir)
	}
	key := fmt.Sprintf("agents.%s.model", agentName)
	if err := SetValue(path, key, model); err != nil {
		return "", err
	}
	// An agent you have just chosen a model for is one you intend to use.
	if err := SetValue(path, fmt.Sprintf("agents.%s.enabled", agentName), true); err != nil {
		return path, err
	}
	return path, nil
}

// SetJudge persists the judge agent.
func SetJudge(projectDir, agentName string, scope Scope) (string, error) {
	path := GlobalConfigPath()
	if scope == ScopeProject && projectDir != "" {
		path = ProjectPath(projectDir)
	}
	return path, SetValue(path, "judge.agent", agentName)
}

// SetAgentDense persists an agent's dense level ("" turns it off).
func SetAgentDense(projectDir, agentName, level string, scope Scope) (string, error) {
	path := GlobalConfigPath()
	if scope == ScopeProject && projectDir != "" {
		path = ProjectPath(projectDir)
	}
	return path, SetValue(path, fmt.Sprintf("agents.%s.dense", agentName), level)
}
