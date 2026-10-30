package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A write must touch only the key it was given. The old implementation
// serialised the whole resolved config, baking every current default into the
// user's file — and in a project config that silently overrode their globals
// for every key, not just the one they changed.
func TestSetValueTouchesOnlyOneKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	existing := `# my notes
[judge]
agent = "claude"

[agents.claude]
model = "haiku"
dense = "full"

[work]
max_concurrent = 3
`
	if err := os.WriteFile(path, []byte(existing), 0644); err != nil {
		t.Fatal(err)
	}

	if err := SetValue(path, "agents.claude.model", "sonnet"); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Agents["claude"].Model; got != "sonnet" {
		t.Errorf("model is %q, want sonnet", got)
	}
	// Everything else in the file survives.
	if got := cfg.Agents["claude"].Dense; got != "full" {
		t.Errorf("dense lost: %q", got)
	}
	if cfg.Work.MaxConcurrent != 3 {
		t.Errorf("max_concurrent lost: %d", cfg.Work.MaxConcurrent)
	}
	if cfg.Judge.Agent != "claude" {
		t.Errorf("judge lost: %q", cfg.Judge.Agent)
	}

	// And the file must not have grown a copy of every default.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, unwanted := range []string{"agent_timeout_seconds", "secondmem", "strengths", "copilot"} {
		if strings.Contains(string(data), unwanted) {
			t.Errorf("writing one key pulled in %q:\n%s", unwanted, data)
		}
	}
}

// Rewriting a config must keep its leading comment block: that is where the
// file explains itself, and silently eating it is a bad surprise.
func TestSetValueKeepsTheCommentHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := `# Aros project config — overrides ~/.aros/config.toml
# Uncomment and edit to customise.

[agents.claude]
model = "haiku"
`
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	if err := SetValue(path, "agents.claude.model", "sonnet"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.HasPrefix(got, "# Aros project config") {
		t.Errorf("comment header lost:\n%s", got)
	}
	if !strings.Contains(got, "Uncomment and edit") {
		t.Errorf("second comment line lost:\n%s", got)
	}
	if !strings.Contains(got, "sonnet") {
		t.Errorf("the change itself is missing:\n%s", got)
	}
	// And it must still parse.
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("file no longer parses: %v\n%s", err, got)
	}
	if cfg.Agents["claude"].Model != "sonnet" {
		t.Errorf("model is %q", cfg.Agents["claude"].Model)
	}
}

func TestSetValueCreatesFileAndTables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.toml")
	if err := SetValue(path, "agents.opencode.model", "openai/gpt-5.4"); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Agents["opencode"].Model; got != "openai/gpt-5.4" {
		t.Fatalf("model is %q", got)
	}
}

func TestSetValueTypes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := SetValue(path, "work.max_concurrent", 4); err != nil {
		t.Fatal(err)
	}
	if err := SetValue(path, "agents.claude.dangerously_skip_perms", true); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Work.MaxConcurrent != 4 {
		t.Errorf("int not stored as int: %d", cfg.Work.MaxConcurrent)
	}
	if !cfg.Agents["claude"].DangerouslySkipPerms {
		t.Error("bool not stored")
	}

	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), `max_concurrent = '4'`) {
		t.Errorf("int was written as a string:\n%s", data)
	}
}

func TestSetValueRejectsEmptyKey(t *testing.T) {
	if err := SetValue(filepath.Join(t.TempDir(), "c.toml"), "", "x"); err == nil {
		t.Fatal("expected an error for an empty key")
	}
}

// A model choice goes to the project by default, and enables that agent: you
// do not pick a model for an agent you mean to leave off.
func TestSetAgentModelScopes(t *testing.T) {
	proj := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	path, err := SetAgentModel(proj, "claude", "sonnet", ScopeProject)
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(proj, ".aros", "config.toml") {
		t.Fatalf("project scope wrote to %s", path)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agents["claude"].Model != "sonnet" || !cfg.Agents["claude"].Enabled {
		t.Errorf("model/enabled wrong: %+v", cfg.Agents["claude"])
	}

	gpath, err := SetAgentModel(proj, "claude", "opus", ScopeGlobal)
	if err != nil {
		t.Fatal(err)
	}
	if gpath != filepath.Join(home, ".aros", "config.toml") {
		t.Fatalf("global scope wrote to %s", gpath)
	}
	// The project file must be untouched by a global write.
	if cfg, _ := Load(path); cfg.Agents["claude"].Model != "sonnet" {
		t.Error("a global write changed the project config")
	}
}

func TestSetJudgeAndDense(t *testing.T) {
	proj := t.TempDir()
	t.Setenv("HOME", t.TempDir())

	if _, err := SetJudge(proj, "opencode", ScopeProject); err != nil {
		t.Fatal(err)
	}
	if _, err := SetAgentDense(proj, "claude", "ultra", ScopeProject); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(ProjectPath(proj))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Judge.Agent != "opencode" {
		t.Errorf("judge is %q", cfg.Judge.Agent)
	}
	if cfg.Agents["claude"].Dense != "ultra" {
		t.Errorf("dense is %q", cfg.Agents["claude"].Dense)
	}

	// "off" is stored as the empty string, which normalize() treats as off.
	if _, err := SetAgentDense(proj, "claude", "", ScopeProject); err != nil {
		t.Fatal(err)
	}
	cfg, _ = Load(ProjectPath(proj))
	if cfg.Agents["claude"].Dense != "" {
		t.Errorf("dense off not stored: %q", cfg.Agents["claude"].Dense)
	}
}
