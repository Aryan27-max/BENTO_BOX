// Package cli is Bento's user interface: the start screen, the profile
// selector, the plan Bento asks you to confirm, live progress and the final
// report.
package cli

import (
	"os"

	"github.com/Aryan27-max/bento-box/internal/reporter"
	"github.com/Aryan27-max/bento-box/internal/textwidth"
)

// Style renders text with ANSI escape sequences, or leaves it alone when the
// terminal cannot show them.
type Style struct {
	enabled bool
}

// NewStyle decides whether colour is appropriate. Colour is disabled when
// output is redirected, when NO_COLOR is set (the widely-honoured convention),
// or when the user asked for plain output.
func NewStyle(output *os.File, forcePlain bool) Style {
	if forcePlain {
		return Style{}
	}
	if os.Getenv("NO_COLOR") != "" {
		return Style{}
	}
	if !IsTerminal(output) {
		return Style{}
	}
	// Windows consoles need to be told to interpret escape sequences.
	EnableVirtualTerminal(output)
	return Style{enabled: true}
}

// Enabled reports whether colour is in use.
func (s Style) Enabled() bool { return s.enabled }

func (s Style) wrap(code, text string) string {
	if !s.enabled {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

// Bold renders emphasised text.
func (s Style) Bold(text string) string { return s.wrap("1", text) }

// Dim renders secondary text.
func (s Style) Dim(text string) string { return s.wrap("2", text) }

// Green marks success.
func (s Style) Green(text string) string { return s.wrap("32", text) }

// Red marks failure.
func (s Style) Red(text string) string { return s.wrap("31", text) }

// Yellow marks something that needs attention.
func (s Style) Yellow(text string) string { return s.wrap("33", text) }

// Cyan marks headings and highlights.
func (s Style) Cyan(text string) string { return s.wrap("36", text) }

// Palette adapts the style for the reporter.
func (s Style) Palette() reporter.Palette {
	if !s.enabled {
		return reporter.Palette{}
	}
	return reporter.Palette{
		Green: s.Green, Red: s.Red, Yellow: s.Yellow, Dim: s.Dim, Bold: s.Bold,
	}
}

// Symbols used throughout the interface. They are kept in one place so a
// future plain-ASCII mode has a single switch to flip.
const (
	symbolOK      = "✓"
	symbolFail    = "✗"
	symbolWarn    = "!"
	symbolArrow   = "→"
	symbolBullet  = "·"
	symbolPointer = "❯"
)

// stripANSI removes escape sequences, used when measuring printed width.
func stripANSI(text string) string { return textwidth.StripANSI(text) }
