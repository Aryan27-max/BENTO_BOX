package paths

import (
	"path/filepath"
	"strings"
	"testing"
)

// The tests in this file all assert the same property from a different angle:
// what a path means depends on the operating system the path belongs to, not
// on the one Bento happens to be running on. Without it, Windows path handling
// can only ever be exercised by the Windows CI runner — which is the platform
// where getting it wrong is invisible, because filepath there understands both
// separator conventions and forgives the mistake.

// TestWindowsSemanticsDoNotDependOnTheHost is the regression guard for PATH
// entries that differ only by a trailing separator or by separator style.
func TestWindowsSemanticsDoNotDependOnTheHost(t *testing.T) {
	cases := map[string]string{
		`C:\Go\bin\`:            `C:\Go\bin`,
		"C:/Go/bin/":            `C:\Go\bin`,
		`C:\Go\\bin`:            `C:\Go\bin`,
		`C:\Go\..\Go\bin`:       `C:\Go\bin`,
		`\\server\share\bin\`:   `\\server\share\bin`,
		`  C:\Program Files\  `: `C:\Program Files`,
	}
	for input, want := range cases {
		if got := NormaliseFor(input, true); got != want {
			t.Errorf("NormaliseFor(%q, windows) = %q, want %q", input, got, want)
		}
	}

	if !SameEntry(`C:\Go\bin\`, `C:\Go\bin`, true) {
		t.Error("a trailing separator does not make a second directory")
	}
	if !SameEntry(`\\server\share\bin`, "//server/share/bin", true) {
		t.Error("both separators describe the same UNC directory")
	}
	// The volume is part of the identity: two shares on one server are two
	// directories, however similar the rest of the path looks.
	if SameEntry(`\\server\one\bin`, `\\server\two\bin`, true) {
		t.Error("different shares must not compare equal")
	}
}

// TestPosixSemanticsDoNotDependOnTheHost is the other half: a backslash is an
// ordinary character in a Unix filename and must never be read as structure,
// even when Bento is running on Windows.
func TestPosixSemanticsDoNotDependOnTheHost(t *testing.T) {
	if got := NormaliseFor(`C:\Go\bin\`, false); got != `C:\Go\bin\` {
		t.Errorf("NormaliseFor(..., posix) = %q, want the name left alone", got)
	}
	if SameEntry(`C:\Go\bin\`, `C:\Go\bin`, false) {
		t.Error("on Unix these are two different filenames")
	}
	if got := NormaliseFor("/opt/go/bin/", false); got != "/opt/go/bin" {
		t.Errorf("NormaliseFor = %q, want /opt/go/bin", got)
	}
}

// TestExpansionFollowsTheTargetNotTheHost covers the catalog entries that
// caused GUI applications to be reported missing: a Windows path template is
// written with forward slashes and expanded against a backslashed environment
// variable, and only the target platform can say what the result should be.
func TestExpansionFollowsTheTargetNotTheHost(t *testing.T) {
	lookup := LookupFor("/home/dev", func(name string) string {
		if name == "LOCALAPPDATA" {
			return `C:\Users\dev\AppData\Local`
		}
		return ""
	}, true)

	const template = "${LOCALAPPDATA}/Programs/Microsoft VS Code/Code.exe"
	want := `C:\Users\dev\AppData\Local\Programs\Microsoft VS Code\Code.exe`
	if got := ExpandFor(template, lookup, true); got != want {
		t.Errorf("ExpandFor(windows) = %q, want %q", got, want)
	}

	// The Windows well-known folders are synthesised in the target's flavour
	// too when the environment does not supply them.
	synthesised := LookupFor(`C:\Users\dev`, func(string) string { return "" }, true)
	if got := ExpandFor("${LOCALAPPDATA}/Android/Sdk", synthesised, true); got != `C:\Users\dev\AppData\Local\Android\Sdk` {
		t.Errorf("ExpandFor with a synthesised LOCALAPPDATA = %q", got)
	}
}

// TestTargetAwareCleaningAgreesWithFilepath checks the hand-written cleaner
// against the standard library for the platform this test is running on. It is
// what keeps the two implementations honest: whichever runner executes it, one
// of the two flavours is checked against path/filepath itself.
func TestTargetAwareCleaningAgreesWithFilepath(t *testing.T) {
	corpus := []string{
		"/usr/local/bin", "/usr/local/bin/", "/usr//local//bin", "/usr/local/../bin",
		"./go/bin", "go/bin/", "../go/bin", ".", "a",
		`C:\Go\bin`, `C:\Go\bin\`, `C:\Go\\bin`, `C:/Go/bin/`, `C:\Go\..\Go\bin`,
		`C:\Program Files\Go\bin`, `\\server\share\bin`, `relative\win\path`,
	}
	for _, input := range corpus {
		native := filepath.FromSlash(input)
		want := filepath.Clean(native)
		if got := CleanFor(native, hostIsWindows); got != want {
			t.Errorf("CleanFor(%q) = %q, filepath.Clean gives %q", native, got, want)
		}
	}

	joins := [][]string{
		{"/home/dev", ".bento"},
		{"/home/dev", "go", "bin"},
		{`C:\Users\dev`, "AppData", "Local"},
		{"base", "", "leaf"},
		{"/a/b", "../c"},
	}
	for _, elements := range joins {
		if got, want := joinFor(hostIsWindows, elements...), filepath.Join(elements...); got != want {
			t.Errorf("joinFor(%v) = %q, filepath.Join gives %q", elements, got, want)
		}
	}

	for _, input := range corpus {
		if got, want := FromSlashFor(input, hostIsWindows), filepath.FromSlash(input); got != want {
			t.Errorf("FromSlashFor(%q) = %q, filepath.FromSlash gives %q", input, got, want)
		}
	}

	// Sanity: the corpus is meant to contain paths that actually exercise
	// cleaning, so a future edit cannot quietly empty it.
	if len(corpus) < 10 || !strings.Contains(strings.Join(corpus, " "), `\`) {
		t.Fatal("the corpus no longer covers Windows-shaped input")
	}
}
