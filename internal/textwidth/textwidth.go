// Package textwidth measures how many terminal columns a string occupies.
//
// Bento's report is drawn as a box, and a box only looks like a box if every
// line is the same width. Counting runes is not enough: an emoji takes two
// columns, a colour escape sequence takes none, and a variation selector turns
// a one-column symbol into a two-column one.
package textwidth

import "strings"

// Of returns the number of terminal columns a string occupies, ignoring ANSI
// escape sequences.
func Of(text string) int {
	runes := []rune(StripANSI(text))

	width := 0
	for index, char := range runes {
		var next rune
		if index+1 < len(runes) {
			next = runes[index+1]
		}
		width += runeWidth(char, next)
	}
	return width
}

// StripANSI removes colour escape sequences, which occupy no columns.
func StripANSI(text string) string {
	if !strings.Contains(text, "\x1b") {
		return text
	}

	var builder strings.Builder
	inEscape := false
	for _, char := range text {
		switch {
		case char == '\x1b':
			inEscape = true
		case inEscape && (char == 'm' || char == 'K' || char == 'A' || char == 'J'):
			inEscape = false
		case !inEscape:
			builder.WriteRune(char)
		}
	}
	return builder.String()
}

const (
	variationSelector16 = 0xFE0F
	zeroWidthJoiner     = 0x200D
)

// runeWidth returns the columns one rune occupies. next is the following rune,
// because a variation selector after a symbol switches it to its
// two-column emoji presentation — which is exactly what happens to ⛓️ and ☢️
// in Bento's own profile list.
func runeWidth(char, next rune) int {
	switch char {
	case variationSelector16, zeroWidthJoiner:
		return 0
	}
	if char < 0x1100 {
		return 1
	}

	switch {
	// Emoji blocks are unambiguously two columns wide.
	case between(char, 0x1F300, 0x1FAFF),
		between(char, 0x1F000, 0x1F2FF):
		return 2

	// East Asian Wide and Fullwidth ranges.
	case between(char, 0x1100, 0x115F), // Hangul Jamo
		between(char, 0x2E80, 0x303E), // CJK radicals and punctuation
		between(char, 0x3041, 0x33FF), // kana through CJK compatibility
		between(char, 0x3400, 0x4DBF), // CJK extension A
		between(char, 0x4E00, 0x9FFF), // CJK unified ideographs
		between(char, 0xA000, 0xA4CF), // Yi
		between(char, 0xAC00, 0xD7A3), // Hangul syllables
		between(char, 0xF900, 0xFAFF), // CJK compatibility ideographs
		between(char, 0xFE30, 0xFE6F), // CJK compatibility forms
		between(char, 0xFF00, 0xFF60), // fullwidth forms
		between(char, 0xFFE0, 0xFFE6):
		return 2

	// Symbols that default to text presentation and only become wide when a
	// variation selector follows: ⛓ is one column, ⛓️ is two.
	case between(char, 0x2190, 0x2BFF):
		if next == variationSelector16 {
			return 2
		}
		return 1

	// Combining marks occupy no space of their own.
	case between(char, 0x0300, 0x036F), between(char, 0x20D0, 0x20FF):
		return 0
	}
	return 1
}

func between(char, low, high rune) bool { return char >= low && char <= high }

// Pad appends spaces so the text occupies exactly width columns. Text that is
// already wider is returned with a single trailing space.
func Pad(text string, width int) string {
	gap := width - Of(text)
	if gap <= 0 {
		return text + " "
	}
	return text + strings.Repeat(" ", gap)
}

// Truncate shortens text to at most width columns, marking the cut with an
// ellipsis.
func Truncate(text string, width int) string {
	if Of(text) <= width || width <= 0 {
		return text
	}

	runes := []rune(text)
	used, cut := 0, 0
	for index, char := range runes {
		var next rune
		if index+1 < len(runes) {
			next = runes[index+1]
		}
		size := runeWidth(char, next)
		if used+size > width-1 {
			break
		}
		used += size
		cut = index + 1
	}
	return string(runes[:cut]) + "…"
}
