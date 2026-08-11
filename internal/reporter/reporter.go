// Package reporter turns a finished run into the two artefacts a user gets: a
// terminal summary they read immediately, and .bento/report.json that a script
// or a colleague can read later.
//
// The report states exactly what happened. Statuses are never rounded up:
// something that was already present says so, something that could not be
// installed on this platform says why, and a version is only printed when it
// was actually observed.
package reporter

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Aryan27-max/bento-box/internal/dependency"
	"github.com/Aryan27-max/bento-box/internal/detector"
	"github.com/Aryan27-max/bento-box/internal/environment"
	"github.com/Aryan27-max/bento-box/internal/paths"
	"github.com/Aryan27-max/bento-box/internal/textwidth"
)

// Report is the complete record of one Bento run.
type Report struct {
	Timestamp    time.Time `json:"timestamp"`
	BentoVersion string    `json:"bento_version"`

	Platform     string          `json:"platform"`
	Architecture string          `json:"architecture"`
	OSName       string          `json:"os_name"`
	Distro       detector.Distro `json:"distro,omitzero"`
	Profile      string          `json:"profile"`
	ProfileName  string          `json:"profile_name"`
	DryRun       bool            `json:"dry_run"`

	// EnvironmentReady reports whether every required dependency ended in a
	// working state.
	EnvironmentReady bool `json:"environment_ready"`
	// RestartRequired reports that some installation only takes effect after
	// a reboot. Bento never reboots the machine itself.
	RestartRequired bool `json:"restart_required"`
	// ShellRestartRequired reports that environment changes are not visible
	// to the shell Bento was started from.
	ShellRestartRequired bool `json:"shell_restart_required"`

	Dependencies []dependency.Outcome       `json:"dependencies"`
	Environment  []environment.Change       `json:"environment_changes,omitempty"`
	Summary      dependency.Summary         `json:"summary"`
	Services     []dependency.ServiceStatus `json:"services,omitempty"`

	Errors   []string `json:"errors,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
	Notices  []string `json:"notices,omitempty"`

	DurationSeconds float64 `json:"duration_seconds"`
	LogFile         string  `json:"log_file,omitempty"`
}

// Input is everything the reporter needs to build a report.
type Input struct {
	Version       string
	System        detector.System
	ProfileID     string
	ProfileName   string
	DryRun        bool
	Outcomes      []dependency.Outcome
	Environment   []environment.Change
	Duration      time.Duration
	LogFile       string
	RestartNotice string
}

// Build assembles a report from a finished run.
func Build(input Input) Report {
	report := Report{
		Timestamp:    time.Now(),
		BentoVersion: input.Version,
		Platform:     input.System.OS,
		Architecture: input.System.Arch,
		OSName:       input.System.Display(),
		Distro:       input.System.Distro,
		Profile:      input.ProfileID,
		ProfileName:  input.ProfileName,
		DryRun:       input.DryRun,
		Dependencies: input.Outcomes,
		Environment:  input.Environment,
		Summary:      dependency.Summarise(input.Outcomes),
		LogFile:      input.LogFile,
	}
	report.DurationSeconds = input.Duration.Round(time.Millisecond).Seconds()

	ready := true
	for _, outcome := range input.Outcomes {
		if outcome.RestartRequired && outcome.Status.OK() {
			report.RestartRequired = true
		}
		if outcome.Service != nil {
			report.Services = append(report.Services, *outcome.Service)
		}
		for _, message := range outcome.Errors {
			report.Errors = append(report.Errors, fmt.Sprintf("%s: %s", outcome.DisplayName, message))
		}
		for _, message := range outcome.Warnings {
			report.Warnings = append(report.Warnings, fmt.Sprintf("%s: %s", outcome.DisplayName, message))
		}
		if outcome.Status == dependency.StatusFailed {
			ready = false
		}
	}

	// A dry run never leaves the machine ready, because it never changed
	// anything; saying otherwise would be a lie of convenience.
	report.EnvironmentReady = ready && !input.DryRun

	if input.RestartNotice != "" {
		report.ShellRestartRequired = true
		report.Notices = append(report.Notices, input.RestartNotice)
	}
	if report.RestartRequired {
		report.Notices = append(report.Notices, "One or more installations need a system restart before they work. Bento will not restart your machine.")
	}
	return report
}

// WriteJSON writes the machine-readable report to .bento/report.json under the
// user's home and returns the path it wrote.
func (r Report) WriteJSON(home string) (string, error) {
	directory := paths.Home(home)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return "", fmt.Errorf("creating %s: %w", directory, err)
	}

	path := filepath.Join(directory, "report.json")
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	return path, nil
}

// WriteJSONTo writes the report to an arbitrary writer, which is what
// --json uses to pipe a report into another tool.
func (r Report) WriteJSONTo(writer io.Writer) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(r)
}

// --- Terminal rendering ---------------------------------------------------

// Palette supplies the colours used when rendering. A zero Palette renders
// plain text, which is what happens when output is redirected or NO_COLOR is
// set.
type Palette struct {
	Green  func(string) string
	Red    func(string) string
	Yellow func(string) string
	Dim    func(string) string
	Bold   func(string) string
}

func (p Palette) green(s string) string  { return apply(p.Green, s) }
func (p Palette) red(s string) string    { return apply(p.Red, s) }
func (p Palette) yellow(s string) string { return apply(p.Yellow, s) }
func (p Palette) dim(s string) string    { return apply(p.Dim, s) }
func (p Palette) bold(s string) string   { return apply(p.Bold, s) }

func apply(fn func(string) string, s string) string {
	if fn == nil {
		return s
	}
	return fn(s)
}

const reportWidth = 62

// Render writes the human-readable report.
func (r Report) Render(writer io.Writer, palette Palette) {
	box := &boxWriter{writer: writer, width: reportWidth, palette: palette}

	box.top()
	box.centre("🍱  BENTO BOX")
	if r.DryRun {
		box.centre("DRY RUN — NOTHING WAS INSTALLED")
	} else {
		box.centre("INSTALLATION REPORT")
	}
	box.divider()

	box.field("OS", r.OSName)
	box.field("Architecture", r.Architecture)
	box.field("Profile", r.ProfileName)
	box.field("Duration", formatDuration(r.DurationSeconds))
	box.divider()

	box.row(pad("Dependency", 22)+pad("Version", 14)+pad("Status", 18), "")
	box.divider()
	for _, outcome := range r.Dependencies {
		box.dependency(outcome)
	}
	box.divider()

	box.field("Installed", fmt.Sprint(r.Summary.Installed))
	box.field("Already present", fmt.Sprint(r.Summary.AlreadyInstalled))
	box.field("Updated", fmt.Sprint(r.Summary.Updated))
	box.field("Warnings", fmt.Sprint(r.Summary.Warnings))
	box.field("Failed", fmt.Sprint(r.Summary.Failed))
	box.field("Skipped", fmt.Sprint(r.Summary.Skipped))
	box.field("Unsupported", fmt.Sprint(r.Summary.Unsupported))
	box.divider()

	switch {
	case r.DryRun:
		box.row(palette.yellow("Environment:  DRY RUN"), "")
	case r.EnvironmentReady:
		box.row(palette.green("Environment:  ✓ READY"), "")
	default:
		box.row(palette.red("Environment:  ✗ SOME DEPENDENCIES FAILED"), "")
	}
	box.bottom()

	r.renderDetails(writer, palette)
}

func (r Report) renderDetails(writer io.Writer, palette Palette) {
	if failures := r.failures(); len(failures) > 0 {
		fmt.Fprintf(writer, "\n%s\n", palette.bold("Failed"))
		for _, outcome := range failures {
			fmt.Fprintf(writer, "  %s %s\n", palette.red("✗"), outcome.DisplayName)
			for _, message := range outcome.Errors {
				fmt.Fprintf(writer, "      %s\n", palette.dim(message))
			}
		}
	}

	if manual := r.manual(); len(manual) > 0 {
		fmt.Fprintf(writer, "\n%s\n", palette.bold("Needs your attention"))
		for _, outcome := range manual {
			fmt.Fprintf(writer, "  %s %s — %s\n", palette.yellow("!"), outcome.DisplayName, outcome.Reason)
			if outcome.URL != "" {
				fmt.Fprintf(writer, "      %s\n", palette.dim(outcome.URL))
			}
		}
	}

	if unsupported := r.unsupported(); len(unsupported) > 0 {
		fmt.Fprintf(writer, "\n%s\n", palette.bold("Not available on this platform"))
		for _, outcome := range unsupported {
			fmt.Fprintf(writer, "  %s %s — %s\n", palette.dim("—"), outcome.DisplayName, outcome.Reason)
		}
	}

	if services := r.notableServices(); len(services) > 0 {
		fmt.Fprintf(writer, "\n%s\n", palette.bold("Services"))
		for _, service := range services {
			fmt.Fprintf(writer, "  %-24s %s\n", service.Name, serviceLabel(service, palette))
		}
		fmt.Fprintf(writer, "  %s\n", palette.dim("Installed and running are separate: Bento does not start services you did not ask for."))
	}

	if len(r.Warnings) > 0 {
		fmt.Fprintf(writer, "\n%s\n", palette.bold("Warnings"))
		for _, warning := range r.Warnings {
			fmt.Fprintf(writer, "  %s %s\n", palette.yellow("!"), warning)
		}
	}

	if changes := appliedChanges(r.Environment); len(changes) > 0 {
		fmt.Fprintf(writer, "\n%s\n", palette.bold("Environment changes"))
		for _, change := range changes {
			fmt.Fprintf(writer, "  %s %s\n", palette.green("✓"), change)
		}
	}

	for _, notice := range r.Notices {
		fmt.Fprintf(writer, "\n%s %s\n", palette.yellow("⚠"), notice)
	}

	if r.LogFile != "" {
		fmt.Fprintf(writer, "\n%s\n", palette.dim("Full log: "+r.LogFile))
	}
}

func (r Report) failures() []dependency.Outcome {
	return r.filter(func(o dependency.Outcome) bool { return o.Status == dependency.StatusFailed })
}

func (r Report) manual() []dependency.Outcome {
	return r.filter(func(o dependency.Outcome) bool {
		return o.Status == dependency.StatusSkipped && o.Reason != ""
	})
}

func (r Report) unsupported() []dependency.Outcome {
	return r.filter(func(o dependency.Outcome) bool { return o.Status == dependency.StatusUnsupported })
}

func (r Report) filter(keep func(dependency.Outcome) bool) []dependency.Outcome {
	var out []dependency.Outcome
	for _, outcome := range r.Dependencies {
		if keep(outcome) {
			out = append(out, outcome)
		}
	}
	return out
}

// notableServices lists services worth mentioning: anything that is not simply
// running.
func (r Report) notableServices() []dependency.ServiceStatus {
	var out []dependency.ServiceStatus
	seen := map[string]bool{}
	for _, service := range r.Services {
		if seen[service.Name] || service.State == dependency.ServiceUnknown {
			continue
		}
		seen[service.Name] = true
		out = append(out, service)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func serviceLabel(service dependency.ServiceStatus, palette Palette) string {
	switch service.State {
	case dependency.ServiceRunning:
		return palette.green("running")
	case dependency.ServiceStopped:
		return palette.yellow("installed, not running")
	case dependency.ServiceNotFound:
		return palette.dim("no service registered")
	case dependency.ServiceUnmanaged:
		return palette.dim("started by you, not by Bento")
	case dependency.ServiceStartFailed:
		return palette.red("could not be started")
	default:
		return palette.dim("unknown")
	}
}

func appliedChanges(changes []environment.Change) []string {
	var out []string
	for _, change := range changes {
		if change.Applied {
			out = append(out, change.String())
		}
	}
	return out
}

// --- box drawing ----------------------------------------------------------

type boxWriter struct {
	writer  io.Writer
	width   int
	palette Palette
}

func (b *boxWriter) top()     { fmt.Fprintf(b.writer, "╭%s╮\n", strings.Repeat("─", b.width)) }
func (b *boxWriter) bottom()  { fmt.Fprintf(b.writer, "╰%s╯\n", strings.Repeat("─", b.width)) }
func (b *boxWriter) divider() { fmt.Fprintf(b.writer, "├%s┤\n", strings.Repeat("─", b.width)) }

// row prints one line. The visible width is measured on the uncoloured text,
// because ANSI escape sequences take no space on screen but plenty in a string.
func (b *boxWriter) row(coloured, plain string) {
	if plain == "" {
		plain = stripANSI(coloured)
	}
	padding := b.width - 2 - displayWidth(plain)
	if padding < 0 {
		padding = 0
	}
	fmt.Fprintf(b.writer, "│ %s%s │\n", coloured, strings.Repeat(" ", padding))
}

func (b *boxWriter) centre(text string) {
	space := b.width - 2 - displayWidth(text)
	if space < 0 {
		space = 0
	}
	left := space / 2
	fmt.Fprintf(b.writer, "│ %s%s%s │\n",
		strings.Repeat(" ", left), b.palette.bold(text), strings.Repeat(" ", space-left))
}

func (b *boxWriter) field(name, value string) {
	b.row(pad(name+":", 18)+value, "")
}

func (b *boxWriter) dependency(outcome dependency.Outcome) {
	version := outcome.Version
	if version == "" {
		version = "—"
	}
	status := string(outcome.Status)

	plain := pad(truncate(outcome.DisplayName, 21), 22) + pad(truncate(version, 13), 14) +
		outcome.Status.Symbol() + " " + status
	coloured := pad(truncate(outcome.DisplayName, 21), 22) + pad(truncate(version, 13), 14) +
		b.colourise(outcome.Status, outcome.Status.Symbol()+" "+status)

	b.row(coloured, plain)
}

func (b *boxWriter) colourise(status dependency.Status, text string) string {
	switch status {
	case dependency.StatusInstalled, dependency.StatusAlreadyInstalled, dependency.StatusUpdated, dependency.StatusVerified:
		return b.palette.green(text)
	case dependency.StatusFailed:
		return b.palette.red(text)
	case dependency.StatusWarning:
		return b.palette.yellow(text)
	default:
		return b.palette.dim(text)
	}
}

func pad(text string, width int) string { return textwidth.Pad(text, width) }

func truncate(text string, width int) string { return textwidth.Truncate(text, width) }

// displayWidth measures printed columns. Emoji occupy two columns and colour
// escapes occupy none, and a box only looks like a box if both are counted
// correctly.
func displayWidth(text string) int { return textwidth.Of(text) }

func stripANSI(text string) string { return textwidth.StripANSI(text) }

func formatDuration(seconds float64) string {
	duration := time.Duration(seconds * float64(time.Second))
	switch {
	case duration < time.Second:
		return duration.Round(time.Millisecond).String()
	case duration < time.Minute:
		return duration.Round(100 * time.Millisecond).String()
	default:
		return duration.Round(time.Second).String()
	}
}
