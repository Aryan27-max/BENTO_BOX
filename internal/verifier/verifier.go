// Package verifier answers one question honestly: is this dependency actually
// present, and which version is it? Every answer comes from running the tool
// or looking at the filesystem. Nothing here ever reports a version it did not
// observe.
package verifier

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Aryan27-max/bento-box/internal/command"
	"github.com/Aryan27-max/bento-box/internal/dependency"
	"github.com/Aryan27-max/bento-box/internal/paths"
	"github.com/Aryan27-max/bento-box/internal/version"
)

// Result is what verification observed.
type Result struct {
	// Present reports whether the dependency was found.
	Present bool
	// Version is the parsed version, zero when none could be determined.
	Version version.Version
	// VersionKnown distinguishes "version 0.0.0" from "no version observed".
	VersionKnown bool
	// RawOutput is the tool's own output, kept for the log.
	RawOutput string
	// Location is where the dependency was found: an executable path or an
	// application bundle.
	Location string
	// Components records verification of sub-tools such as cargo and clippy.
	Components []dependency.ComponentStatus
	// Problem explains a failure in user-facing language.
	Problem string
}

// VersionString renders the observed version, or an empty string when none was
// observed. It never invents a number.
func (r Result) VersionString() string {
	if !r.VersionKnown {
		return ""
	}
	return r.Version.String()
}

// MissingComponents lists required sub-tools that were not found.
func (r Result) MissingComponents() []string {
	var missing []string
	for _, component := range r.Components {
		if !component.Present {
			missing = append(missing, component.Name)
		}
	}
	return missing
}

// Verifier checks dependencies against a machine.
type Verifier struct {
	Runner command.Runner
	// OS is the operating system being verified against.
	OS string
	// Home is the user's home directory, used to expand ${HOME}.
	Home string
	// Getenv reads environment variables during placeholder expansion.
	Getenv func(string) string
	// Exists reports whether a filesystem path exists. Injectable so GUI
	// application checks can be tested without installing anything.
	Exists func(string) bool
	// ExtraPath holds directories added during this run. A tool installed
	// minutes ago is not on the PATH this process inherited, so verification
	// looks in these directories too rather than falsely reporting a failure.
	ExtraPath []string
}

// New returns a Verifier for the running machine.
func New(runner command.Runner, osName, home string) *Verifier {
	return &Verifier{
		Runner: runner,
		OS:     osName,
		Home:   home,
		Getenv: os.Getenv,
		Exists: pathExists,
	}
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Verify checks one dependency and reports what was observed.
func (v *Verifier) Verify(ctx context.Context, spec dependency.Spec) Result {
	verification := spec.VerificationFor(v.OS)

	// GUI applications are checked on disk first: VS Code, Postman and
	// Compass are frequently installed without ever landing on PATH.
	if len(verification.Paths) > 0 {
		if result, found := v.verifyPaths(verification); found {
			result.Components = v.verifyComponents(ctx, verification)
			// A GUI application may also expose a CLI; if so, take its
			// version rather than reporting none.
			if verification.Command != "" && !verification.SkipVersion {
				if command := v.verifyCommand(ctx, verification); command.Present && command.VersionKnown {
					result.Version, result.VersionKnown, result.RawOutput = command.Version, true, command.RawOutput
				}
			}
			return result
		}
	}

	if verification.Command == "" {
		return Result{Problem: fmt.Sprintf("%s has no way to be verified on %s", spec.Label(), v.OS)}
	}

	result := v.verifyCommand(ctx, verification)
	result.Components = v.verifyComponents(ctx, verification)
	return result
}

func (v *Verifier) verifyPaths(verification dependency.Verification) (Result, bool) {
	exists := v.Exists
	if exists == nil {
		exists = pathExists
	}
	lookup := paths.Lookup(v.Home, v.Getenv)

	for _, candidate := range verification.Paths[v.OS] {
		expanded := paths.Expand(candidate, lookup)
		if expanded != "" && exists(expanded) {
			return Result{Present: true, Location: expanded}, true
		}
	}
	return Result{}, false
}

func (v *Verifier) verifyCommand(ctx context.Context, verification dependency.Verification) Result {
	executable, found := v.resolve(verification.Command)
	if !found {
		return Result{Problem: fmt.Sprintf("%s was not found on PATH", verification.Command)}
	}

	result := Result{Present: true, Location: executable}
	if verification.SkipVersion {
		return result
	}

	run, err := v.Runner.Run(ctx, command.Spec{
		Name: executable, Args: verification.Args, AllowFailure: true,
		Env: v.pathEnv(),
	})
	if err != nil {
		result.Problem = err.Error()
		return result
	}

	output := run.Output()
	result.RawOutput = output
	parsed, parseErr := version.Extract(output, verification.Pattern)

	// Resolving the executable is not proof that the dependency is there.
	// Python libraries are verified with `python -c "import torch"`: the
	// interpreter always resolves, so only the exit code distinguishes an
	// installed library from a missing one. A failing check with no version
	// in its output means the dependency is absent.
	if !run.Success() && parseErr != nil {
		result.Present = false
		if output == "" {
			result.Problem = fmt.Sprintf("%s exited with code %d", verification.Command, run.ExitCode)
		} else {
			result.Problem = fmt.Sprintf("%s failed: %s", verification.Command, firstLine(output))
		}
		return result
	}

	if parseErr != nil {
		// The tool ran cleanly; Bento simply could not read a version out of
		// what it said. That is a warning, not an absence.
		result.Problem = fmt.Sprintf("could not read a version from %q", firstLine(output))
		return result
	}
	result.Version, result.VersionKnown = parsed, true
	return result
}

func (v *Verifier) verifyComponents(ctx context.Context, verification dependency.Verification) []dependency.ComponentStatus {
	if len(verification.Components) == 0 {
		return nil
	}

	statuses := make([]dependency.ComponentStatus, 0, len(verification.Components))
	for _, component := range verification.Components {
		status := dependency.ComponentStatus{Name: component.Name}

		if executable, found := v.resolve(component.Command); found {
			run, err := v.Runner.Run(ctx, command.Spec{
				Name: executable, Args: component.Args, AllowFailure: true, Env: v.pathEnv(),
			})
			if err == nil {
				parsed, parseErr := version.Extract(run.Output(), component.Pattern)
				// A component is only present if its own invocation worked.
				// `cargo clippy --version` resolves cargo either way, so a
				// missing clippy shows up as a failing exit code with no
				// version — and must be reported as missing, not present.
				if run.Success() || parseErr == nil {
					status.Present = true
				}
				if parseErr == nil {
					status.Version = parsed.String()
				}
			}
		}
		statuses = append(statuses, status)
	}
	return statuses
}

// resolve finds an executable, looking first on the inherited PATH and then in
// the directories Bento has added during this run.
func (v *Verifier) resolve(name string) (string, bool) {
	if path, err := v.Runner.LookPath(name); err == nil {
		return path, true
	}

	exists := v.Exists
	if exists == nil {
		exists = pathExists
	}
	for _, dir := range v.ExtraPath {
		for _, candidate := range executableNames(name, v.OS) {
			if full := filepath.Join(dir, candidate); exists(full) {
				return full, true
			}
		}
	}
	return "", false
}

// executableNames returns the filenames an executable might have. Windows
// hides the extension in every command a user types, but the file on disk is
// still git.exe or npm.cmd.
func executableNames(name, osName string) []string {
	if osName != "windows" {
		return []string{name}
	}
	if strings.ContainsAny(name, ".") {
		return []string{name}
	}
	return []string{name + ".exe", name + ".cmd", name + ".bat", name}
}

// pathEnv exposes the directories added during this run to the commands
// verification executes.
func (v *Verifier) pathEnv() []string {
	if len(v.ExtraPath) == 0 {
		return nil
	}
	getenv := v.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	separator := string(os.PathListSeparator)
	return []string{"PATH=" + strings.Join(v.ExtraPath, separator) + separator + getenv("PATH")}
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	if len(line) > 120 {
		return line[:120] + "…"
	}
	return line
}
