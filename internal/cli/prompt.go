package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/Aryan27-max/bento-box/internal/profiles"
)

// ErrCancelled reports that the user backed out at a prompt. It is a normal
// outcome, not a failure: Bento exits quietly having changed nothing.
var ErrCancelled = errors.New("cancelled")

// Prompt asks the user questions. Every prompt has a non-interactive path, so
// Bento works the same when driven by a script as when driven by a person.
type Prompt struct {
	Input  *os.File
	Output io.Writer
	Style  Style
	// Interactive reports whether a person is watching. When false, prompts
	// fall back to reading a line, and confirmation must come from --yes.
	Interactive bool
}

// NewPrompt builds a prompt for the real terminal.
func NewPrompt(style Style, output io.Writer) *Prompt {
	return &Prompt{
		Input:       os.Stdin,
		Output:      output,
		Style:       style,
		Interactive: IsTerminal(os.Stdin) && IsTerminal(os.Stdout),
	}
}

// SelectProfile asks the user which profile to install. On an interactive
// terminal this is an arrow-key menu; everywhere else it is a numbered list
// read from standard input.
func (p *Prompt) SelectProfile(options []profiles.Profile) (profiles.Profile, error) {
	if len(options) == 0 {
		return profiles.Profile{}, errors.New("no profiles are available")
	}
	if !p.Interactive {
		return p.selectByNumber(options)
	}
	profile, err := p.selectWithArrowKeys(options)
	if errors.Is(err, errNoRawMode) {
		// A terminal that will not go into raw mode is still usable; it just
		// cannot do a live menu.
		return p.selectByNumber(options)
	}
	return profile, err
}

var errNoRawMode = errors.New("terminal does not support raw mode")

func (p *Prompt) selectWithArrowKeys(options []profiles.Profile) (profiles.Profile, error) {
	restore, err := makeRaw(p.Input)
	if err != nil {
		return profiles.Profile{}, errNoRawMode
	}
	defer restore()

	fmt.Fprintf(p.Output, "%s\n\n", p.Style.Bold("What are you building?"))
	p.renderMenu(options, 0, false)

	reader := bufio.NewReader(p.Input)
	selected := 0

	for {
		key, err := readKey(reader)
		if err != nil {
			return profiles.Profile{}, ErrCancelled
		}

		switch key.kind {
		case keyUp:
			selected = (selected - 1 + len(options)) % len(options)
		case keyDown:
			selected = (selected + 1) % len(options)
		case keyEnter:
			p.renderMenu(options, selected, true)
			fmt.Fprintln(p.Output)
			return options[selected], nil
		case keyCancel:
			p.renderMenu(options, selected, true)
			fmt.Fprintln(p.Output)
			return profiles.Profile{}, ErrCancelled
		case keyDigit:
			if key.digit >= 1 && key.digit <= len(options) {
				selected = key.digit - 1
				p.renderMenu(options, selected, true)
				fmt.Fprintln(p.Output)
				return options[selected], nil
			}
		}
		p.renderMenu(options, selected, true)
	}
}

// renderMenu draws the option list, redrawing in place after the first time by
// moving the cursor back up over the lines it previously wrote.
func (p *Prompt) renderMenu(options []profiles.Profile, selected int, redraw bool) {
	if redraw {
		fmt.Fprintf(p.Output, "\x1b[%dA", len(options))
	}
	for index, profile := range options {
		line := fmt.Sprintf("  %s  %s", profile.Label(), p.Style.Dim(profile.Description))
		if index == selected {
			line = fmt.Sprintf("%s %s  %s", p.Style.Cyan(symbolPointer), p.Style.Bold(profile.Label()),
				p.Style.Dim(profile.Description))
		}
		fmt.Fprintf(p.Output, "\r\x1b[K%s\r\n", line)
	}
}

func (p *Prompt) selectByNumber(options []profiles.Profile) (profiles.Profile, error) {
	fmt.Fprintf(p.Output, "%s\n\n", p.Style.Bold("What are you building?"))
	for index, profile := range options {
		fmt.Fprintf(p.Output, "  %d) %s — %s\n", index+1, profile.Label(), profile.Description)
	}
	fmt.Fprintf(p.Output, "\nChoose a profile [1-%d]: ", len(options))

	reader := bufio.NewReader(p.Input)
	line, err := reader.ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return profiles.Profile{}, ErrCancelled
	}

	answer := strings.TrimSpace(line)
	if answer == "" {
		return profiles.Profile{}, ErrCancelled
	}
	if number, err := strconv.Atoi(answer); err == nil {
		if number < 1 || number > len(options) {
			return profiles.Profile{}, fmt.Errorf("%q is not one of the %d options", answer, len(options))
		}
		return options[number-1], nil
	}

	// A name works too, which is what a person typing "web" expects.
	for _, profile := range options {
		if strings.EqualFold(profile.ID, answer) || strings.EqualFold(profile.Name, answer) {
			return profile, nil
		}
	}
	return profiles.Profile{}, fmt.Errorf("%q is not a known profile", answer)
}

// Confirm asks a yes/no question, defaulting to yes. When Bento is not
// interactive it refuses rather than assuming consent: an unattended run has
// to pass --yes to say so explicitly.
func (p *Prompt) Confirm(question string) (bool, error) {
	if !p.Interactive {
		return false, errors.New("cannot ask for confirmation without a terminal; pass --yes to proceed unattended")
	}

	fmt.Fprintf(p.Output, "%s [Y/n] ", question)
	reader := bufio.NewReader(p.Input)
	line, err := reader.ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return false, nil
	}

	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// --- key decoding ---------------------------------------------------------

type keyKind int

const (
	keyOther keyKind = iota
	keyUp
	keyDown
	keyEnter
	keyCancel
	keyDigit
)

type key struct {
	kind  keyKind
	digit int
}

// readKey decodes a single keypress, including the escape sequences that
// arrow keys arrive as.
func readKey(reader *bufio.Reader) (key, error) {
	first, err := reader.ReadByte()
	if err != nil {
		return key{}, err
	}

	switch first {
	case '\r', '\n':
		return key{kind: keyEnter}, nil
	case 3, 4, 'q', 'Q': // Ctrl+C, Ctrl+D, q
		return key{kind: keyCancel}, nil
	case 'k', 'K':
		return key{kind: keyUp}, nil
	case 'j', 'J':
		return key{kind: keyDown}, nil
	case 27: // escape, possibly the start of an arrow-key sequence
		if reader.Buffered() == 0 {
			return key{kind: keyCancel}, nil
		}
		bracket, err := reader.ReadByte()
		if err != nil || bracket != '[' {
			return key{kind: keyCancel}, nil
		}
		direction, err := reader.ReadByte()
		if err != nil {
			return key{kind: keyCancel}, nil
		}
		switch direction {
		case 'A':
			return key{kind: keyUp}, nil
		case 'B':
			return key{kind: keyDown}, nil
		}
		return key{kind: keyOther}, nil
	}

	if first >= '1' && first <= '9' {
		return key{kind: keyDigit, digit: int(first - '0')}, nil
	}
	return key{kind: keyOther}, nil
}
