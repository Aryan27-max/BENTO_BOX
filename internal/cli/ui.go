package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/Aryan27-max/bento-box/internal/dependency"
	"github.com/Aryan27-max/bento-box/internal/detector"
	"github.com/Aryan27-max/bento-box/internal/installer"
	"github.com/Aryan27-max/bento-box/internal/textwidth"
)

// UI writes everything the user sees before and during a run.
type UI struct {
	Output io.Writer
	Style  Style
	// Verbose prints every step of every installation rather than a summary
	// line per dependency.
	Verbose bool
}

// Banner prints the Bento header.
func (u *UI) Banner(version string) {
	fmt.Fprintf(u.Output, "\n%s\n", u.Style.Bold("🍱  BENTO BOX"))
	fmt.Fprintf(u.Output, "%s\n\n", u.Style.Dim("Developer Environment Bootstrapper "+version))
}

// System prints what was detected, so the user can see Bento understood their
// machine before it does anything to it.
func (u *UI) System(system detector.System) {
	fmt.Fprintf(u.Output, "%s\n", u.Style.Bold("Detected system"))
	u.field("OS", system.Display())
	u.field("Architecture", system.Arch)

	if len(system.PackageManagers) > 0 {
		u.field("Package managers", strings.Join(system.PackageManagers, ", "))
	} else {
		u.field("Package managers", u.Style.Yellow("none detected"))
	}
	if system.Shell.Name != "" {
		u.field("Shell", system.Shell.Name)
	}
	if system.NvidiaGPU {
		u.field("GPU", system.GPUName)
	}
	if system.Elevated {
		u.field("Privileges", "elevated")
	}
	fmt.Fprintln(u.Output)
}

func (u *UI) field(name, value string) {
	fmt.Fprintf(u.Output, "  %s %s\n", u.Style.Dim(pad(name+":", 20)), value)
}

// Plan shows exactly what Bento intends to do and where each thing comes from.
// Nothing has been changed at this point: this is the screen the user says yes
// or no to.
func (u *UI) Plan(plan installer.Plan) {
	fmt.Fprintf(u.Output, "%s %s\n\n", u.Style.Bold("Profile:"), plan.Profile.Label())

	if toInstall := plan.ToInstall(); len(toInstall) > 0 {
		fmt.Fprintf(u.Output, "%s\n", u.Style.Bold(fmt.Sprintf("Bento will install (%d)", len(toInstall))))
		for _, item := range toInstall {
			suffix := ""
			if item.Source != "" {
				suffix = u.Style.Dim("  " + item.Source)
			}
			verb := u.Style.Cyan(symbolArrow)
			if item.Action == dependency.ActionUpgrade {
				verb = u.Style.Yellow("↑")
			}
			fmt.Fprintf(u.Output, "  %s %s%s\n", verb, pad(item.Label(), 24), suffix)
			if item.Reason != "" {
				fmt.Fprintf(u.Output, "      %s\n", u.Style.Dim(item.Reason))
			}
		}
		fmt.Fprintln(u.Output)
	}

	if keep := plan.AlreadySatisfied(); len(keep) > 0 {
		fmt.Fprintf(u.Output, "%s\n", u.Style.Bold(fmt.Sprintf("Already installed (%d)", len(keep))))
		for _, item := range keep {
			version := item.Current.VersionString()
			if version == "" {
				version = "version unknown"
			}
			fmt.Fprintf(u.Output, "  %s %s%s\n", u.Style.Green(symbolOK), pad(item.Label(), 24), u.Style.Dim(version))
		}
		fmt.Fprintln(u.Output)
	}

	if skipped := plan.Skipped(); len(skipped) > 0 {
		fmt.Fprintf(u.Output, "%s\n", u.Style.Bold(fmt.Sprintf("Bento will not install (%d)", len(skipped))))
		for _, item := range skipped {
			fmt.Fprintf(u.Output, "  %s %s%s\n", u.Style.Yellow(symbolWarn), pad(item.Label(), 24), u.Style.Dim(item.Reason))
		}
		fmt.Fprintln(u.Output)
	}

	if unsupported := plan.Unsupported(); len(unsupported) > 0 {
		fmt.Fprintf(u.Output, "%s\n", u.Style.Bold(fmt.Sprintf("Not available on this platform (%d)", len(unsupported))))
		for _, item := range unsupported {
			fmt.Fprintf(u.Output, "  %s %s%s\n", u.Style.Dim("—"), pad(item.Label(), 24), u.Style.Dim(item.Reason))
		}
		fmt.Fprintln(u.Output)
	}

	if plan.NeedsElevation() && !plan.System.Elevated {
		message := "Some installations need administrator privileges and will prompt for them."
		if plan.System.OS != "windows" {
			message = "Some installations need root privileges; Bento will use sudo, which may ask for your password."
		}
		fmt.Fprintf(u.Output, "%s %s\n\n", u.Style.Yellow(symbolWarn), message)
	}
}

// Progress reports live installation progress. It implements
// installer.Progress.
type Progress struct {
	UI      *UI
	current string
}

// Start announces work on a dependency.
func (p *Progress) Start(index, total int, item installer.Item) {
	p.current = item.Label()

	switch item.Action {
	case dependency.ActionKeep, dependency.ActionUnsupported, dependency.ActionSkip:
		// Nothing is happening to these; the final report covers them.
		return
	}
	fmt.Fprintf(p.UI.Output, "%s %s\n",
		p.UI.Style.Dim(fmt.Sprintf("[%d/%d]", index, total)),
		p.UI.Style.Bold(item.Label()))
}

// Step reports a stage within a dependency's installation.
func (p *Progress) Step(message string) {
	if !p.UI.Verbose {
		return
	}
	fmt.Fprintf(p.UI.Output, "      %s %s\n", p.UI.Style.Dim(symbolBullet), p.UI.Style.Dim(message))
}

// Finish reports the outcome of one dependency.
func (p *Progress) Finish(outcome dependency.Outcome) {
	switch outcome.Status {
	case dependency.StatusAlreadyInstalled, dependency.StatusUnsupported:
		return
	case dependency.StatusSkipped:
		if !p.UI.Verbose {
			return
		}
	}

	style := p.UI.Style
	var symbol, detail string

	switch outcome.Status {
	case dependency.StatusInstalled, dependency.StatusVerified:
		symbol = style.Green(symbolOK)
		detail = "installed " + outcome.Version
	case dependency.StatusUpdated:
		symbol = style.Green(symbolOK)
		detail = fmt.Sprintf("updated %s → %s", outcome.PreviousVersion, outcome.Version)
	case dependency.StatusWarning:
		symbol = style.Yellow(symbolWarn)
		detail = "installed " + outcome.Version + " with warnings"
	case dependency.StatusFailed:
		symbol = style.Red(symbolFail)
		detail = "failed"
	case dependency.StatusPlanned:
		symbol = style.Dim(symbolBullet)
		detail = "dry run"
	default:
		symbol = style.Dim(symbolBullet)
		detail = strings.ToLower(string(outcome.Status))
	}

	fmt.Fprintf(p.UI.Output, "      %s %s\n", symbol, style.Dim(strings.TrimSpace(detail)))

	for _, environmentChange := range outcome.Environment {
		fmt.Fprintf(p.UI.Output, "      %s %s\n", style.Green(symbolOK), style.Dim(environmentChange))
	}
	if outcome.Status == dependency.StatusFailed {
		for _, message := range outcome.Errors {
			fmt.Fprintf(p.UI.Output, "      %s %s\n", style.Red(symbolBullet), style.Dim(message))
		}
	}
}

func pad(text string, width int) string { return textwidth.Pad(text, width) }

// ClearLine erases the current line so a transient status message can be
// replaced. On a terminal without colour support there are no escape
// sequences to use, so it simply starts a new line instead of printing
// escape codes as literal text.
func (u *UI) ClearLine() {
	if u.Style.Enabled() {
		fmt.Fprint(u.Output, "\r\x1b[K")
		return
	}
	fmt.Fprintln(u.Output)
}
