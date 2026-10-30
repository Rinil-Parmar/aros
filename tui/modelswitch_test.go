package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// echoModelClaude reports back which --model it was invoked with, so a test can
// see the model actually used rather than the one the UI claims.
const echoModelClaude = `#!/usr/bin/env bash
set -euo pipefail
model="?"
prev=""
for arg in "$@"; do
  if [[ "$prev" == "--model" ]]; then model="$arg"; fi
  prev="$arg"
done
prompt="$(cat || true)"
printf '{"type":"result","result":"MODEL_USED=%s","is_error":false,"session_id":"s-1"}\n' "$model"
`

func newEchoHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	// Replace the mock with one that echoes the model back.
	bin := filepath.Join(os.Getenv("HOME"), "bin", "claude")
	if err := os.WriteFile(bin, []byte(echoModelClaude), 0755); err != nil {
		t.Fatal(err)
	}
	return h
}

// The reported bug: set the model, then chat, and the old model comes back.
func TestModelChangeAppliesToTheNextChat(t *testing.T) {
	h := newEchoHarness(t)
	defer h.quit()
	h.startProject()

	h.inspect(func(m *Model) {
		if got := m.cfg.Agents["claude"].Model; got != "mock" {
			t.Fatalf("fixture model is %q", got)
		}
	})

	h.typeLine("/model claude sonnet")
	h.waitFor("model set", 60*time.Second, func(msg tea.Msg) bool {
		_, ok := msg.(modelSetMsg)
		return ok
	})

	// 1. config must hold the new model
	h.inspect(func(m *Model) {
		if got := m.cfg.Agents["claude"].Model; got != "sonnet" {
			t.Errorf("config still has %q after /model claude sonnet", got)
		}
	})

	// 2. the chat must actually invoke it
	h.typeLine("which model are you?")
	h.waitFor("chat done", 60*time.Second, func(msg tea.Msg) bool {
		cd, ok := msg.(chatDoneMsg)
		return ok && cd.err == nil
	})
	h.inspect(func(m *Model) {
		all := m.plainTranscript("")
		if strings.Contains(all, "MODEL_USED=mock") {
			t.Error("the chat used the OLD model after /model")
		}
		if !strings.Contains(all, "MODEL_USED=sonnet") {
			t.Errorf("the chat did not use sonnet:\n%s", all)
		}
		// 3. the header on that reply must name the new model
		var last string
		for _, msg := range m.messages {
			if msg.Kind == kindAgent {
				last = msg.ModelName
			}
		}
		if last != "sonnet" {
			t.Errorf("reply header says model %q, want sonnet", last)
		}
	})
}

// The reported symptom reproduced: a model set in the TUI is lost the moment
// you reopen, because /model only touched the in-memory config.
func TestModelChangeSurvivesRestart(t *testing.T) {
	h := newEchoHarness(t)
	cur := h
	defer func() { cur.quit() }()
	h.startProject()

	h.typeLine("/model claude sonnet")
	h.waitFor("model set", 60*time.Second, func(msg tea.Msg) bool {
		_, ok := msg.(modelSetMsg)
		return ok
	})
	h.inspect(func(m *Model) {
		if got := m.cfg.Agents["claude"].Model; got != "sonnet" {
			t.Fatalf("setup failed: model is %q", got)
		}
	})

	h2 := h.restart()
	cur = h2
	h2.waitFor("bootstrap", 15*time.Second, func(msg tea.Msg) bool {
		_, ok := msg.(bootstrapMsg)
		return ok
	})
	h2.inspect(func(m *Model) {
		if got := m.cfg.Agents["claude"].Model; got != "sonnet" {
			t.Errorf("after reopening, model is %q — the change was lost", got)
		}
	})
}

// The picker flow: /model with no arguments must get you to a chosen model
// without typing any exact name.
func TestModelPickerFlowNeedsNoExactName(t *testing.T) {
	h := newEchoHarness(t)
	defer h.quit()
	h.startProject()

	// /model with nothing else opens the agent chooser.
	h.typeLine("/model")
	h.inspect(func(m *Model) {
		if m.mode != modePicker || m.picker == nil {
			t.Fatalf("no picker opened (mode=%d)", m.mode)
		}
		if got, _ := m.picker.selected(); got.value != "claude" {
			t.Fatalf("agent picker selected %+v", got)
		}
	})

	// Enter on the agent kicks off the model lookup, which opens the second picker.
	h.p.Send(tea.KeyMsg{Type: tea.KeyEnter})
	h.waitFor("models fetched", 60*time.Second, func(msg tea.Msg) bool {
		_, ok := msg.(modelPickMsg)
		return ok
	})
	h.inspect(func(m *Model) {
		if m.mode != modePicker || m.picker == nil {
			t.Fatalf("model picker not open (mode=%d)", m.mode)
		}
		if len(m.picker.items) == 0 {
			t.Fatal("model picker is empty")
		}
	})

	// Arrow to a different model and take it — no typing of exact names.
	var chosen string
	h.p.Send(tea.KeyMsg{Type: tea.KeyDown})
	h.inspect(func(m *Model) {
		got, ok := m.picker.selected()
		if !ok {
			t.Fatal("nothing selected after arrow down")
		}
		chosen = got.value
	})
	h.p.Send(tea.KeyMsg{Type: tea.KeyEnter})
	h.waitFor("model applied", 60*time.Second, func(msg tea.Msg) bool {
		_, ok := msg.(modelSetMsg)
		return ok
	})

	h.inspect(func(m *Model) {
		if m.mode == modePicker {
			t.Error("picker stayed open after selecting")
		}
		if got := m.cfg.Agents["claude"].Model; got != chosen {
			t.Errorf("config has %q, picked %q", got, chosen)
		}
	})

	// And it is the model actually used for the next call.
	h.typeLine("which model?")
	h.waitFor("chat done", 60*time.Second, func(msg tea.Msg) bool {
		cd, ok := msg.(chatDoneMsg)
		return ok && cd.err == nil
	})
	h.inspect(func(m *Model) {
		if all := m.plainTranscript(""); !strings.Contains(all, "MODEL_USED="+chosen) {
			t.Errorf("chat did not use the picked model %q:\n%s", chosen, all)
		}
	})
}

// Esc must dismiss the picker and leave the setting alone.
func TestModelPickerEscapeChangesNothing(t *testing.T) {
	h := newEchoHarness(t)
	defer h.quit()
	h.startProject()

	var before string
	h.inspect(func(m *Model) { before = m.cfg.Agents["claude"].Model })

	h.typeLine("/model claude")
	h.waitFor("models fetched", 60*time.Second, func(msg tea.Msg) bool {
		_, ok := msg.(modelPickMsg)
		return ok
	})
	h.p.Send(tea.KeyMsg{Type: tea.KeyDown})
	h.p.Send(tea.KeyMsg{Type: tea.KeyEsc})
	h.inspect(func(m *Model) {
		if m.mode == modePicker || m.picker != nil {
			t.Error("esc did not close the picker")
		}
		if got := m.cfg.Agents["claude"].Model; got != before {
			t.Errorf("esc changed the model to %q", got)
		}
	})

	// The input box must work again straight away.
	h.typeLine("status")
	h.inspect(func(m *Model) {
		if m.mode != modeText {
			t.Errorf("mode is %d after esc, want modeText", m.mode)
		}
	})
}
