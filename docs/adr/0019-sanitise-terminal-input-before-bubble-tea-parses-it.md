# ADR-0019: Sanitise terminal input before Bubble Tea parses it

- **Status:** Accepted
- **Date:** 2026-11-14
- **Deciders:** Rinil Parmar
- **Related:** [ADR-0001](0001-record-architecture-decisions.md)

## Context

Scrolling the chat typed characters into the input box:

> "when i scroll the chat up and down some random character are written in
> input box"

An earlier pass treated this as two separate TUI faults — `Esc` logging
"Nothing to cancel." into the transcript, and `GotoBottom()` fighting the
scroll position. Both were real, both were fixed, and neither was this bug.
The characters kept appearing.

The cause is in Bubble Tea's input parser, not in Aros. With mouse reporting
on, each wheel click sends an SGR mouse report — `\x1b[<64;80;24M`, twelve
bytes. Bubble Tea reads the terminal 256 bytes at a time (`key.go`:
`var buf [256]byte`), so a fast scroll, which produces far more than 256 bytes
at once, puts a read boundary inside one of those reports.

Bubble Tea recovers from a *key* sequence split that way: `detectOneMsg` returns
a zero width and the outer loop stitches the halves together on the next read.
It cannot recover from a *mouse* sequence split that way, because the mouse
branch never consults `canHaveMoreData`. The orphaned halves fall through to the
rune parser and arrive as ordinary `KeyRunes` messages: `\x1b[<64;8` becomes
`alt+[` plus the runes `<64;8`, and the next read's `0;24M` becomes five more.
They land in whatever has focus — the chat input.

Reproduced through the real parser, a 40-click burst yields 39 mouse events and
two phantom keystrokes, `alt+[` and `<64;80;24M`. In a pty, the pre-fix binary
renders `❯ project name <64;80;24M`.

## Decision

Interpose a reader between the terminal and Bubble Tea (`tui/input.go`,
`tea.WithInput`) that never hands over a sequence it has cut in half.

- Hand over only whole sequences. `completeSequences` measures the longest
  prefix of a buffer that holds complete escape sequences and complete runes.
- When something straddles the end of the buffer, wait up to 20 ms for the rest
  and finish the sequence here, where there is still room for it. A terminal
  emits a sequence in one burst, so the remainder is already in flight; a lone
  `Esc` has nothing behind it and falls through once the wait expires. This is
  what curses calls `ESCDELAY`, with a much shorter delay.
- If the rest never comes, or the buffer is already full, drop a half mouse
  report and discard its tail. Losing one tick out of the burst that caused the
  split is invisible; its halves are not.
- Buffer nothing between calls. The cancelreader waits on the file descriptor
  before each read, so bytes held back would sit unread until the next
  keypress — a held `Esc` is worse than a late one.
- Pass a straddling *key* sequence through untouched: Bubble Tea gets those
  right on its own, and second-guessing it would risk swallowing a keypress.

Also switch `tea.WithMouseAllMotion` to `tea.WithMouseCellMotion`. The TUI
consumes only wheel up and wheel down; all-motion reporting sends a sequence
for every pixel the mouse moves, and that flood is what made the split routine
rather than rare.

Wrap only a real terminal. Naming an input of our own also disables Bubble
Tea's "stdin is not a tty, so open `/dev/tty`" fallback, which is what keeps
`aros < /dev/null` and piped invocations working.

## Consequences

- Scrolling no longer types into the input. Verified in a pty against the real
  binary: a 1440-byte burst, delivered both as one write and as one write per
  wheel click, produces no visible text, where the pre-fix binary produces
  `<64;80;24M` in the input box.
- A violent scroll can lose a single wheel tick when the rest of a report does
  not arrive within 20 ms. The scroll still moves; one tick out of dozens does
  not read as a dropped input.
- `Esc` is delivered up to 20 ms late. It is the universal cancel key, so this
  was the constraint the design had to respect, not a detail: dropping or
  holding a lone `Esc` was never acceptable.
- A dependency, not Aros, owns the defect. `tui/input.go` is a workaround with
  a stated expiry: `TestBubbleteaTurnsSplitMouseReportsIntoKeystrokes` asserts
  that the upstream bug is still there, so if a future Bubble Tea fixes it the
  test fails and the workaround can be deleted.

## Notes

While reproducing this in a pty, two further things surfaced that are *not*
Aros bugs, recorded so the next person does not chase them:

- Bubble Tea's `init()` calls `lipgloss.HasDarkBackground()` on purpose
  (`tea_init.go`), so the terminal is queried before any Program acquires it.
  On a terminal that answers neither `OSC 11` nor `CSI 6n`, every `aros`
  command — `--help` included — blocks for `termenv.OSCTimeout` (5 s) during
  package initialisation and swallows the first bytes of input that arrive
  meanwhile. Real terminals answer in microseconds. A test harness must answer
  the query, or it will blame the application for its own silence.
- A test harness reader that drops the remainder of a chunk larger than the
  buffer it was given will under-feed the parser and hide the very split it is
  meant to produce.
