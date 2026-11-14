package tui

import (
	"io"
	"os"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

// Mouse reporting makes the terminal send an escape sequence for every wheel
// click: "\x1b[<65;80;24M". Bubble Tea reads the terminal 256 bytes at a time
// (key.go: `var buf [256]byte`), so a fast scroll — which produces far more
// than 256 bytes at once — puts a read boundary in the middle of one of those
// sequences.
//
// Bubble Tea can recover from a key sequence split that way, but not a mouse
// one: detectOneMsg() only consults `canHaveMoreData` on the rune path, never
// in the mouse branch. So the orphaned halves fall through to the rune parser
// and are delivered as ordinary KeyRunes messages — "\x1b[<65;8" becomes
// alt+[ plus the runes "<65;8", and the next read's ";24M" becomes four more.
// They land in whatever has focus, which is how digits, semicolons and stray
// Ms got typed into the chat input while scrolling.
//
// safeInput sits between the terminal and Bubble Tea and never hands over a
// sequence it has cut in half.

const esc = 0x1b

// escDelay is how long to wait for the rest of a sequence that a read boundary
// cut in half. A terminal emits a sequence in one burst, so the remainder is
// already in flight and arrives in microseconds; a lone Esc keypress has
// nothing behind it and falls through once the wait expires. This is what
// curses calls ESCDELAY, with a far shorter delay.
const escDelay = 20 * time.Millisecond

// TerminalInput is the reader to give tea.WithInput. It keeps the *os.File
// identity Bubble Tea needs — raw mode (term.File) and cancellable reads
// (cancelreader.File) both type-assert for Fd() — and only replaces Read.
type TerminalInput struct {
	*os.File
	safe *safeInput
}

// SafeInput wraps a terminal so split mouse sequences cannot reach the parser.
func SafeInput(f *os.File) *TerminalInput {
	return &TerminalInput{File: f, safe: newSafeInput(f)}
}

func (t *TerminalInput) Read(p []byte) (int, error) { return t.safe.Read(p) }

type safeInput struct {
	src io.Reader
	// more reports whether further input is already waiting, which is how a
	// sequence still in flight is told apart from a lone Esc keypress.
	more func() bool
	// skip means we dropped the head of a mouse report and are discarding the
	// rest of it. Nothing is ever buffered between calls: the cancelreader
	// waits on the file descriptor before each Read, so bytes held here would
	// sit unread until the next keypress.
	skip bool
}

func newSafeInput(src io.Reader) *safeInput {
	s := &safeInput{src: src}
	if f, ok := src.(interface{ Fd() uintptr }); ok {
		fd := int32(f.Fd())
		s.more = func() bool { return readable(fd, escDelay) }
	}
	return s
}

// readable reports whether the descriptor has input waiting, waiting up to d.
func readable(fd int32, d time.Duration) bool {
	fds := []unix.PollFd{{Fd: fd, Events: unix.POLLIN}}
	ms := int(d.Milliseconds())
	for {
		n, err := unix.Poll(fds, ms)
		if err == unix.EINTR {
			continue
		}
		return err == nil && n > 0
	}
}

func (s *safeInput) waiting() bool { return s.more != nil && s.more() }

func (s *safeInput) Read(p []byte) (int, error) {
	n := 0
	for {
		m, err := s.src.Read(p[n:])
		if m == 0 {
			if err != nil {
				return 0, err
			}
			continue
		}
		n += m
		b := p[:n]

		if s.skip {
			end := mouseTailEnd(b)
			if end < 0 {
				n = 0 // the whole chunk was tail; read on
				if err != nil {
					return 0, err
				}
				continue
			}
			s.skip = false
			// Shift what follows to the front; copy is memmove-safe.
			n = copy(p, b[end:])
			if n == 0 {
				if err != nil {
					return 0, err
				}
				continue
			}
			b = p[:n]
		}

		cut := completeSequences(b)
		if cut == n {
			return n, err
		}

		// Something straddles the end of the buffer. Wait briefly for the rest
		// and finish the sequence here, where there is still room for it.
		//
		// Handing the fragment over instead is what leaked: a short read tells
		// Bubble Tea it is looking at an event boundary, which switches off its
		// own deferral, and the halves then arrive as keystrokes.
		if n < len(p) && err == nil && s.waiting() {
			continue
		}

		if isSGRMousePrefix(b[cut:]) {
			// The buffer is full, or the rest never came. Drop the report:
			// losing one tick out of the burst that caused the split is
			// invisible, whereas its halves arrive as typed text.
			s.skip = true
			if cut > 0 {
				return cut, err
			}
			n = 0
			if err != nil {
				return 0, err
			}
			continue
		}

		// A lone Esc, or a key sequence filling the whole buffer. Bubble Tea
		// gets both of those right on its own.
		return n, err
	}
}

// completeSequences returns the length of the longest prefix of b that holds
// only whole sequences and whole runes.
func completeSequences(b []byte) int {
	i := 0
	for i < len(b) {
		w := sequenceLen(b[i:])
		if w == 0 {
			return i
		}
		i += w
	}
	return i
}

// sequenceLen returns the length of the sequence at the start of b, or 0 if b
// ends before the sequence does. b must not be empty.
func sequenceLen(b []byte) int {
	if b[0] != esc {
		if b[0] < utf8.RuneSelf {
			return 1
		}
		if !utf8.FullRune(b) {
			return 0 // a multi-byte rune cut in half
		}
		_, w := utf8.DecodeRune(b)
		return w
	}
	if len(b) == 1 {
		return 0 // a lone trailing ESC: ambiguous, so not yet complete
	}
	switch b[1] {
	case '[':
		return csiLen(b)
	case 'O': // SS3, e.g. ESC O P for F1
		if len(b) < 3 {
			return 0
		}
		return 3
	case 'P', ']', '^', '_': // DCS, OSC, PM, APC: run until ST or BEL
		return stringSeqLen(b)
	case esc:
		return 1 // treat the first ESC as its own key
	default:
		w := sequenceLen(b[1:]) // ESC + key = the alt modifier
		if w == 0 {
			return 0
		}
		return 1 + w
	}
}

// csiLen measures "ESC [ ... final", where final is 0x40-0x7e.
func csiLen(b []byte) int {
	// X10 mouse reporting is "ESC [ M" plus exactly three raw bytes, which may
	// themselves fall in the final-byte range, so it has to be special-cased.
	if len(b) >= 3 && b[2] == 'M' {
		if len(b) < 6 {
			return 0
		}
		return 6
	}
	for i := 2; i < len(b); i++ {
		switch c := b[i]; {
		case c >= 0x30 && c <= 0x3f: // parameter bytes
		case c >= 0x20 && c <= 0x2f: // intermediate bytes
		case c >= 0x40 && c <= 0x7e: // final byte
			return i + 1
		default:
			return i // malformed; whatever precedes it is all we can claim
		}
	}
	return 0
}

func stringSeqLen(b []byte) int {
	for i := 2; i < len(b); i++ {
		if b[i] == 0x07 { // BEL
			return i + 1
		}
		if b[i] == esc {
			if i+1 >= len(b) {
				return 0
			}
			if b[i+1] == '\\' { // ST
				return i + 2
			}
		}
	}
	return 0
}

// isSGRMousePrefix reports whether b is the beginning of an unfinished SGR
// mouse report. "ESC" and "ESC [" alone are not: they could be any key, and
// Bubble Tea handles those correctly on its own.
func isSGRMousePrefix(b []byte) bool {
	if len(b) < 3 || b[0] != esc || b[1] != '[' || b[2] != '<' {
		return false
	}
	for _, c := range b[3:] {
		if (c < '0' || c > '9') && c != ';' {
			return false
		}
	}
	return true
}

// mouseTailEnd returns the index just past the end of a mouse report whose
// head was dropped, or -1 if the whole of b is still inside it. If b turns out
// not to look like the rest of a mouse report, it reports where real input
// resumes, so a mis-sync cannot eat keystrokes.
func mouseTailEnd(b []byte) int {
	for i, c := range b {
		switch {
		case c >= '0' && c <= '9', c == ';':
		case c == 'M' || c == 'm':
			return i + 1
		default:
			return i
		}
	}
	return -1
}
