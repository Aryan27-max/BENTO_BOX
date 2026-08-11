//go:build windows

package cli

import (
	"os"

	"golang.org/x/sys/windows"
)

// Windows consoles need to be configured explicitly for two things Unix
// terminals give you for free: interpreting ANSI escape sequences on output,
// and delivering arrow keys as escape sequences on input. Without the second,
// a raw read on Windows simply never sees the arrow keys at all.

// IsTerminal reports whether the file is an interactive console.
func IsTerminal(file *os.File) bool {
	var mode uint32
	return windows.GetConsoleMode(windows.Handle(file.Fd()), &mode) == nil
}

// EnableVirtualTerminal turns on ANSI escape interpretation for output.
func EnableVirtualTerminal(file *os.File) {
	handle := windows.Handle(file.Fd())

	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		return
	}
	_ = windows.SetConsoleMode(handle, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
}

// makeRaw puts the console into a mode where keystrokes arrive immediately and
// arrow keys arrive as escape sequences. It returns a function that restores
// the console exactly as it was.
func makeRaw(file *os.File) (func(), error) {
	handle := windows.Handle(file.Fd())

	var original uint32
	if err := windows.GetConsoleMode(handle, &original); err != nil {
		return nil, err
	}

	raw := original &^ (windows.ENABLE_ECHO_INPUT | windows.ENABLE_LINE_INPUT | windows.ENABLE_PROCESSED_INPUT)
	raw |= windows.ENABLE_VIRTUAL_TERMINAL_INPUT

	if err := windows.SetConsoleMode(handle, raw); err != nil {
		return nil, err
	}
	return func() { _ = windows.SetConsoleMode(handle, original) }, nil
}
