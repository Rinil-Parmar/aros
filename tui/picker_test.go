package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func items(labels ...string) []pickerItem {
	out := make([]pickerItem, 0, len(labels))
	for _, l := range labels {
		out = append(out, pickerItem{label: l, value: l})
	}
	return out
}

func key(t tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: t} }
func runes(s string) tea.KeyMsg    { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

// The picker must open on whatever is already in effect, so the list starts
// where you are instead of at the top.
func TestPickerOpensOnCurrent(t *testing.T) {
	p := newPicker("model", items("a", "b", "c"), "b", nil)
	got, ok := p.selected()
	if !ok || got.value != "b" {
		t.Fatalf("opened on %+v, want b", got)
	}
}

func TestPickerFilterAndSelect(t *testing.T) {
	var picked string
	p := newPicker("model", items(
		"openai/gpt-5.4", "openai/gpt-5.4-mini", "anthropic/claude-sonnet-4-5",
	), "", func(v string) tea.Cmd { picked = v; return nil })

	// Typing narrows the list.
	for _, r := range "mini" {
		p.handleKey(runes(string(r)))
	}
	if len(p.match) != 1 {
		t.Fatalf("filter %q left %d matches", p.query, len(p.match))
	}
	if done, _ := p.handleKey(key(tea.KeyEnter)); !done {
		t.Fatal("enter should close the picker")
	}
	if picked != "openai/gpt-5.4-mini" {
		t.Fatalf("picked %q", picked)
	}
}

func TestPickerBackspaceWidensAgain(t *testing.T) {
	p := newPicker("m", items("alpha", "beta", "gamma"), "", nil)
	for _, r := range "ga" {
		p.handleKey(runes(string(r)))
	}
	if len(p.match) != 1 {
		t.Fatalf("expected 1 match for %q, got %d", p.query, len(p.match))
	}
	p.handleKey(key(tea.KeyBackspace))
	p.handleKey(key(tea.KeyBackspace))
	if len(p.match) != 3 {
		t.Fatalf("backspace did not widen: %d matches for %q", len(p.match), p.query)
	}
}

func TestPickerWrapsAndScrolls(t *testing.T) {
	var labels []string
	for i := 0; i < 20; i++ {
		labels = append(labels, string(rune('a'+i)))
	}
	p := newPicker("m", items(labels...), "", nil)

	// Up from the first entry wraps to the last.
	p.handleKey(key(tea.KeyUp))
	if got, _ := p.selected(); got.label != "t" {
		t.Errorf("up from the top selected %q, want the last entry", got.label)
	}
	// Down from the last wraps to the first.
	p.handleKey(key(tea.KeyDown))
	if got, _ := p.selected(); got.label != "a" {
		t.Errorf("down from the bottom selected %q, want the first", got.label)
	}
	// The window follows the cursor.
	for i := 0; i < 12; i++ {
		p.handleKey(key(tea.KeyDown))
	}
	if p.cursor < p.top || p.cursor >= p.top+p.visibleRows() {
		t.Errorf("cursor %d outside the visible window [%d,%d)", p.cursor, p.top, p.top+p.visibleRows())
	}
}

// Enter with nothing matching must not close the picker or pick anything.
func TestPickerEnterWithNoMatchDoesNothing(t *testing.T) {
	called := false
	p := newPicker("m", items("alpha"), "", func(string) tea.Cmd { called = true; return nil })
	for _, r := range "zzz" {
		p.handleKey(runes(string(r)))
	}
	done, _ := p.handleKey(key(tea.KeyEnter))
	if done || called {
		t.Fatalf("enter with no match: done=%v called=%v", done, called)
	}
}

func TestPickerEscCloses(t *testing.T) {
	called := false
	p := newPicker("m", items("a"), "", func(string) tea.Cmd { called = true; return nil })
	done, _ := p.handleKey(key(tea.KeyEsc))
	if !done || called {
		t.Fatalf("esc should close without selecting: done=%v called=%v", done, called)
	}
}

// The overlay has to fit the frame exactly, like everything else.
func TestPickerRendersToExactWidth(t *testing.T) {
	for _, n := range []int{1, 3, 9, 40} {
		var labels []string
		for i := 0; i < n; i++ {
			labels = append(labels, strings.Repeat("x", 10+i)+"/model-name")
		}
		for _, w := range []int{60, 80, 120} {
			m := fixtureModel(false)
			m.Update(tea.WindowSizeMsg{Width: w, Height: 40})
			p := newPicker("claude model", items(labels...), labels[0], nil)
			p.query = "model"
			p.refilter()
			m.openPicker(p)

			out := m.renderPicker()
			lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
			if len(lines) != p.rows() {
				t.Errorf("n=%d w=%d: %d rows rendered, rows() says %d", n, w, len(lines), p.rows())
			}
			for i, line := range lines {
				if got := lipgloss.Width(line); got != w {
					t.Errorf("n=%d w=%d: row %d is %d cols\n  %q", n, w, i+1, got, stripANSI(line))
					break
				}
			}
		}
	}
}
