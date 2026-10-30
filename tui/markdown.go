package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Agent replies are markdown. Rendering them as flat prose makes the single
// most important distinction for a programmer — code versus explanation —
// invisible: a shell command, a file path and a sentence all looked the same.
//
// This handles the small subset agents actually emit: fenced code blocks,
// inline code, bold, headings and bullets. It is deliberately not a markdown
// library: glamour reflows text and would fight the exact-width layout
// contract in view.go, and 80% of the readability comes from code blocks
// alone. Anything unrecognised is passed through untouched.
//
// Every returned line is at most width columns, because the layout contract
// depends on it (TestAgentBodyRespectsWidth pins that).

// renderAgentBody styles one agent message for a panel `width` columns wide.
func renderAgentBody(body string, width int) string {
	if width < 8 {
		width = 8
	}
	var out []string
	inFence := false

	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)

		// Fence delimiters: show the language as a quiet label rather than ```.
		if strings.HasPrefix(trimmed, "```") {
			lang := strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
			// Frame the block symmetrically: a full-width rule top and bottom,
			// with the language as a label. The rule is what marks the block
			// when colour is unavailable (NO_COLOR, piped output).
			if !inFence {
				label := lang
				if label == "" {
					label = "code"
				}
				out = append(out, styleCodeFence.Render(rule("── "+label+" ", width)))
			} else {
				out = append(out, styleCodeFence.Render(rule("", width)))
			}
			inFence = !inFence
			continue
		}

		if inFence {
			// Code is clipped, not wrapped: a wrapped line of code is harder to
			// read than a truncated one, and /copy or /export gives the full text.
			out = append(out, styleCodeBlock.Render(padRight(truncate(line, width), width)))
			continue
		}

		if trimmed == "" {
			out = append(out, "")
			continue
		}

		// Headings: agents use them to structure plans.
		if text, ok := splitHeading(trimmed); ok {
			out = append(out, wrapTo(styleMdHeading.Render(text), width))
			continue
		}

		// Bullets and numbered items: keep the marker, hang the continuation.
		if marker, rest, ok := splitListItem(line); ok {
			indent := strings.Repeat(" ", lipgloss.Width(marker))
			wrapped := wrapTo(renderInline(rest), width-lipgloss.Width(marker))
			lines := strings.Split(wrapped, "\n")
			for i, l := range lines {
				if i == 0 {
					out = append(out, styleMdBullet.Render(marker)+l)
				} else {
					out = append(out, indent+l)
				}
			}
			continue
		}

		out = append(out, wrapTo(renderInline(line), width))
	}
	return strings.Join(out, "\n")
}

// splitHeading recognises "# text" through "#### text".
func splitHeading(trimmed string) (text string, ok bool) {
	rest := strings.TrimLeft(trimmed, "#")
	level := len(trimmed) - len(rest)
	if level == 0 || level > 4 {
		return "", false
	}
	text = strings.TrimSpace(rest)
	return text, text != ""
}

// splitListItem recognises "- x", "* x", "• x" and "1. x".
func splitListItem(line string) (marker, rest string, ok bool) {
	lead := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
	body := strings.TrimLeft(line, " \t")
	for _, m := range []string{"- ", "* ", "• "} {
		if strings.HasPrefix(body, m) {
			return lead + "• ", strings.TrimPrefix(body, m), true
		}
	}
	// "12. text"
	for i := 0; i < len(body) && i < 3; i++ {
		if body[i] >= '0' && body[i] <= '9' {
			continue
		}
		if body[i] == '.' && i > 0 && i+1 < len(body) && body[i+1] == ' ' {
			return lead + body[:i+2], body[i+2:], true
		}
		break
	}
	return "", "", false
}

// renderInline styles `code` and **bold** spans inside a line of prose.
func renderInline(line string) string {
	line = styleSpans(line, "`", styleInlineCode)
	line = styleSpans(line, "**", styleMdBold)
	return line
}

// styleSpans wraps text between matched delimiters in st. Unmatched delimiters
// are left exactly as they are, so a lone backtick in prose is not swallowed.
func styleSpans(s, delim string, st lipgloss.Style) string {
	var b strings.Builder
	for {
		i := strings.Index(s, delim)
		if i == -1 {
			b.WriteString(s)
			return b.String()
		}
		rest := s[i+len(delim):]
		j := strings.Index(rest, delim)
		if j == -1 {
			b.WriteString(s)
			return b.String() // no closing delimiter: leave the line alone
		}
		inner := rest[:j]
		if strings.TrimSpace(inner) == "" {
			// "``" or "****": not a span, emit literally and move on.
			b.WriteString(s[:i+len(delim)])
			s = rest
			continue
		}
		b.WriteString(s[:i])
		b.WriteString(st.Render(inner))
		s = rest[j+len(delim):]
	}
}

// rule renders a label followed by dashes out to width columns.
func rule(label string, width int) string {
	label = truncate(label, width)
	if pad := width - lipgloss.Width(label); pad > 0 {
		return label + strings.Repeat("─", pad)
	}
	return label
}

// wrapTo wraps s to width columns. lipgloss's wrap is ANSI-aware, so styled
// spans survive it and every line still measures width or less.
func wrapTo(s string, width int) string {
	if width < 4 {
		width = 4
	}
	return lipgloss.NewStyle().Width(width).Render(s)
}
