package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// A picker is a list you choose from with the arrow keys, instead of having to
// know and type an exact name. Model names in particular are long, numerous and
// unguessable (`openai/gpt-5.4-mini-flex`), so requiring the exact string made
// changing a model harder than reading the list it came from.
//
// It is deliberately generic: /model uses it twice, once to choose the agent
// and once to choose that agent's model.

const (
	pickerMaxVisible = 9
	// The title and footer are drawn *into* the border rows, so the chrome is
	// just those two rows. Getting this wrong makes the whole frame too tall.
	pickerChrome = 2
)

type pickerItem struct {
	label string // shown
	value string // handed to onSelect
	hint  string // dim, right of the label
}

type picker struct {
	title   string
	items   []pickerItem
	match   []int // indices into items, after filtering
	query   string
	cursor  int // index into match
	top     int // first visible row, for scrolling
	current string
	footer  string
	// onSelect runs on the event loop and may return a command, so selecting
	// can kick off work (such as looking up an agent's models).
	onSelect func(value string) tea.Cmd
}

func newPicker(title string, items []pickerItem, current string, onSelect func(string) tea.Cmd) *picker {
	p := &picker{title: title, items: items, current: current, onSelect: onSelect}
	p.refilter()
	// Open on whatever is in effect, so the list starts where you are.
	for i, idx := range p.match {
		if items[idx].value == current {
			p.cursor = i
			break
		}
	}
	p.scrollToCursor()
	return p
}

// refilter recomputes the visible subset. A substring match is enough: these
// lists are short and the names are long.
func (p *picker) refilter() {
	q := strings.ToLower(p.query)
	p.match = p.match[:0]
	for i, it := range p.items {
		if q == "" || strings.Contains(strings.ToLower(it.label), q) {
			p.match = append(p.match, i)
		}
	}
	if p.cursor >= len(p.match) {
		p.cursor = len(p.match) - 1
	}
	if p.cursor < 0 {
		p.cursor = 0
	}
	p.scrollToCursor()
}

func (p *picker) move(delta int) {
	if len(p.match) == 0 {
		return
	}
	p.cursor += delta
	// Wrap: a short list is faster to reach from either end.
	if p.cursor < 0 {
		p.cursor = len(p.match) - 1
	}
	if p.cursor >= len(p.match) {
		p.cursor = 0
	}
	p.scrollToCursor()
}

func (p *picker) scrollToCursor() {
	visible := p.visibleRows()
	if p.cursor < p.top {
		p.top = p.cursor
	}
	if p.cursor >= p.top+visible {
		p.top = p.cursor - visible + 1
	}
	if p.top < 0 {
		p.top = 0
	}
}

func (p *picker) visibleRows() int {
	if n := len(p.match); n < pickerMaxVisible {
		if n < 1 {
			return 1 // the "no match" line
		}
		return n
	}
	return pickerMaxVisible
}

// rows is the height the picker occupies, border included, so the layout can
// budget for it exactly.
func (p *picker) rows() int { return p.visibleRows() + pickerChrome }

func (p *picker) selected() (pickerItem, bool) {
	if p.cursor < 0 || p.cursor >= len(p.match) {
		return pickerItem{}, false
	}
	return p.items[p.match[p.cursor]], true
}

// handleKey applies one keypress. It reports whether the picker should close,
// and any command the selection produced.
func (p *picker) handleKey(msg tea.KeyMsg) (close bool, cmd tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		return true, nil
	case tea.KeyEnter:
		it, ok := p.selected()
		if !ok {
			return false, nil // nothing to pick; keep the list open
		}
		if p.onSelect != nil {
			return true, p.onSelect(it.value)
		}
		return true, nil
	case tea.KeyUp, tea.KeyShiftTab:
		p.move(-1)
	case tea.KeyDown, tea.KeyTab:
		p.move(1)
	case tea.KeyPgUp:
		p.move(-pickerMaxVisible)
	case tea.KeyPgDown:
		p.move(pickerMaxVisible)
	case tea.KeyHome:
		p.cursor, p.top = 0, 0
	case tea.KeyEnd:
		p.cursor = len(p.match) - 1
		p.scrollToCursor()
	case tea.KeyBackspace:
		if p.query != "" {
			r := []rune(p.query)
			p.query = string(r[:len(r)-1])
			p.refilter()
		}
	case tea.KeyRunes, tea.KeySpace:
		s := string(msg.Runes)
		if msg.Type == tea.KeySpace {
			s = " "
		}
		p.query += s
		p.refilter()
	}
	return false, nil
}

// ── rendering ──────────────────────────────────────────────────────────────────

// renderPicker draws the list exactly m.width columns wide and p.rows() tall.
func (m *Model) renderPicker() string {
	p := m.picker
	if p == nil {
		return ""
	}
	w := m.width
	inner := w - 2 // inside the border runes
	bc := stylePickerBorder

	// Title row carries the live filter, so you can see what you have typed.
	title := p.title
	if p.query != "" {
		title += "  ▸ " + p.query
	}
	label := "─ " + truncate(title, max(4, inner-14)) + " "
	countTag := ""
	if len(p.items) > 0 {
		countTag = fmt.Sprintf(" %d/%d ", len(p.match), len(p.items))
	}
	fill := inner - lipgloss.Width(label) - lipgloss.Width(countTag)
	if fill < 0 {
		fill = 0
	}
	top := bc.Render("╭"+label) + bc.Render(strings.Repeat("─", fill)) +
		styleMuted.Render(countTag) + bc.Render("╮")

	var rows []string
	if len(p.match) == 0 {
		rows = append(rows, m.pickerRow(bc, inner, styleMuted.Render("  no match — backspace to widen"), false))
	}
	visible := p.visibleRows()
	for i := p.top; i < p.top+visible && i < len(p.match); i++ {
		it := p.items[p.match[i]]
		selected := i == p.cursor

		// A fixed two-column gutter keeps every label starting at the same
		// place, so the list reads as a column rather than a ragged edge.
		marker := "  "
		name := it.label
		if selected {
			marker = stylePickerSel.Render("▸ ")
			name = stylePickerSel.Render(it.label)
		}
		line := marker + name
		if it.hint != "" {
			line += "  " + styleMuted.Render(it.hint)
		}
		if it.value == p.current {
			line += "  " + styleTaskDone.Render("● in use")
		}
		rows = append(rows, m.pickerRow(bc, inner, " "+line, selected))
	}

	footer := p.footer
	if footer == "" {
		footer = "↑↓ move · type to filter · enter select · esc cancel"
	}
	bottomLabel := "─ " + truncate(footer, max(4, inner-4)) + " "
	bfill := inner - lipgloss.Width(bottomLabel)
	if bfill < 0 {
		bfill = 0
	}
	bottom := bc.Render("╰") + styleMuted.Render(bottomLabel) + bc.Render(strings.Repeat("─", bfill)+"╯")

	return top + "\n" + strings.Join(rows, "\n") + "\n" + bottom + "\n"
}

// pickerRow pads one row to the border, highlighting the whole line when selected.
func (m *Model) pickerRow(bc lipgloss.Style, inner int, content string, selected bool) string {
	pad := inner - lipgloss.Width(content)
	if pad < 0 {
		content = lipgloss.NewStyle().MaxWidth(inner).Render(content)
		pad = inner - lipgloss.Width(content)
		if pad < 0 {
			pad = 0
		}
	}
	return bc.Render("│") + content + strings.Repeat(" ", pad) + bc.Render("│")
}
