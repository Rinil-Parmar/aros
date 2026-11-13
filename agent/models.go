package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Rinil-Parmar/aros/config"
)

// Model discovery.
//
// A hardcoded list of model names is worse than no list: it goes stale and then
// confidently suggests models that no longer exist. Both of Aros's earlier
// defaults did exactly that — `copilot gpt-5.4-mini` and
// `opencode/gpt-5-nano` were both rejected as unavailable.
//
// So each agent reports what it actually supports, by the best means it has:
//
//   - opencode has `opencode models`, which is authoritative. It takes a few
//     seconds, so the result is cached on disk.
//   - claude has no listing command, but rejects an unknown model for free
//     (cost 0, no API call), so Aros offers its documented aliases and
//     *verifies* whatever you choose.
//   - copilot has neither, and its config is not readable, so "auto" is offered
//     and anything else is passed through unchecked.
//
// Lists are therefore labelled by how they were obtained, and a list Aros is
// not sure about never claims to be complete.

// ModelLister is implemented by agents that can enumerate their models.
type ModelLister interface {
	ListModels(ctx context.Context) ([]string, error)
}

// ModelVerifier is implemented by agents that can check a model name cheaply,
// without spending tokens.
type ModelVerifier interface {
	VerifyModel(ctx context.Context, model string) error
}

// ModelSwitcher returns a copy of the agent bound to a different model, so
// verification can try each one in turn.
type ModelSwitcher interface {
	WithModel(model string) Agent
}

// VerifyModels probes each model with one minimal real call and records which
// worked. onResult is called after each so a caller can show progress; results
// are persisted as they arrive, so interrupting it keeps what was learned.
func VerifyModels(ctx context.Context, a Agent, models []string, onResult func(model string, works bool, detail string)) map[string]bool {
	sw, ok := a.(ModelSwitcher)
	if !ok {
		return nil
	}
	results := map[string]bool{}
	if cached, found := readModelCache(a.Name()); found && len(cached.Verified) > 0 {
		for k, v := range cached.Verified {
			results[k] = v // keep earlier answers for models not retried
		}
	}
	for _, model := range models {
		if ctx.Err() != nil {
			break
		}
		works, detail := Probe(ctx, sw.WithModel(model))
		// A cancelled or timed-out probe says nothing about the model. Recording
		// it as a failure would blacklist a perfectly good model — which is
		// exactly what happened the first time this ran: the enclosing deadline
		// expired partway through and every remaining model was written off.
		if !works && ctx.Err() != nil {
			break
		}
		if !works && isTransient(detail) {
			if onResult != nil {
				onResult(model, false, "skipped — "+detail)
			}
			continue // leave it unrecorded rather than wrong
		}
		results[model] = works
		if onResult != nil {
			onResult(model, works, detail)
		}
		writeVerified(a.Name(), results)
	}
	return results
}

// ModelSource says how a list was obtained, so the UI never presents a guess
// as fact.
type ModelSource string

const (
	// SourceLive came from the CLI itself: authoritative.
	SourceLive ModelSource = "live"
	// SourceCached came from a previous live query.
	SourceCached ModelSource = "cached"
	// SourceKnown is a curated list of names the vendor documents. It may be
	// incomplete, and other names may well work.
	SourceKnown ModelSource = "known"
)

// ModelList is what an agent reports.
type ModelList struct {
	Agent   string
	Models  []string
	Source  ModelSource
	Fetched time.Time
	// Note explains a non-live list to the user.
	Note string
	// Verified records which models this installation's credentials could
	// actually serve, model -> works. Empty when never verified.
	//
	// It matters because a model being listed proves nothing: an account can
	// advertise a model and still refuse it. A ChatGPT Plus subscription, for
	// instance, serves 15 of opencode's 33 openai models and rejects the rest
	// with "not supported when using Codex with a ChatGPT account". Only using
	// one tells you, so Aros remembers the answer instead of hardcoding a list
	// that would be wrong for the next person.
	Verified   map[string]bool
	VerifiedAt time.Time
}

// Usable returns the models worth offering: the ones verified to work, or
// everything when nothing has been verified yet.
func (l ModelList) Usable() []string {
	if len(l.Verified) == 0 {
		return l.Models
	}
	var out []string
	for _, m := range l.Models {
		if works, known := l.Verified[m]; works || !known {
			out = append(out, m)
		}
	}
	return out
}

// Rejected counts models verification found unusable.
func (l ModelList) Rejected() int {
	n := 0
	for _, works := range l.Verified {
		if !works {
			n++
		}
	}
	return n
}

// Mark renders a verification marker for one model.
func (l ModelList) Mark(model string) string {
	works, known := l.Verified[model]
	switch {
	case !known:
		return ""
	case works:
		return "verified"
	default:
		return "rejected"
	}
}

// knownModels is the fallback for agents that cannot enumerate. These are
// documented aliases, not an exhaustive list — the Note says so.
var knownModels = map[string]struct {
	models []string
	note   string
}{
	"claude": {
		models: []string{"haiku", "sonnet", "opus"},
		note:   "claude has no list command; these aliases always resolve to current models. Full ids like claude-sonnet-4-5 also work and are verified when you set them.",
	},
	"copilot": {
		models: []string{"auto"},
		note:   "copilot has no list command and its model set depends on your subscription. \"auto\" lets the CLI choose; other names are passed through unchecked.",
	},
}

const modelCacheTTL = 24 * time.Hour

// Models returns the models an agent supports, preferring a live query and
// falling back to cache then to the curated list. refresh skips the cache.
func Models(ctx context.Context, a Agent, refresh bool) ModelList {
	name := a.Name()

	lister, canList := a.(ModelLister)
	if !canList {
		return knownList(name)
	}

	if !refresh {
		if cached, ok := readModelCache(name); ok {
			return cached
		}
	}

	models, err := lister.ListModels(ctx)
	if err != nil || len(models) == 0 {
		// A failed query must not lose a cache we already had.
		if cached, ok := readModelCache(name); ok {
			cached.Note = "could not reach " + name + "; showing the last known list"
			return cached
		}
		l := knownList(name)
		if err != nil {
			l.Note = "could not list models (" + err.Error() + ")"
		}
		return l
	}

	list := ModelList{Agent: name, Models: models, Source: SourceLive, Fetched: time.Now()}
	if prev, found := readModelCache(name); found {
		list.Verified, list.VerifiedAt = prev.Verified, prev.VerifiedAt
	}
	writeModelCache(list)
	return list
}

func knownList(name string) ModelList {
	if k, ok := knownModels[name]; ok {
		return ModelList{Agent: name, Models: k.models, Source: SourceKnown, Note: k.note}
	}
	return ModelList{
		Agent:  name,
		Source: SourceKnown,
		Note:   "this agent cannot list its models; any name is passed through unchecked",
	}
}

// Filter returns the models containing q (case-insensitive). An empty q returns
// everything. Exact matches sort first so a full name is always at the top.
func (l ModelList) Filter(q string) []string {
	if q == "" {
		return l.Models
	}
	lq := strings.ToLower(q)
	var exact, prefix, rest []string
	for _, m := range l.Models {
		lm := strings.ToLower(m)
		switch {
		case lm == lq:
			exact = append(exact, m)
		case strings.HasPrefix(lm, lq):
			prefix = append(prefix, m)
		case strings.Contains(lm, lq):
			rest = append(rest, m)
		}
	}
	return append(append(exact, prefix...), rest...)
}

// Has reports whether the list contains model exactly. Always false for a
// SourceKnown list, which makes no completeness claim.
func (l ModelList) Has(model string) bool {
	if l.Source == SourceKnown {
		return false
	}
	for _, m := range l.Models {
		if m == model {
			return true
		}
	}
	return false
}

// Describe renders a one-line provenance note for the UI.
func (l ModelList) Describe() string {
	switch l.Source {
	case SourceLive:
		return fmt.Sprintf("%d models, from %s just now", len(l.Models), l.Agent)
	case SourceCached:
		s := fmt.Sprintf("%d models, cached %s ago", len(l.Models), time.Since(l.Fetched).Round(time.Minute))
		if n := l.Rejected(); n > 0 {
			s += fmt.Sprintf(" · %d your account cannot use", n)
		}
		return s
	default:
		return fmt.Sprintf("%d known names (not an exhaustive list)", len(l.Models))
	}
}

// ── opencode: a real listing command ───────────────────────────────────────────

// ListModels runs `opencode models`. Authoritative but takes a few seconds,
// which is why Models() caches it.
func (a *OpenCodeAdapter) ListModels(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, _, _, err := runCommand(ctx, "opencode", []string{"models"}, a.WorkDir, nil, a.env()...)
	if err != nil {
		return nil, err
	}
	return parseOpenCodeModels(out), nil
}

// parseOpenCodeModels keeps the "provider/name" lines and drops banners and
// anything else the command may print around them.
func parseOpenCodeModels(raw string) []string {
	var models []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, "/") || strings.ContainsAny(line, " \t") {
			continue
		}
		models = append(models, line)
	}
	sort.Strings(models)
	return models
}

// ── claude: free verification ──────────────────────────────────────────────────

// VerifyModel asks claude to resolve the model without doing any work. An
// unknown model is rejected with no API call and no cost, so this is a free
// check that catches a typo now instead of mid-phase.
func (a *ClaudeAdapter) VerifyModel(ctx context.Context, model string) error {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	probe := &ClaudeAdapter{Model: model, WorkDir: a.WorkDir}
	// An empty tool set and a one-character prompt: the model is resolved
	// before anything is sent, so a bad name fails before any token is spent.
	res, err := probe.exec(ctx, probe.chatArgs(), "hi")
	if err == nil {
		return nil
	}
	low := strings.ToLower(err.Error() + " " + res.Output)
	if strings.Contains(low, "model") &&
		(strings.Contains(low, "not exist") || strings.Contains(low, "unrecognized") ||
			strings.Contains(low, "may not have access")) {
		return fmt.Errorf("claude does not recognise model %q (or your account has no access to it)", model)
	}
	// Anything else — network, auth, rate limit — is not the model's fault.
	return nil
}

// ── cache ──────────────────────────────────────────────────────────────────────

type modelCacheFile struct {
	Agents map[string]modelCacheEntry `json:"agents"`
}

type modelCacheEntry struct {
	Models     []string        `json:"models"`
	Fetched    time.Time       `json:"fetched_at"`
	Verified   map[string]bool `json:"verified,omitempty"`
	VerifiedAt time.Time       `json:"verified_at,omitempty"`
}

var modelCacheMu sync.Mutex

func modelCachePath() string {
	return filepath.Join(config.DefaultGlobalPath(), "models-cache.json")
}

func readModelCache(agent string) (ModelList, bool) {
	modelCacheMu.Lock()
	defer modelCacheMu.Unlock()

	data, err := os.ReadFile(modelCachePath())
	if err != nil {
		return ModelList{}, false
	}
	var f modelCacheFile
	if err := json.Unmarshal(data, &f); err != nil {
		return ModelList{}, false
	}
	e, ok := f.Agents[agent]
	if !ok || len(e.Models) == 0 || time.Since(e.Fetched) > modelCacheTTL {
		return ModelList{}, false
	}
	return ModelList{
		Agent: agent, Models: e.Models, Source: SourceCached, Fetched: e.Fetched,
		Verified: e.Verified, VerifiedAt: e.VerifiedAt,
	}, true
}

// writeVerified stores verification results without touching the model list.
func writeVerified(agentName string, verified map[string]bool) {
	modelCacheMu.Lock()
	defer modelCacheMu.Unlock()

	path := modelCachePath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return
	}
	f := modelCacheFile{Agents: map[string]modelCacheEntry{}}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &f)
		if f.Agents == nil {
			f.Agents = map[string]modelCacheEntry{}
		}
	}
	e := f.Agents[agentName]
	e.Verified = verified
	e.VerifiedAt = time.Now()
	f.Agents[agentName] = e
	if data, err := json.MarshalIndent(f, "", "  "); err == nil {
		tmp := path + ".tmp"
		if os.WriteFile(tmp, data, 0644) == nil {
			_ = os.Rename(tmp, path)
		}
	}
}

func writeModelCache(l ModelList) {
	modelCacheMu.Lock()
	defer modelCacheMu.Unlock()

	path := modelCachePath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return // caching is best-effort
	}
	f := modelCacheFile{Agents: map[string]modelCacheEntry{}}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &f)
		if f.Agents == nil {
			f.Agents = map[string]modelCacheEntry{}
		}
	}
	// A fresh listing must not discard what we learned about the credentials.
	prev := f.Agents[l.Agent]
	f.Agents[l.Agent] = modelCacheEntry{
		Models: l.Models, Fetched: l.Fetched,
		Verified: prev.Verified, VerifiedAt: prev.VerifiedAt,
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, data, 0644) == nil {
		_ = os.Rename(tmp, path)
	}
}

// ── health checks ──────────────────────────────────────────────────────────────

// Health is the outcome of checking one agent.
type Health struct {
	Agent    string
	Model    string
	Binary   string // version line, or "" when unknown
	ModelOK  bool
	ModelWhy string
	CallOK   bool // only meaningful when a probe ran
	CallWhy  string
	Probed   bool
}

// CheckModel verifies the configured model as cheaply as the agent allows:
// against a live list where there is one, otherwise via a free rejection check,
// otherwise not at all.
func CheckModel(ctx context.Context, a Agent, model string) (ok bool, why string) {
	if list := Models(ctx, a, false); list.Source != SourceKnown {
		if list.Has(model) {
			return true, fmt.Sprintf("in %s's list of %d", a.Name(), len(list.Models))
		}
		near := list.Filter(model)
		if len(near) > 3 {
			near = near[:3]
		}
		why = fmt.Sprintf("not in %s's list", a.Name())
		if len(near) > 0 {
			why += " — closest: " + strings.Join(near, ", ")
		}
		return false, why
	}
	if v, canVerify := a.(ModelVerifier); canVerify {
		if err := v.VerifyModel(ctx, model); err != nil {
			return false, err.Error()
		}
		return true, "accepted by " + a.Name()
	}
	return true, "cannot be checked — " + a.Name() + " offers no way to validate a name"
}

// Probe makes one minimal real call, which is the only way to know that
// credentials actually work. It costs a little: a few hundredths of a cent for
// claude, one premium request for copilot. Callers make it opt-in.
func Probe(ctx context.Context, a Agent) (ok bool, detail string) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	res, err := Reason(ctx, a, "Reply with the single word: OK")
	if err != nil {
		return false, firstLine([]byte(err.Error()), err)
	}
	out := strings.TrimSpace(res.Output)
	if out == "" {
		return false, "the call succeeded but returned nothing"
	}
	if len(out) > 60 {
		out = out[:60] + "…"
	}
	return true, "replied " + fmt.Sprintf("%q", out)
}

// isTransient reports failures that are about the moment, not the model:
// timeouts, cancellation, rate limits and network trouble. Recording one as
// "this model does not work" would be wrong and sticky.
func isTransient(detail string) bool {
	d := strings.ToLower(detail)
	for _, s := range []string{
		"context deadline", "context canceled", "signal: killed",
		"rate limit", "429", "too many requests",
		"timeout", "timed out", "temporarily", "503", "502", "connection",
	} {
		if strings.Contains(d, s) {
			return true
		}
	}
	return false
}

// BinaryVersion returns the agent CLI's version line, best-effort.
func BinaryVersion(ctx context.Context, name string) string {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, _, _, err := runCommand(ctx, name, []string{"--version"}, "", nil)
	if err != nil {
		return ""
	}
	return firstLine([]byte(out), nil)
}

// ── binding an adapter to a different model ────────────────────────────────────

func (a *ClaudeAdapter) WithModel(model string) Agent {
	c := *a
	c.Model = model
	return &c
}

func (a *OpenCodeAdapter) WithModel(model string) Agent {
	c := *a
	c.Model = model
	return &c
}

func (a *CopilotAdapter) WithModel(model string) Agent {
	c := *a
	c.Model = model
	return &c
}
