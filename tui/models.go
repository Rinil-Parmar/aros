package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/Rinil-Parmar/aros/agent"
	"github.com/Rinil-Parmar/aros/config"
	tea "github.com/charmbracelet/bubbletea"
)

// modelListMsg carries a model list back from a lookup goroutine.
type modelListMsg struct {
	gen    int
	list   agent.ModelList
	filter string
	scope  config.Scope
	// setTo is non-empty when the lookup was to validate a model before using it.
	setTo string
}

// modelSetMsg reports the result of verifying and applying a model.
type modelSetMsg struct {
	gen     int
	agent   string
	model   string
	err     error
	warning string
	scope   config.Scope
}

// handleModelCmd implements /model.
//
//	/model                       what each agent is using
//	/model <agent>               the models that agent actually supports
//	/model <agent> <text>        exact name -> set it; otherwise filter the list
//	/model <agent> --refresh     re-query, bypassing the cache
//
// Listing and setting both run off the event loop: a live query takes seconds
// and claude's verification spawns a subprocess.
func (m *Model) handleModelCmd(parts []string) tea.Cmd {
	if len(parts) == 1 {
		m.addChrome("Models in use:")
		for _, name := range m.agentNames() {
			ac := m.cfg.Agents[name]
			mark := "  "
			if !ac.Enabled {
				mark = "· "
			}
			m.addChrome(fmt.Sprintf("  %s%-10s %s", mark, name, ac.Model))
		}
		m.addChrome("")
		m.addChrome("/model <agent>            list what that agent supports")
		m.addChrome("/model <agent> <text>     set it, or filter the list")
		m.addChrome("  saved to the project config; --global for every project, --session for neither")
		return nil
	}

	agentName := strings.ToLower(parts[1])
	ag, ok := m.reg[agentName]
	if !ok {
		if _, configured := m.cfg.Agents[agentName]; configured {
			m.addError(fmt.Sprintf("agent %q is not available here (disabled, or its CLI is not on PATH)", agentName))
		} else {
			m.addError(fmt.Sprintf("unknown agent %q — configured: %s", agentName, strings.Join(m.agentNames(), ", ")))
		}
		return nil
	}

	refresh := false
	scope := config.ScopeProject // a model choice belongs to the project
	var rest []string
	for _, p := range parts[2:] {
		switch p {
		case "--refresh", "-r":
			refresh = true
		case "--global", "-g":
			scope = config.ScopeGlobal
		case "--session":
			scope = scopeSession
		default:
			rest = append(rest, p)
		}
	}
	query := strings.Join(rest, " ")

	if m.rejectIfBusy() {
		return nil
	}
	pr := m.newPhaseRun() // reuse the busy/cancel machinery so Esc works
	gen := pr.gen
	if query == "" {
		m.addChrome(fmt.Sprintf("looking up %s models…", agentName))
	}

	return func() tea.Msg {
		list := agent.Models(pr.ctx, ag, refresh)

		// An exact match is a set, not a search.
		if query != "" && list.Has(query) {
			return modelSetMsg{gen: gen, agent: agentName, model: query, scope: scope}
		}
		// A list that cannot claim completeness must still let you set a name,
		// after verifying it where that is possible.
		if query != "" && list.Source == agent.SourceKnown {
			warn := ""
			if v, canVerify := ag.(agent.ModelVerifier); canVerify {
				if err := v.VerifyModel(pr.ctx, query); err != nil {
					return modelSetMsg{gen: gen, agent: agentName, model: query, err: err, scope: scope}
				}
			} else {
				warn = agentName + " cannot verify model names; if it is wrong you will see it on the next call"
			}
			return modelSetMsg{gen: gen, agent: agentName, model: query, warning: warn, scope: scope}
		}
		return modelListMsg{gen: gen, list: list, filter: query, scope: scope}
	}
}

// showModelList renders a filtered list, and sets the model outright when the
// filter leaves exactly one candidate.
func (m *Model) showModelList(msg modelListMsg) {
	list, q := msg.list, msg.filter
	matches := list.Filter(q)

	if q != "" && len(matches) == 1 {
		m.applyModel(list.Agent, matches[0], "", msg.scope)
		return
	}
	if q != "" && len(matches) == 0 {
		m.addError(fmt.Sprintf("no %s model matches %q", list.Agent, q))
		if list.Source != agent.SourceKnown {
			m.addChrome(fmt.Sprintf("/model %s          to see all %d", list.Agent, len(list.Models)))
		}
		return
	}

	header := fmt.Sprintf("%s · %s", list.Agent, list.Describe())
	if q != "" {
		header = fmt.Sprintf("%s · %d of %d match %q", list.Agent, len(matches), len(list.Models), q)
	}
	m.addChrome(header)
	if list.Note != "" {
		m.addChrome("  " + list.Note)
	}

	// A long list is unreadable in a panel, so cap it and say how to narrow.
	const maxShown = 24
	shown := matches
	if len(shown) > maxShown {
		shown = shown[:maxShown]
	}
	current := m.cfg.Agents[list.Agent].Model
	for _, mdl := range shown {
		mark := "  "
		if mdl == current {
			mark = "→ " // what is in use now
		}
		m.addChrome("  " + mark + mdl)
	}
	if len(matches) > maxShown {
		m.addChrome(fmt.Sprintf("  … %d more — narrow it: /model %s <text>", len(matches)-maxShown, list.Agent))
	}
}

// applyModel sets the model for the running session and writes it to the
// config, so reopening keeps it. It used to change only the in-memory config,
// which meant the choice silently reverted to the file's value — the default
// haiku, for anyone without a model in their config — the next time Aros
// opened. "This session only" was documented, but it is not what anyone
// expects from choosing a model.
func (m *Model) applyModel(agentName, model, warning string, scope config.Scope) {
	if err := m.setAgentModel(agentName, model); err != nil {
		m.addError(err.Error())
		return
	}
	m.addSuccess(fmt.Sprintf("%s → %s", agentName, model))
	if warning != "" {
		m.addChrome("  " + warning)
	}
	if scope == scopeSession {
		m.addChrome("  this session only — it will revert when you reopen")
		return
	}
	path, err := config.SetAgentModel(m.cwd, agentName, model, scope)
	if err != nil {
		m.addError("could not save it: " + err.Error())
		m.addChrome("  the change applies to this session only")
		return
	}
	m.addChrome("  saved to " + m.shortPath(path))
}

// scopeSession means "do not write this to any file".
const scopeSession config.Scope = "session"

// shortPath renders a config path relative to the project when it is inside it,
// so the common case reads as .aros/config.toml rather than an absolute path.
func (m *Model) shortPath(path string) string {
	if m.cwd != "" && strings.HasPrefix(path, m.cwd+"/") {
		return strings.TrimPrefix(path, m.cwd+"/")
	}
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(path, home+"/") {
		return "~/" + strings.TrimPrefix(path, home+"/")
	}
	return path
}
