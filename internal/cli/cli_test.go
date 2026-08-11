package cli

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/Aryan27-max/bento-box/internal/profiles"
)

func TestParseOptions(t *testing.T) {
	cases := []struct {
		name      string
		arguments []string
		check     func(*testing.T, Options)
	}{
		{"no arguments", nil, func(t *testing.T, o Options) {
			if o.Profile != "" || o.AssumeYes || o.DryRun {
				t.Errorf("defaults are not neutral: %+v", o)
			}
		}},
		{"long profile", []string{"--profile", "web"}, func(t *testing.T, o Options) {
			if o.Profile != "web" {
				t.Errorf("Profile = %q", o.Profile)
			}
		}},
		{"short profile", []string{"-p", "nuke"}, func(t *testing.T, o Options) {
			if o.Profile != "nuke" {
				t.Errorf("Profile = %q", o.Profile)
			}
		}},
		{"bare profile name", []string{"blockchain"}, func(t *testing.T, o Options) {
			// Typing `bento web` is what people try first.
			if o.Profile != "blockchain" {
				t.Errorf("Profile = %q, want the bare argument to be taken as the profile", o.Profile)
			}
		}},
		{"unattended", []string{"--profile", "ai", "--yes"}, func(t *testing.T, o Options) {
			if !o.AssumeYes {
				t.Error("--yes was not parsed")
			}
		}},
		{"dry run", []string{"--dry-run"}, func(t *testing.T, o Options) {
			if !o.DryRun {
				t.Error("--dry-run was not parsed")
			}
		}},
		{"json implies machine output", []string{"--json"}, func(t *testing.T, o Options) {
			if !o.JSON {
				t.Error("--json was not parsed")
			}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			options, err := ParseOptions(tc.arguments, io.Discard)
			if err != nil {
				t.Fatalf("ParseOptions(%v): %v", tc.arguments, err)
			}
			tc.check(t, options)
		})
	}
}

func TestParseOptionsRejectsNonsense(t *testing.T) {
	if _, err := ParseOptions([]string{"--not-a-flag"}, io.Discard); err == nil {
		t.Error("an unknown flag should be an error")
	}
	if _, err := ParseOptions([]string{"web", "extra", "arguments"}, io.Discard); err == nil {
		t.Error("stray arguments should be an error")
	}
}

func TestVersionFlagPrintsAndExitsCleanly(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(t.Context(), []string{"--version"}, &stdout, &stderr)

	if code != ExitOK {
		t.Errorf("exit code = %d, want %d", code, ExitOK)
	}
	if !strings.Contains(stdout.String(), "bento") {
		t.Errorf("stdout = %q, want the version", stdout.String())
	}
}

func TestListFlagShowsEveryProfile(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(t.Context(), []string{"--list"}, &stdout, &stderr)

	if code != ExitOK {
		t.Errorf("exit code = %d, want %d", code, ExitOK)
	}
	output := stdout.String()
	for _, profile := range []string{"ai", "web", "blockchain", "app", "nuke"} {
		if !strings.Contains(output, profile) {
			t.Errorf("--list output is missing %q:\n%s", profile, output)
		}
	}
}

func TestUnknownProfileIsAUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(t.Context(), []string{"--profile", "underwater-basket-weaving", "--plan"}, &stdout, &stderr)

	if code != ExitUsage {
		t.Errorf("exit code = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "unknown profile") {
		t.Errorf("stderr = %q, want an explanation", stderr.String())
	}
}

func TestHelpExitsCleanly(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run(t.Context(), []string{"--help"}, &stdout, &stderr); code != ExitOK {
		t.Errorf("exit code = %d, want %d", code, ExitOK)
	}
	if !strings.Contains(stderr.String(), "Usage") {
		t.Errorf("help output is missing usage:\n%s", stderr.String())
	}
}

// --- styling --------------------------------------------------------------

func TestNoColorIsHonoured(t *testing.T) {
	// A style built with forcePlain must never emit escape sequences, whatever
	// the terminal says.
	style := NewStyle(os.Stdout, true)
	if style.Enabled() {
		t.Fatal("forcePlain should disable colour")
	}
	for _, rendered := range []string{
		style.Green("ok"), style.Red("bad"), style.Bold("loud"), style.Dim("quiet"),
	} {
		if strings.Contains(rendered, "\x1b") {
			t.Errorf("plain style emitted an escape sequence: %q", rendered)
		}
	}
}

func TestNoColorEnvironmentVariable(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	if NewStyle(os.Stdout, false).Enabled() {
		t.Error("NO_COLOR must disable colour")
	}
}

func TestPaletteIsEmptyWhenColourIsOff(t *testing.T) {
	palette := NewStyle(os.Stdout, true).Palette()
	if palette.Green != nil || palette.Red != nil {
		t.Error("a plain style should hand the reporter an empty palette")
	}
}

// --- key decoding ---------------------------------------------------------

func TestReadKeyDecodesArrowsAndShortcuts(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  keyKind
		digit int
	}{
		{"up arrow", "\x1b[A", keyUp, 0},
		{"down arrow", "\x1b[B", keyDown, 0},
		{"vim up", "k", keyUp, 0},
		{"vim down", "j", keyDown, 0},
		{"enter", "\r", keyEnter, 0},
		{"newline", "\n", keyEnter, 0},
		{"ctrl-c", "\x03", keyCancel, 0},
		{"q", "q", keyCancel, 0},
		{"digit", "3", keyDigit, 3},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := readKey(bufio.NewReader(strings.NewReader(tc.input)))
			if err != nil {
				t.Fatalf("readKey: %v", err)
			}
			if got.kind != tc.want {
				t.Errorf("kind = %v, want %v", got.kind, tc.want)
			}
			if tc.digit != 0 && got.digit != tc.digit {
				t.Errorf("digit = %d, want %d", got.digit, tc.digit)
			}
		})
	}
}

func TestBareEscapeCancels(t *testing.T) {
	got, err := readKey(bufio.NewReader(strings.NewReader("\x1b")))
	if err != nil {
		t.Fatalf("readKey: %v", err)
	}
	if got.kind != keyCancel {
		t.Errorf("a bare escape should cancel, got %v", got.kind)
	}
}

// --- non-interactive prompts ---------------------------------------------

func testProfiles() []profiles.Profile {
	return []profiles.Profile{
		{ID: "ai", Name: "AI / ML", Emoji: "🤖", Description: "Python and friends"},
		{ID: "web", Name: "Web", Emoji: "🌐", Description: "Node and databases"},
		{ID: "nuke", Name: "NUKE", Emoji: "☢️", Description: "Everything"},
	}
}

func promptWith(input string) (*Prompt, *bytes.Buffer) {
	output := &bytes.Buffer{}
	return &Prompt{
		Input:       fileFromString(input),
		Output:      output,
		Style:       Style{},
		Interactive: false,
	}, output
}

// fileFromString writes input into a pipe so the prompt can read it from an
// *os.File, which is what the real prompt uses.
func fileFromString(input string) *os.File {
	reader, writer, err := os.Pipe()
	if err != nil {
		panic(err)
	}
	go func() {
		writer.WriteString(input)
		writer.Close()
	}()
	return reader
}

func TestSelectProfileByNumber(t *testing.T) {
	prompt, output := promptWith("2\n")

	profile, err := prompt.SelectProfile(testProfiles())
	if err != nil {
		t.Fatalf("SelectProfile: %v", err)
	}
	if profile.ID != "web" {
		t.Errorf("chose %q, want web", profile.ID)
	}
	if !strings.Contains(output.String(), "1) ") {
		t.Error("the numbered fallback should list the options")
	}
}

func TestSelectProfileByName(t *testing.T) {
	prompt, _ := promptWith("nuke\n")

	profile, err := prompt.SelectProfile(testProfiles())
	if err != nil {
		t.Fatalf("SelectProfile: %v", err)
	}
	if profile.ID != "nuke" {
		t.Errorf("chose %q, want nuke", profile.ID)
	}
}

func TestSelectProfileRejectsOutOfRange(t *testing.T) {
	prompt, _ := promptWith("99\n")
	if _, err := prompt.SelectProfile(testProfiles()); err == nil {
		t.Error("an out-of-range choice should be an error")
	}
}

func TestEmptySelectionCancels(t *testing.T) {
	prompt, _ := promptWith("\n")
	if _, err := prompt.SelectProfile(testProfiles()); err != ErrCancelled {
		t.Errorf("err = %v, want ErrCancelled", err)
	}
}

// TestConfirmRefusesWithoutATerminal: an unattended run must pass --yes
// explicitly rather than have consent assumed for it.
func TestConfirmRefusesWithoutATerminal(t *testing.T) {
	prompt, _ := promptWith("y\n")

	confirmed, err := prompt.Confirm("Continue?")
	if err == nil {
		t.Fatal("confirmation without a terminal should be an error")
	}
	if confirmed {
		t.Error("consent must never be assumed")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Errorf("error = %q, want it to point at --yes", err)
	}
}
