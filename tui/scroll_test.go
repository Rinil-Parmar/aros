package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Scrolling must not change the conversation. Reported symptom: scrolling up or
// down made something appear in the chat.
func TestScrollingWritesNothing(t *testing.T) {
	inputs := []struct {
		name string
		msg  tea.Msg
	}{
		{"pgup", tea.KeyMsg{Type: tea.KeyPgUp}},
		{"pgdown", tea.KeyMsg{Type: tea.KeyPgDown}},
		{"home", tea.KeyMsg{Type: tea.KeyHome}},
		{"end", tea.KeyMsg{Type: tea.KeyEnd}},
		{"shift+up", tea.KeyMsg{Type: tea.KeyShiftUp}},
		{"shift+down", tea.KeyMsg{Type: tea.KeyShiftDown}},
		{"up", tea.KeyMsg{Type: tea.KeyUp}},
		{"down", tea.KeyMsg{Type: tea.KeyDown}},
		{"ctrl+u", tea.KeyMsg{Type: tea.KeyCtrlU}},
		{"wheel up", tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp}},
		{"wheel down", tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown}},
		{"wheel motion", tea.MouseMsg{Action: tea.MouseActionMotion, Button: tea.MouseButtonNone}},
		{"wheel release", tea.MouseMsg{Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft}},
	}

	for _, in := range inputs {
		m := fixtureModel(false)
		m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
		populate(m)

		before := len(m.messages)
		beforeInput := m.textarea.Value()
		var beforePhase string
		if m.project != nil {
			beforePhase = string(m.project.Phase)
		}

		// Scroll a few times, as a user would.
		for i := 0; i < 3; i++ {
			m.Update(in.msg)
		}

		if after := len(m.messages); after != before {
			var added []string
			for _, msg := range m.messages[before:] {
				added = append(added, fmt.Sprintf("%q", msg.Body))
			}
			t.Errorf("%s added %d message(s) to the chat: %v", in.name, after-before, added)
		}
		if got := m.textarea.Value(); got != beforeInput {
			t.Errorf("%s changed the input box to %q", in.name, got)
		}
		if m.project != nil && string(m.project.Phase) != beforePhase {
			t.Errorf("%s changed the phase %s → %s", in.name, beforePhase, m.project.Phase)
		}
	}
}

// Scrolling up then receiving new output must not yank the view back to the
// bottom: that is the other half of "scrolling does not work".
func TestScrollPositionIsKeptWhenOutputArrives(t *testing.T) {
	m := fixtureModel(false)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	for i := 0; i < 60; i++ {
		m.appendStream("claude", fmt.Sprintf("line %d of agent output", i))
	}
	// Scroll away from the bottom.
	m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if m.viewport.AtBottom() {
		t.Skip("viewport could not scroll in this fixture")
	}
	at := m.viewport.YOffset

	m.appendStream("claude", "a new line arrives while you are reading history")

	if m.viewport.YOffset != at {
		t.Errorf("new output moved the view from %d to %d — reading history is impossible",
			at, m.viewport.YOffset)
	}
}

// Esc with nothing running must write nothing. Terminals emit a bare ESC inside
// mouse and scroll sequences, so a message here showed up as text appearing in
// the chat while scrolling.
func TestIdleEscapeWritesNothing(t *testing.T) {
	m := fixtureModel(false)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	populate(m)
	m.busy, m.chatInProgress = false, false
	m.mode = modeText

	before := len(m.messages)
	for i := 0; i < 5; i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	}
	if after := len(m.messages); after != before {
		var added []string
		for _, msg := range m.messages[before:] {
			added = append(added, fmt.Sprintf("%q", msg.Body))
		}
		t.Fatalf("idle Esc wrote %d message(s): %v", after-before, added)
	}

	// It should still clear a half-typed line.
	m.textarea.SetValue("half typed")
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if got := m.textarea.Value(); got != "" {
		t.Errorf("Esc did not clear the input: %q", got)
	}

	// And it must still cancel something real, with a message.
	m.busy = true
	before = len(m.messages)
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if len(m.messages) == before {
		t.Error("Esc cancelled a running phase without saying so")
	}
	if m.busy {
		t.Error("Esc did not clear busy")
	}
}

// When the view is scrolled away from the tail, the status bar must say so —
// otherwise new output silently not appearing looks like a freeze.
func TestStatusBarShowsWhenScrolledAway(t *testing.T) {
	m := fixtureModel(false)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	for i := 0; i < 80; i++ {
		m.appendStream("claude", fmt.Sprintf("output line %d", i))
	}
	if !m.viewport.AtBottom() {
		t.Fatal("fixture should start at the bottom")
	}
	if got := stripANSI(m.renderStatusBar()); strings.Contains(got, "end") {
		t.Errorf("hint shown while at the bottom: %q", got)
	}

	m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if m.viewport.AtBottom() {
		t.Skip("viewport did not scroll")
	}
	if got := stripANSI(m.renderStatusBar()); !strings.Contains(got, "end") {
		t.Errorf("no jump-to-end hint while scrolled away: %q", got)
	}
}
