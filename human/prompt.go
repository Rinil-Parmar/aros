package human

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"golang.org/x/term"
)

// stdin is shared across all prompts. A fresh bufio.Reader per call would
// buffer ahead and silently swallow input typed (or piped) for the next prompt.
var (
	stdin   = bufio.NewReader(os.Stdin)
	stdinMu sync.Mutex // concurrent work tasks may ask the human — one at a time
)

func readLine() (string, error) {
	stdinMu.Lock()
	defer stdinMu.Unlock()
	line, err := stdin.ReadString('\n')
	if err != nil {
		if err == io.EOF && line != "" {
			return strings.TrimSpace(line), nil
		}
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// Confirm prints question and blocks until user enters y/yes or n/no.
// Returns true for yes, false for no. Other input re-prompts.
func Confirm(question string) (bool, error) {
	for {
		fmt.Printf("\n%s [y/n]: ", question)
		line, err := readLine()
		if err != nil {
			return false, err
		}
		switch strings.ToLower(line) {
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
		fmt.Println("Please answer y or n.")
	}
}

// AskInput prints prompt and returns the user's freeform input.
func AskInput(prompt string) (string, error) {
	fmt.Printf("\n%s\n> ", prompt)
	return readLine()
}

// Paginate prints content, pausing every (termHeight-2) lines with a --More-- prompt.
// Falls back to plain print if stdout is not a terminal.
func Paginate(content string) {
	lines := strings.Split(content, "\n")
	height := terminalHeight()

	if height <= 0 || len(lines) <= height {
		fmt.Println(content)
		return
	}

	for i, line := range lines {
		fmt.Println(line)
		if (i+1)%height == 0 && i+1 < len(lines) {
			fmt.Print("--More-- (press Enter) ")
			if _, err := readLine(); err != nil {
				// stdin closed (piped input exhausted) — print the rest without pausing
				fmt.Println(strings.Join(lines[i+1:], "\n"))
				return
			}
		}
	}
}

func terminalHeight() int {
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return 0 // not a TTY (piped) → no pagination
	}
	_, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return 40
	}
	if h > 4 {
		return h - 2
	}
	return h
}
