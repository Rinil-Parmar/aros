package agent

import (
	"context"
	"fmt"
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

// A verified list must offer only what the credentials can serve: a listed
// model the account refuses is worse than an absent one, because you pick it
// and the next phase fails.
func TestUsableHidesRejectedModels(t *testing.T) {
	l := ModelList{
		Agent:  "opencode",
		Source: SourceCached,
		Models: []string{"a", "b", "c", "d"},
		Verified: map[string]bool{
			"a": true, "b": false, "c": true, // d never probed
		},
	}
	got := l.Usable()
	want := []string{"a", "c", "d"} // unprobed stays available
	if len(got) != len(want) {
		t.Fatalf("Usable() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Usable() = %v, want %v", got, want)
		}
	}
	if l.Rejected() != 1 {
		t.Errorf("Rejected() = %d, want 1", l.Rejected())
	}
	if l.Mark("a") != "verified" || l.Mark("b") != "rejected" || l.Mark("d") != "" {
		t.Errorf("marks wrong: a=%q b=%q d=%q", l.Mark("a"), l.Mark("b"), l.Mark("d"))
	}

	// With no verification data, everything is on offer.
	plain := ModelList{Models: []string{"x", "y"}, Source: SourceLive}
	if len(plain.Usable()) != 2 {
		t.Errorf("an unverified list must offer everything: %v", plain.Usable())
	}
}

// switchAgent reports which model it was asked to use, and can fail on command.
type switchAgent struct {
	model    string
	failWith map[string]string
	tried    []string
}

func (s *switchAgent) Name() string { return "switcher" }
func (s *switchAgent) Run(ctx context.Context, p string) (AgentResult, error) {
	s.tried = append(s.tried, s.model)
	if msg, bad := s.failWith[s.model]; bad {
		return AgentResult{}, fmt.Errorf("%s", msg)
	}
	return AgentResult{Output: "OK"}, nil
}
func (s *switchAgent) WithModel(m string) Agent {
	c := *s
	c.model = m
	c.tried = s.tried
	return &c
}

// A timeout or rate limit says nothing about a model. Recording one as a
// failure blacklists a good model, which is what happened on the first real
// run: the enclosing deadline expired and every remaining model was written
// off as unusable.
func TestVerifyModelsDoesNotBlacklistOnTransientFailures(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	a := &switchAgent{failWith: map[string]string{
		"m2": "429 rate limit exceeded",
		"m3": "the model is not supported with this account",
	}}
	got := VerifyModels(context.Background(), a, []string{"m1", "m2", "m3", "m4"}, nil)

	if works, known := got["m1"]; !known || !works {
		t.Errorf("m1 should be recorded working: %v %v", works, known)
	}
	if _, known := got["m2"]; known {
		t.Error("a rate-limited probe must not be recorded as a verdict")
	}
	if works, known := got["m3"]; !known || works {
		t.Errorf("a genuine rejection should be recorded as failing: %v %v", works, known)
	}
	if works, known := got["m4"]; !known || !works {
		t.Error("verification should continue past a transient failure")
	}
}

// A cancelled run must stop and record nothing further.
func TestVerifyModelsStopsOnCancellation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	a := &switchAgent{}
	got := VerifyModels(ctx, a, []string{"m1", "m2"}, nil)
	if len(got) != 0 {
		t.Fatalf("a cancelled run recorded verdicts: %v", got)
	}
}

func TestVerifyModelsNeedsASwitcher(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if got := VerifyModels(context.Background(), &runOnlyAgent{}, []string{"m"}, nil); got != nil {
		t.Fatalf("an agent that cannot switch model should verify nothing: %v", got)
	}
}

// A fresh listing must not discard what was learned about the credentials.
func TestVerificationSurvivesARefresh(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := &listerAgent{name: "opencode", models: []string{"m1", "m2"}}
	Models(context.Background(), a, false)
	writeVerified("opencode", map[string]bool{"m1": true, "m2": false})

	a.models = []string{"m1", "m2", "m3"}
	l := Models(context.Background(), a, true) // re-query
	if len(l.Verified) != 2 {
		t.Fatalf("refresh lost the verification data: %+v", l.Verified)
	}
	if l.Rejected() != 1 {
		t.Errorf("Rejected() = %d after refresh", l.Rejected())
	}
}
