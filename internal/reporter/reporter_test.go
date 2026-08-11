package reporter

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Aryan27-max/bento-box/internal/dependency"
	"github.com/Aryan27-max/bento-box/internal/detector"
	"github.com/Aryan27-max/bento-box/internal/environment"
)

func sampleOutcomes() []dependency.Outcome {
	return []dependency.Outcome{
		{Name: "git", DisplayName: "Git", Category: dependency.CategoryDevelopment,
			Status: dependency.StatusAlreadyInstalled, Version: "2.47.1"},
		{Name: "node", DisplayName: "Node.js", Category: dependency.CategoryRuntime,
			Status: dependency.StatusInstalled, Version: "22.14.0"},
		{Name: "python", DisplayName: "Python", Category: dependency.CategoryLanguage,
			Status: dependency.StatusUpdated, Version: "3.13.1", PreviousVersion: "3.9.2"},
		{Name: "redis", DisplayName: "Redis", Category: dependency.CategoryDatabase,
			Status: dependency.StatusUnsupported, Reason: "Redis publishes no official Windows build."},
		{Name: "postman", DisplayName: "Postman", Category: dependency.CategoryGUIApplication,
			Status: dependency.StatusSkipped, Reason: "Postman ships a tarball for Linux.",
			URL: "https://www.postman.com/downloads/"},
		{Name: "docker", DisplayName: "Docker", Category: dependency.CategoryService,
			Status: dependency.StatusInstalled, Version: "28.0.1",
			Service: &dependency.ServiceStatus{Name: "docker", State: dependency.ServiceStopped}},
	}
}

func sampleInput() Input {
	return Input{
		Version: "0.1.0",
		System: detector.System{
			OS: "linux", Arch: "amd64", OSName: "Ubuntu 24.04.1 LTS", Home: "/home/dev",
		},
		ProfileID:   "web",
		ProfileName: "Web",
		Outcomes:    sampleOutcomes(),
		Duration:    92 * time.Second,
	}
}

func TestSummaryCountsEachStatusSeparately(t *testing.T) {
	report := Build(sampleInput())

	if report.Summary.Installed != 2 {
		t.Errorf("Installed = %d, want 2", report.Summary.Installed)
	}
	if report.Summary.AlreadyInstalled != 1 {
		t.Errorf("AlreadyInstalled = %d, want 1", report.Summary.AlreadyInstalled)
	}
	if report.Summary.Updated != 1 {
		t.Errorf("Updated = %d, want 1", report.Summary.Updated)
	}
	if report.Summary.Unsupported != 1 {
		t.Errorf("Unsupported = %d, want 1", report.Summary.Unsupported)
	}
	if report.Summary.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", report.Summary.Skipped)
	}
	if report.Summary.Total != 6 {
		t.Errorf("Total = %d, want 6", report.Summary.Total)
	}
}

// TestUnsupportedAndSkippedDoNotBlockReadiness: a platform that cannot run
// Redis is not a broken environment.
func TestUnsupportedAndSkippedDoNotBlockReadiness(t *testing.T) {
	report := Build(sampleInput())
	if !report.EnvironmentReady {
		t.Error("unsupported and skipped dependencies must not make the environment 'not ready'")
	}
}

func TestFailureMakesTheEnvironmentNotReady(t *testing.T) {
	input := sampleInput()
	input.Outcomes = append(input.Outcomes, dependency.Outcome{
		Name: "rust", DisplayName: "Rust", Status: dependency.StatusFailed,
		Errors: []string{"rustup-init exited with code 1"},
	})

	report := Build(input)
	if report.EnvironmentReady {
		t.Error("a failed dependency must make the environment not ready")
	}
	if len(report.Errors) != 1 || !strings.Contains(report.Errors[0], "Rust") {
		t.Errorf("Errors = %v, want the failure attributed to Rust", report.Errors)
	}
}

// TestDryRunIsNeverReported As Ready: nothing was changed, so claiming the
// machine is ready would be false.
func TestDryRunIsNeverReportedAsReady(t *testing.T) {
	input := sampleInput()
	input.DryRun = true

	report := Build(input)
	if report.EnvironmentReady {
		t.Error("a dry run must never report the environment as ready")
	}
}

func TestRestartRequirementIsPropagated(t *testing.T) {
	input := sampleInput()
	input.Outcomes = append(input.Outcomes, dependency.Outcome{
		Name: "docker-desktop", DisplayName: "Docker Desktop",
		Status: dependency.StatusInstalled, RestartRequired: true,
	})

	report := Build(input)
	if !report.RestartRequired {
		t.Error("restart_required should be set when an install needs a reboot")
	}
	joined := strings.Join(report.Notices, " ")
	if !strings.Contains(joined, "restart") {
		t.Errorf("notices = %v, want an explanation of the restart", report.Notices)
	}
	// Bento must say it will not reboot; silently rebooting would be hostile.
	if !strings.Contains(joined, "will not restart") {
		t.Errorf("notices = %v, want a promise not to reboot", report.Notices)
	}
}

func TestShellRestartNoticeIsRecorded(t *testing.T) {
	input := sampleInput()
	input.RestartNotice = "Environment changes were written to /home/dev/.bashrc. Open a new terminal."

	report := Build(input)
	if !report.ShellRestartRequired {
		t.Error("shell_restart_required should be set when the environment changed")
	}
}

func TestJSONReportHasTheDocumentedShape(t *testing.T) {
	report := Build(sampleInput())

	var buffer bytes.Buffer
	if err := report.WriteJSONTo(&buffer); err != nil {
		t.Fatalf("WriteJSONTo: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(buffer.Bytes(), &decoded); err != nil {
		t.Fatalf("the report is not valid JSON: %v", err)
	}

	for _, key := range []string{
		"timestamp", "platform", "architecture", "profile",
		"environment_ready", "dependencies", "summary", "restart_required",
	} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("report.json is missing the %q field", key)
		}
	}

	if decoded["platform"] != "linux" {
		t.Errorf("platform = %v, want linux", decoded["platform"])
	}
	if decoded["profile"] != "web" {
		t.Errorf("profile = %v, want web", decoded["profile"])
	}

	summary := decoded["summary"].(map[string]any)
	for _, key := range []string{"installed", "already_installed", "updated", "failed", "skipped"} {
		if _, ok := summary[key]; !ok {
			t.Errorf("summary is missing the %q field", key)
		}
	}
}

func TestWriteJSONCreatesBentoReport(t *testing.T) {
	home := t.TempDir()
	report := Build(sampleInput())

	path, err := report.WriteJSON(home)
	if err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	if want := filepath.Join(home, ".bento", "report.json"); path != want {
		t.Errorf("path = %q, want %q", path, want)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the report: %v", err)
	}
	var decoded Report
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("the written report does not round-trip: %v", err)
	}
	if len(decoded.Dependencies) != len(report.Dependencies) {
		t.Errorf("round-tripped %d dependencies, want %d", len(decoded.Dependencies), len(report.Dependencies))
	}
}

func TestRenderShowsEveryOutcome(t *testing.T) {
	var buffer bytes.Buffer
	Build(sampleInput()).Render(&buffer, Palette{})
	output := buffer.String()

	for _, want := range []string{
		"BENTO BOX", "INSTALLATION REPORT",
		"Ubuntu 24.04.1 LTS", "amd64", "Web",
		"Git", "2.47.1", "ALREADY_INSTALLED",
		"Node.js", "22.14.0", "INSTALLED",
		"Python", "UPDATED",
		"Redis", "UNSUPPORTED",
		"READY",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("report is missing %q:\n%s", want, output)
		}
	}
}

func TestRenderExplainsUnsupportedAndManualDependencies(t *testing.T) {
	var buffer bytes.Buffer
	Build(sampleInput()).Render(&buffer, Palette{})
	output := buffer.String()

	if !strings.Contains(output, "no official Windows build") {
		t.Error("the report should carry the reason a dependency is unsupported")
	}
	if !strings.Contains(output, "https://www.postman.com/downloads/") {
		t.Error("a manual dependency should show where to get it")
	}
}

// TestRenderReportsStoppedServicesWithoutCallingThemFailures.
func TestRenderReportsStoppedServicesWithoutCallingThemFailures(t *testing.T) {
	var buffer bytes.Buffer
	Build(sampleInput()).Render(&buffer, Palette{})
	output := buffer.String()

	if !strings.Contains(output, "installed, not running") {
		t.Errorf("a stopped service should be described plainly:\n%s", output)
	}
	if strings.Contains(output, "SOME DEPENDENCIES FAILED") {
		t.Error("a stopped service is not a failed installation")
	}
}

// TestBoxIsRectangular: every line of the box must be the same width, or the
// report looks broken in a terminal.
func TestBoxIsRectangular(t *testing.T) {
	var buffer bytes.Buffer
	Build(sampleInput()).Render(&buffer, Palette{})

	var width int
	for _, line := range strings.Split(buffer.String(), "\n") {
		if !strings.HasPrefix(line, "│") && !strings.HasPrefix(line, "╭") &&
			!strings.HasPrefix(line, "├") && !strings.HasPrefix(line, "╰") {
			continue
		}
		got := displayWidth(line)
		if width == 0 {
			width = got
			continue
		}
		if got != width {
			t.Errorf("line %q is %d wide, want %d", line, got, width)
		}
	}
	if width == 0 {
		t.Fatal("no box was rendered")
	}
}

// TestColouredBoxStaysRectangular: ANSI escapes take no space on screen, and
// the width calculation has to know that.
func TestColouredBoxStaysRectangular(t *testing.T) {
	palette := Palette{
		Green:  func(s string) string { return "\x1b[32m" + s + "\x1b[0m" },
		Red:    func(s string) string { return "\x1b[31m" + s + "\x1b[0m" },
		Yellow: func(s string) string { return "\x1b[33m" + s + "\x1b[0m" },
		Dim:    func(s string) string { return "\x1b[2m" + s + "\x1b[0m" },
		Bold:   func(s string) string { return "\x1b[1m" + s + "\x1b[0m" },
	}

	var buffer bytes.Buffer
	Build(sampleInput()).Render(&buffer, palette)

	var width int
	for _, line := range strings.Split(buffer.String(), "\n") {
		if !strings.HasPrefix(line, "│") {
			continue
		}
		got := displayWidth(line)
		if width == 0 {
			width = got
			continue
		}
		if got != width {
			t.Errorf("coloured line %q measures %d, want %d", stripANSI(line), got, width)
		}
	}
}

func TestEnvironmentChangesAreListed(t *testing.T) {
	input := sampleInput()
	input.Environment = []environment.Change{
		{Kind: environment.ChangeVariable, Name: "GOPATH", Value: "/home/dev/go", Applied: true},
		{Kind: environment.ChangePath, Value: "/home/dev/go/bin", Applied: true},
		{Kind: environment.ChangeVariable, Name: "ANDROID_HOME", Value: "/nope", Applied: false, Reason: "does not exist"},
	}

	var buffer bytes.Buffer
	Build(input).Render(&buffer, Palette{})
	output := buffer.String()

	if !strings.Contains(output, "GOPATH=/home/dev/go") {
		t.Error("an applied variable should be listed")
	}
	if !strings.Contains(output, "PATH += /home/dev/go/bin") {
		t.Error("an applied PATH entry should be listed")
	}
	if strings.Contains(output, "ANDROID_HOME=/nope") {
		t.Error("a change that was not applied must not be listed as one that was")
	}
}

func TestTruncateKeepsTheBoxIntact(t *testing.T) {
	input := sampleInput()
	input.Outcomes = []dependency.Outcome{{
		Name:        "very-long",
		DisplayName: "A Dependency With An Unreasonably Long Display Name",
		Status:      dependency.StatusInstalled,
		Version:     "1.2.3-with-a-very-long-suffix",
	}}

	var buffer bytes.Buffer
	Build(input).Render(&buffer, Palette{})

	var width int
	for _, line := range strings.Split(buffer.String(), "\n") {
		if !strings.HasPrefix(line, "│") {
			continue
		}
		if width == 0 {
			width = displayWidth(line)
		} else if displayWidth(line) != width {
			t.Errorf("long names broke the box: %q", line)
		}
	}
}
