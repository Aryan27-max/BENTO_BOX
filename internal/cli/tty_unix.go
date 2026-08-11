//go:build !windows

package cli

import (
	"os"

	"golang.org/x/term"
)

// IsTerminal reports whether the file is an interactive terminal.
func IsTerminal(file *os.File) bool { return term.IsTerminal(int(file.Fd())) }

// EnableVirtualTerminal is a no-op: Unix terminals interpret ANSI escape
// sequences without being asked.
func EnableVirtualTerminal(*os.File) {}

// makeRaw puts the terminal into raw mode and returns a restore function.
func makeRaw(file *os.File) (func(), error) {
	state, err := term.MakeRaw(int(file.Fd()))
	if err != nil {
		return nil, err
	}
	return func() { _ = term.Restore(int(file.Fd()), state) }, nil
}
