package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// withColor forces styling on: lipgloss emits no escape codes when stdout is
// not a terminal, which it never is under `go test`. Without this, assertions
// about styling would pass or fail depending on the environment rather than
// the code.
func withColor(t *testing.T) {
	t.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
}

// The layout contract depends on this: a rendered agent body must never be
// wider than the panel it goes in, whatever markdown it contains.
func TestAgentBodyRespectsWidth(t *testing.T) {
	bodies := []string{
		"A short line.",
		strings.Repeat("a very long sentence that must wrap repeatedly ", 12),
		"Run `go test ./... -race -count=1 -timeout 260s` before pushing anything at all.",
		"```go\nfunc main() { fmt.Println(\"a line of code far longer than any narrow panel could ever hope to show\") }\n```",
		"## A heading that is itself long enough to need wrapping in a narrow panel\nbody text",
		"- a bullet whose text is long enough that it has to wrap onto following lines\n- short one",
		"1. numbered item with quite a lot of text in it so that wrapping is required\n2. second",
		"**bold text that goes on and on and needs to wrap because it is very long indeed**",
		"unclosed `backtick and **unclosed bold",
		"```\nno language fence\n```",
		"",
	}
	for _, w := range []int{20, 30, 48, 78} {
		for i, body := range bodies {
			out := renderAgentBody(body, w)
			for ln, line := range strings.Split(out, "\n") {
				if got := lipgloss.Width(line); got > w {
					t.Errorf("body %d at width %d: line %d is %d cols\n  %q",
						i, w, ln+1, got, stripANSI(line))
				}
			}
		}
	}
}

func TestFencedCodeIsStyledAndPlainTextIsNot(t *testing.T) {
	withColor(t)
	out := renderAgentBody("before\n```sh\ngo build ./...\n```\nafter", 40)
	plain := stripANSI(out)

	if !strings.Contains(plain, "go build ./...") {
		t.Fatalf("code content lost: %q", plain)
	}
	// The ``` markers are replaced with a quiet label.
	if strings.Contains(plain, "```") {
		t.Errorf("raw fence markers left in output: %q", plain)
	}
	if !strings.Contains(plain, "sh") {
		t.Errorf("fence language not shown: %q", plain)
	}

	// The code line must carry styling that the prose lines do not.
	var codeLine, proseLine string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(stripANSI(l), "go build") {
			codeLine = l
		}
		if strings.TrimSpace(stripANSI(l)) == "before" {
			proseLine = l
		}
	}
	if !strings.Contains(codeLine, "\x1b[") {
		t.Error("code line is not styled")
	}
	if strings.Contains(proseLine, "\x1b[") {
		t.Errorf("plain prose should not be styled: %q", proseLine)
	}
}

func TestInlineCodeAndBold(t *testing.T) {
	withColor(t)
	out := renderAgentBody("use `parseConfig` and **check** it", 60)
	if !strings.Contains(out, "\x1b[") {
		t.Fatal("inline spans were not styled")
	}
	plain := stripANSI(out)
	// Delimiters are consumed, content kept.
	if strings.Contains(plain, "`") || strings.Contains(plain, "**") {
		t.Errorf("delimiters left in output: %q", plain)
	}
	for _, want := range []string{"parseConfig", "check"} {
		if !strings.Contains(plain, want) {
			t.Errorf("content lost: %q missing from %q", want, plain)
		}
	}
}

// An unmatched delimiter must be left alone, not swallow the rest of the line.
func TestUnmatchedDelimitersArePreserved(t *testing.T) {
	plain := stripANSI(renderAgentBody("a lone ` backtick stays", 60))
	if !strings.Contains(plain, "`") {
		t.Errorf("lone backtick was eaten: %q", plain)
	}
	plain = stripANSI(renderAgentBody("empty `` span", 60))
	if !strings.Contains(plain, "empty") || !strings.Contains(plain, "span") {
		t.Errorf("empty span broke the line: %q", plain)
	}
}

func TestHeadingsAndBullets(t *testing.T) {
	plain := stripANSI(renderAgentBody("## Execution\n- first\n* second\n3. third", 60))
	if strings.Contains(plain, "#") {
		t.Errorf("heading markers left in: %q", plain)
	}
	if !strings.Contains(plain, "Execution") {
		t.Errorf("heading text lost: %q", plain)
	}
	// Bullets are normalised to one marker.
	if n := strings.Count(plain, "•"); n != 2 {
		t.Errorf("expected 2 bullets, got %d: %q", n, plain)
	}
	if !strings.Contains(plain, "3. third") {
		t.Errorf("numbered item lost its marker: %q", plain)
	}
}

func TestSplitHeading(t *testing.T) {
	for _, c := range []struct {
		in   string
		text string
		ok   bool
	}{
		{"# One", "One", true},
		{"#### Four", "Four", true},
		{"##### Five", "", false}, // too deep
		{"no heading", "", false},
		{"#", "", false}, // no text
	} {
		text, ok := splitHeading(c.in)
		if ok != c.ok || text != c.text {
			t.Errorf("splitHeading(%q) = %q,%v want %q,%v", c.in, text, ok, c.text, c.ok)
		}
	}
}

// A fence that is never closed must not lose the remaining text.
func TestUnclosedFenceKeepsContent(t *testing.T) {
	plain := stripANSI(renderAgentBody("intro\n```go\ncode line\nmore code", 40))
	for _, want := range []string{"intro", "code line", "more code"} {
		if !strings.Contains(plain, want) {
			t.Errorf("lost %q: %q", want, plain)
		}
	}
}
