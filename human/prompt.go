package human

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// Confirm prints question and blocks until user enters y/yes or n/no.
// Returns true for yes, false for no.
func Confirm(question string) (bool, error) {
	fmt.Printf("\n%s [y/n]: ", question)
	r := bufio.NewReader(os.Stdin)
	line, err := r.ReadString('\n')
	if err != nil {
		return false, err
	}
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "y" || line == "yes", nil
}

// AskInput prints prompt and returns the user's freeform input.
func AskInput(prompt string) (string, error) {
	fmt.Printf("\n%s\n> ", prompt)
	r := bufio.NewReader(os.Stdin)
	line, err := r.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// Paginate prints content, pausing every (termHeight-2) lines with a --More-- prompt.
// Falls back to plain print if terminal height cannot be determined.
func Paginate(content string) {
	lines := strings.Split(content, "\n")
	height := terminalHeight()

	if height <= 0 || len(lines) <= height {
		fmt.Println(content)
		return
	}

	r := bufio.NewReader(os.Stdin)
	for i, line := range lines {
		fmt.Println(line)
		if (i+1)%height == 0 && i+1 < len(lines) {
			fmt.Print("--More-- (press Enter) ")
			r.ReadString('\n') //nolint:errcheck
		}
	}
}

func terminalHeight() int {
	_, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return 40 // safe fallback
	}
	if h > 4 {
		return h - 2
	}
	return h
}
