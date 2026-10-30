package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// listerAgent reports a fixed model list.
type listerAgent struct {
	name   string
	models []string
	err    error
	calls  int
}

func (l *listerAgent) Name() string { return l.name }
func (l *listerAgent) Run(ctx context.Context, p string) (AgentResult, error) {
	return AgentResult{}, nil
}
func (l *listerAgent) ListModels(ctx context.Context) ([]string, error) {
	l.calls++
	return l.models, l.err
}

// An agent that cannot enumerate must say so rather than present a guess as fact.
func TestModelsFallsBackToKnownAndNeverClaimsCompleteness(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	plain := &runOnlyAgent{}
	l := Models(context.Background(), plain, false)
	if l.Source != SourceKnown {
		t.Fatalf("source %q, want known", l.Source)
	}
	if l.Note == "" {
		t.Error("a non-live list must explain itself")
	}
	// A known list makes no completeness claim, so Has must not gate on it —
	// otherwise a valid model the list omits would be rejected.
	if l.Has("anything") {
		t.Error("a known list must not claim to contain a model")
	}
}

func TestModelsLiveThenCached(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	a := &listerAgent{name: "opencode", models: []string{"openai/gpt-5.4", "anthropic/claude-sonnet-4-5"}}

	live := Models(context.Background(), a, false)
	if live.Source != SourceLive || len(live.Models) != 2 {
		t.Fatalf("first call: %+v", live)
	}
	if a.calls != 1 {
		t.Fatalf("expected 1 query, got %d", a.calls)
	}
	if _, err := os.Stat(filepath.Join(home, ".aros", "models-cache.json")); err != nil {
		t.Fatalf("cache not written: %v", err)
	}

	// Second call must come from cache: the real query takes seconds.
	cached := Models(context.Background(), a, false)
	if cached.Source != SourceCached || len(cached.Models) != 2 {
		t.Fatalf("second call: %+v", cached)
	}
	if a.calls != 1 {
		t.Fatalf("cache was not used: %d queries", a.calls)
	}

	// refresh must bypass it.
	if r := Models(context.Background(), a, true); r.Source != SourceLive || a.calls != 2 {
		t.Fatalf("refresh did not re-query: %+v calls=%d", r, a.calls)
	}
}

// A failed query must not discard a list we already had.
func TestModelsKeepsCacheWhenQueryFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := &listerAgent{name: "opencode", models: []string{"openai/gpt-5.4"}}
	if l := Models(context.Background(), a, false); l.Source != SourceLive {
		t.Fatal("setup: expected a live list")
	}

	a.err = os.ErrNotExist
	a.models = nil
	l := Models(context.Background(), a, true) // refresh, but it fails
	if len(l.Models) != 1 || l.Models[0] != "openai/gpt-5.4" {
		t.Fatalf("cache lost on failure: %+v", l)
	}
	if !strings.Contains(l.Note, "last known") {
		t.Errorf("the user should be told it is stale: %q", l.Note)
	}
}

func TestModelCacheExpires(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	a := &listerAgent{name: "opencode", models: []string{"m1"}}
	Models(context.Background(), a, false)

	// Backdate the cache past its TTL.
	path := filepath.Join(home, ".aros", "models-cache.json")
	stale := time.Now().Add(-modelCacheTTL - time.Hour).Format(time.RFC3339Nano)
	body := `{"agents":{"opencode":{"models":["m1"],"fetched_at":"` + stale + `"}}}`
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	if l := Models(context.Background(), a, false); l.Source != SourceLive {
		t.Fatalf("expired cache was used: %+v", l)
	}
}

func TestModelListFilter(t *testing.T) {
	l := ModelList{Source: SourceLive, Models: []string{
		"openai/gpt-5.4", "openai/gpt-5.4-mini", "anthropic/claude-sonnet-4-5", "google/gemini-3-flash",
	}}

	if got := l.Filter(""); len(got) != 4 {
		t.Fatalf("empty filter should return all: %v", got)
	}
	got := l.Filter("gpt")
	if len(got) != 2 {
		t.Fatalf("gpt filter: %v", got)
	}
	// An exact match must come first so typing a full name always lands on it.
	exact := l.Filter("openai/gpt-5.4")
	if exact[0] != "openai/gpt-5.4" {
		t.Fatalf("exact match not first: %v", exact)
	}
	if got := l.Filter("SONNET"); len(got) != 1 {
		t.Fatalf("filter should be case-insensitive: %v", got)
	}
	if got := l.Filter("nope"); len(got) != 0 {
		t.Fatalf("no match should be empty: %v", got)
	}
}

func TestModelListHasAndDescribe(t *testing.T) {
	live := ModelList{Agent: "opencode", Source: SourceLive, Models: []string{"a", "b"}}
	if !live.Has("a") || live.Has("z") {
		t.Fatal("Has wrong on a live list")
	}
	if !strings.Contains(live.Describe(), "2 models") {
		t.Errorf("describe: %q", live.Describe())
	}

	known := knownList("claude")
	if !strings.Contains(known.Describe(), "not an exhaustive list") {
		t.Errorf("a known list must not look authoritative: %q", known.Describe())
	}
	if len(known.Models) == 0 || known.Note == "" {
		t.Error("claude should offer documented aliases with an explanation")
	}
}

// The real parser must keep provider/model lines and drop everything else.
func TestParseOpenCodeModels(t *testing.T) {
	got := parseOpenCodeModels(`
Available models:
opencode/big-pickle
openai/gpt-5.4
  some/indented noise
anthropic/claude-sonnet-4-5
a banner line with no slash
google/gemini-3-flash
`)
	want := []string{
		"anthropic/claude-sonnet-4-5", "google/gemini-3-flash",
		"openai/gpt-5.4", "opencode/big-pickle", // sorted
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
