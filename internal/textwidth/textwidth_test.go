package textwidth

import "testing"

func TestWidthOfPlainText(t *testing.T) {
	cases := map[string]int{
		"":            0,
		"bento":       5,
		"Node.js":     7,
		"22.14.0":     7,
		"—":           1, // em dash is a normal single-column character
		"✓ INSTALLED": 11,
	}
	for text, want := range cases {
		if got := Of(text); got != want {
			t.Errorf("Of(%q) = %d, want %d", text, got, want)
		}
	}
}

// TestEmojiAreTwoColumns is the bug that broke Bento's report box: emoji were
// counted as one column each, so every line containing one came out narrow.
func TestEmojiAreTwoColumns(t *testing.T) {
	cases := map[string]int{
		"🍱":            2,
		"🤖":            2,
		"🌐":            2,
		"📱":            2,
		"🚀":            2,
		"🍱  BENTO BOX": 2 + 2 + 9,
	}
	for text, want := range cases {
		if got := Of(text); got != want {
			t.Errorf("Of(%q) = %d, want %d", text, got, want)
		}
	}
}

// TestVariationSelectorMakesSymbolsWide covers ⛓️ and ☢️ from Bento's own
// profile list: the base character is a one-column symbol, and the variation
// selector after it switches the terminal to a two-column emoji rendering.
func TestVariationSelectorMakesSymbolsWide(t *testing.T) {
	if got := Of("⛓"); got != 1 {
		t.Errorf("Of(⛓ without selector) = %d, want 1", got)
	}
	if got := Of("⛓️"); got != 2 {
		t.Errorf("Of(⛓ with selector) = %d, want 2", got)
	}
	if got := Of("☢️"); got != 2 {
		t.Errorf("Of(☢ with selector) = %d, want 2", got)
	}
	// The check mark and pointer used throughout the interface stay narrow.
	for _, narrow := range []string{"✓", "✗", "❯", "→", "↑", "·"} {
		if got := Of(narrow); got != 1 {
			t.Errorf("Of(%q) = %d, want 1", narrow, got)
		}
	}
}

func TestAnsiEscapesTakeNoColumns(t *testing.T) {
	coloured := "\x1b[32m✓ INSTALLED\x1b[0m"
	if got, want := Of(coloured), Of("✓ INSTALLED"); got != want {
		t.Errorf("colour changed the measured width: %d vs %d", got, want)
	}
	if got := StripANSI(coloured); got != "✓ INSTALLED" {
		t.Errorf("StripANSI = %q", got)
	}
	// Cursor movement sequences must also be stripped.
	if got := StripANSI("\r\x1b[Khello"); got != "\rhello" {
		t.Errorf("StripANSI of a clear-line sequence = %q", got)
	}
}

func TestWideEastAsianText(t *testing.T) {
	if got := Of("弁当"); got != 4 {
		t.Errorf("Of(弁当) = %d, want 4", got)
	}
}

func TestPad(t *testing.T) {
	if got := Pad("go", 6); got != "go    " {
		t.Errorf("Pad = %q", got)
	}
	if got := Of(Pad("🍱", 6)); got != 6 {
		t.Errorf("padded emoji measures %d columns, want 6", got)
	}
	// Text wider than the field is not truncated by Pad; it gets one space so
	// the next column is still separated.
	if got := Pad("a-very-long-name", 4); got != "a-very-long-name " {
		t.Errorf("Pad of oversized text = %q", got)
	}
}

func TestTruncate(t *testing.T) {
	if got := Truncate("short", 10); got != "short" {
		t.Errorf("Truncate should leave short text alone, got %q", got)
	}

	got := Truncate("A Dependency With A Very Long Name", 12)
	if width := Of(got); width > 12 {
		t.Errorf("Truncate produced %d columns (%q), want at most 12", width, got)
	}
	if got[len(got)-3:] != "…" {
		t.Errorf("Truncate = %q, want it to end with an ellipsis", got)
	}
}

func TestTruncateCountsEmojiCorrectly(t *testing.T) {
	// Four emoji are eight columns; truncating to five must not overflow.
	got := Truncate("🍱🍱🍱🍱", 5)
	if width := Of(got); width > 5 {
		t.Errorf("Truncate produced %d columns (%q), want at most 5", width, got)
	}
}
