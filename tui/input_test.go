package tui

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// One SGR mouse report: wheel-up at column 80, row 24. Twelve bytes, so a
// 256-byte read boundary lands inside the 22nd one.
const wheelUp = "\x1b[<64;80;24M"

func TestSequenceLen(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"plain byte", "a", 1},
		{"utf8 rune", "é!", 2},
		{"truncated utf8 rune", "\xc3", 0},
		{"lone esc", "\x1b", 0},
		{"alt+key", "\x1ba", 2},
		{"arrow up", "\x1b[A", 3},
		{"arrow up then text", "\x1b[Ahi", 3},
		{"truncated csi", "\x1b[", 0},
		{"truncated csi params", "\x1b[1;5", 0},
		{"sgr mouse", wheelUp, 12},
		{"truncated sgr mouse", "\x1b[<64;80;2", 0},
		{"x10 mouse", "\x1b[M !!", 6},
		{"truncated x10 mouse", "\x1b[M !", 0},
		{"ss3 f1", "\x1bOP", 3},
		{"truncated ss3", "\x1bO", 0},
		{"osc with bel", "\x1b]11;?\x07", 7},
		{"truncated osc", "\x1b]11;?", 0},
		{"bracketed paste start", "\x1b[200~", 6},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sequenceLen([]byte(c.in)); got != c.want {
				t.Errorf("sequenceLen(%q) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}

func TestCompleteSequences(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"all whole", wheelUp + wheelUp, 24},
		{"whole then split", wheelUp + "\x1b[<64;80", 12},
		{"typed text then split arrow", "hi\x1b[", 2},
		{"nothing whole", "\x1b[<64", 0},
		{"empty", "", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := completeSequences([]byte(c.in)); got != c.want {
				t.Errorf("completeSequences(%q) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}

// chunkReader hands out exactly the chunks it was given, one per Read, then
// blocks forever — a terminal that has gone quiet, not one at EOF.
type chunkReader struct {
	chunks [][]byte
	i      int
	block  chan struct{}
}

// hasMore stands in for polling the file descriptor: it reports whether more
// input is already waiting.
func (r *chunkReader) hasMore() bool {
	for _, c := range r.chunks[min(r.i, len(r.chunks)):] {
		if len(c) > 0 {
			return true
		}
	}
	return false
}

// newTestInput wires safeInput to the reader the way SafeInput wires it to a
// terminal, so tests exercise the wait-for-the-rest path.
func newTestInput(r *chunkReader) *safeInput {
	return &safeInput{src: r, more: r.hasMore}
}

func (r *chunkReader) Read(p []byte) (int, error) {
	for r.i < len(r.chunks) && len(r.chunks[r.i]) == 0 {
		r.i++
	}
	if r.i >= len(r.chunks) {
		if r.block != nil {
			<-r.block
		}
		return 0, io.EOF
	}
	// A chunk longer than p is consumed over several reads, the way a terminal
	// hands over a burst larger than the reader's buffer.
	n := copy(p, r.chunks[r.i])
	r.chunks[r.i] = r.chunks[r.i][n:]
	return n, nil
}

// drain reads r to exhaustion the way Bubble Tea does: 256 bytes at a time.
func drain(t *testing.T, r io.Reader) string {
	t.Helper()
	var out bytes.Buffer
	buf := make([]byte, 256)
	for {
		n, err := r.Read(buf)
		out.Write(buf[:n])
		if err != nil {
			return out.String()
		}
	}
}

// A mouse report cut in half by a read boundary must not reach the parser as
// two pieces: those pieces are what got typed into the chat input. When the
// rest of it is already in flight, it is waited for and nothing is lost.
func TestSafeInputCompletesSplitMouseReport(t *testing.T) {
	split := wheelUp + wheelUp[:5] // second report cut after "\x1b[<64"
	rest := wheelUp[5:]            // ";80;24M" arrives on the next read
	got := drain(t, newTestInput(&chunkReader{chunks: [][]byte{[]byte(split), []byte(rest + wheelUp + "q")}}))

	if want := wheelUp + wheelUp + wheelUp + "q"; got != want {
		t.Errorf("safeInput produced %q, want %q", got, want)
	}
	if n := completeSequences([]byte(got)); n != len(got) {
		t.Errorf("output still contains a partial sequence: %q (whole prefix %d of %d)", got, n, len(got))
	}
}

// If the rest of a cut mouse report never arrives, the report is dropped and
// its tail discarded, rather than delivered as text.
func TestSafeInputDropsAbandonedMouseReport(t *testing.T) {
	split := wheelUp + wheelUp[:5]
	// more() reports nothing waiting, so the wait expires on every boundary.
	r := &safeInput{src: &chunkReader{chunks: [][]byte{[]byte(split), []byte(wheelUp[5:] + wheelUp + "q")}}}
	got := drain(t, r)

	if want := wheelUp + wheelUp + "q"; got != want {
		t.Errorf("safeInput produced %q, want %q", got, want)
	}
	if n := completeSequences([]byte(got)); n != len(got) {
		t.Errorf("output still contains a partial sequence: %q", got)
	}
}

// A keypress split across reads must be passed through untouched: Bubble Tea
// rejoins those itself, and dropping one would eat a real keystroke.
func TestSafeInputKeepsSplitKeySequence(t *testing.T) {
	got := drain(t, newTestInput(&chunkReader{chunks: [][]byte{[]byte("ab\x1b["), []byte("A")}}))
	if want := "ab\x1b[A"; got != want {
		t.Errorf("safeInput produced %q, want %q", got, want)
	}
	// And the arrow key must arrive whole, in one piece, not as alt+[ then "A".
	if got != "ab\x1b[A" {
		t.Errorf("arrow key was split: %q", got)
	}
}

// A lone Esc arrives as a single byte with nothing following it. It must come
// straight through, or the universal-cancel key would stop working.
func TestSafeInputPassesLoneEscape(t *testing.T) {
	r := &safeInput{src: &chunkReader{chunks: [][]byte{[]byte("\x1b")}}}
	buf := make([]byte, 256)
	n, _ := r.Read(buf)
	if got := string(buf[:n]); got != "\x1b" {
		t.Errorf("lone escape came through as %q", got)
	}
}

// ── end-to-end through Bubble Tea's own parser ────────────────────────────────

type recorder struct {
	mu    sync.Mutex
	keys  []string
	mice  int
	last  time.Time
	start time.Time
}

func (c *recorder) touch() { c.last = time.Now() }

type recordModel struct{ c *recorder }

func (m recordModel) Init() tea.Cmd { return nil }
func (m recordModel) View() string  { return "" }
func (m recordModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		m.c.mu.Lock()
		m.c.keys = append(m.c.keys, msg.String())
		m.c.touch()
		m.c.mu.Unlock()
	case tea.MouseMsg:
		m.c.mu.Lock()
		m.c.mice++
		m.c.touch()
		m.c.mu.Unlock()
	}
	return m, nil
}

// feedScrollBurst runs a real Bubble Tea program over `input` and reports what
// the program saw. Bubble Tea reads its input 256 bytes at a time, so a burst
// this long is split mid-sequence exactly as a fast scroll splits it.
func feedScrollBurst(t *testing.T, wrap bool) (keys []string, mice int) {
	t.Helper()
	const bursts = 40
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })

	var in io.Reader = &chunkReader{ //nolint:staticcheck
		chunks: [][]byte{[]byte(strings.Repeat(wheelUp, bursts))},
		block:  block,
	}
	if wrap {
		in = newTestInput(in.(*chunkReader))
	}

	c := &recorder{start: time.Now()}
	c.touch()
	p := tea.NewProgram(recordModel{c: c},
		tea.WithInput(in),
		tea.WithoutRenderer(),
		tea.WithoutSignalHandler(),
	)
	done := make(chan struct{})
	go func() { _, _ = p.Run(); close(done) }()

	// Wait for the input to be consumed: quiet for 250ms, or 5s hard stop.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
		c.mu.Lock()
		quiet := time.Since(c.last) > 250*time.Millisecond
		c.mu.Unlock()
		if quiet {
			break
		}
	}
	p.Quit()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("program did not exit")
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.keys...), c.mice
}

// The bug, reproduced through the real parser: scrolling types into the input.
// Bubble Tea v1.3.10 cannot defer a mouse sequence that a read boundary cut in
// half (detectOneMsg only honours canHaveMoreData on the rune path), so the
// halves are delivered as keystrokes. If this test ever starts failing because
// no keys arrive, the upstream bug is fixed and safeInput can be deleted.
func TestBubbleteaTurnsSplitMouseReportsIntoKeystrokes(t *testing.T) {
	keys, mice := feedScrollBurst(t, false)
	if mice == 0 {
		t.Fatal("no mouse events parsed at all — test setup is wrong")
	}
	if len(keys) == 0 {
		t.Fatalf("expected the upstream split-sequence bug to produce stray keys; it did not (%d mouse events). "+
			"If bubbletea has fixed this, drop safeInput and this test.", mice)
	}
	t.Logf("unguarded: %d mouse events and %d stray keystrokes %q", mice, len(keys), keys)
}

// The fix: the same burst produces only mouse events.
func TestScrollBurstDoesNotTypeIntoInput(t *testing.T) {
	keys, mice := feedScrollBurst(t, true)
	if mice == 0 {
		t.Fatal("no mouse events parsed at all — test setup is wrong")
	}
	if len(keys) != 0 {
		t.Errorf("scrolling produced %d keystrokes %q — they would be typed into the chat input", len(keys), keys)
	}
}
