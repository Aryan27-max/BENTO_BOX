// Package pkgmanager wraps the system package managers behind one interface.
// The installer never knows whether it is talking to winget, apt or Homebrew;
// it asks a Manager to install a package and receives the same answer shape
// every time. Adding support for a new package manager means adding an adapter
// here and nothing else.
package pkgmanager

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Aryan27-max/bento-box/internal/command"
)

// Request describes an installation through a package manager.
type Request struct {
	// Packages are the manager-specific package identifiers.
	Packages []string
	// Cask selects a Homebrew cask instead of a formula.
	Cask bool
	// Classic selects classic confinement for a snap.
	Classic bool
	// ExtraArgs are appended to the manager's command line.
	ExtraArgs []string
	// Elevate marks an install that needs administrator or root privileges.
	Elevate bool
}

// Result reports what a package manager did.
type Result struct {
	// Command is the invocation that was run, for the log and the report.
	Command string
	Output  string
	// Success reports whether the package manager exited cleanly.
	Success bool
	// Message is a short human-readable explanation of a failure.
	Message string
}

// Manager is the interface every package manager adapter implements.
type Manager interface {
	// Name is the identifier used in dependency definitions.
	Name() string
	// IsAvailable reports whether this manager exists on the machine.
	IsAvailable() bool
	// Refresh updates the manager's package index. It is called at most once
	// per run, immediately before the first install through that manager,
	// because a fresh machine's index is usually empty or stale.
	Refresh(ctx context.Context) error
	// Install installs packages.
	Install(ctx context.Context, request Request) (Result, error)
	// Upgrade upgrades already-installed packages.
	Upgrade(ctx context.Context, request Request) (Result, error)
	// IsInstalled reports whether a single package is installed according to
	// the package manager's own database.
	IsInstalled(ctx context.Context, pkg string) bool
	// Version returns the installed version of a package as the manager
	// records it, or an empty string when it cannot be determined.
	Version(ctx context.Context, pkg string) string
	// InstallLocal installs a downloaded package file. Managers that cannot
	// do this return an error explaining so.
	InstallLocal(ctx context.Context, path string, elevate bool) (Result, error)
}

// Options configure the adapters.
type Options struct {
	Runner command.Runner
	// Elevated reports whether Bento is already running with root or
	// administrator privileges, in which case no sudo prefix is needed.
	Elevated bool
	// DryRun makes every mutating call report what it would have run without
	// running it. Read-only queries still execute so the plan is accurate.
	DryRun bool
}

// base holds the machinery shared by every adapter.
type base struct {
	runner   command.Runner
	elevated bool
	dryRun   bool
	// executable is the program that proves the manager exists.
	executable string
	refreshed  bool
}

// elevate prefixes a command with sudo when privileges are required and Bento
// is not already running as root. On Windows there is no sudo equivalent that
// works non-interactively, so elevation is reported as a requirement instead
// of being faked.
func (b *base) elevate(name string, args []string, needed bool) (string, []string, error) {
	if !needed || b.elevated {
		return name, args, nil
	}
	if !command.Available(b.runner, "sudo") {
		return "", nil, fmt.Errorf("this install needs root privileges but sudo is not available; re-run Bento as root")
	}
	// -n makes sudo fail immediately rather than blocking on a password
	// prompt that the user may not be watching for.
	return "sudo", append([]string{"-n", name}, args...), nil
}

// run executes a mutating command, honouring dry-run mode.
func (b *base) run(ctx context.Context, name string, args []string, elevate bool) (Result, error) {
	return b.runEnv(ctx, name, args, elevate, nil)
}

// runEnv is run with extra environment variables, which package managers need
// to stay non-interactive.
func (b *base) runEnv(ctx context.Context, name string, args []string, elevate bool, env []string) (Result, error) {
	name, args, err := b.elevate(name, args, elevate)
	if err != nil {
		return Result{Message: err.Error()}, err
	}
	spec := command.Spec{Name: name, Args: args, Env: env}

	if b.dryRun {
		return Result{Command: spec.String(), Success: true, Message: "dry run: not executed"}, nil
	}

	result, err := b.runner.Run(ctx, spec)
	outcome := Result{
		Command: spec.String(),
		Output:  strings.TrimSpace(result.Stdout + "\n" + result.Stderr),
		Success: result.Success(),
	}
	if err != nil {
		outcome.Message = err.Error()
		return outcome, err
	}
	if !outcome.Success {
		outcome.Message = summariseFailure(result)
		return outcome, fmt.Errorf("%s exited with code %d", spec.String(), result.ExitCode)
	}
	return outcome, nil
}

// query executes a read-only command. Queries run even in dry-run mode,
// because an accurate plan depends on knowing what is really installed.
func (b *base) query(ctx context.Context, name string, args ...string) command.Result {
	result, err := b.runner.Run(ctx, command.Spec{Name: name, Args: args, AllowFailure: true})
	if err != nil {
		return command.Result{ExitCode: -1}
	}
	return result
}

func (b *base) available() bool { return command.Available(b.runner, b.executable) }

// summariseFailure extracts the most useful line from a failed command so the
// report can say something specific instead of dumping pages of output.
func summariseFailure(result command.Result) string {
	text := strings.TrimSpace(result.Stderr)
	if text == "" {
		text = strings.TrimSpace(result.Stdout)
	}
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line != "" {
			if len(line) > 300 {
				line = line[:300] + "…"
			}
			return line
		}
	}
	return fmt.Sprintf("exit code %d", result.ExitCode)
}

// Registry holds the adapters available on this machine.
type Registry struct {
	managers map[string]Manager
}

// NewRegistry builds every adapter and keeps the ones whose executable is
// present. Availability is observed, never inferred from the OS.
func NewRegistry(options Options) *Registry {
	all := []Manager{
		NewWinget(options),
		NewApt(options),
		NewDnf(options),
		NewPacman(options),
		NewZypper(options),
		NewBrew(options),
		NewSnap(options),
	}

	registry := &Registry{managers: map[string]Manager{}}
	for _, manager := range all {
		if manager.IsAvailable() {
			registry.managers[manager.Name()] = manager
		}
	}
	return registry
}

// Get returns an available manager by name.
func (r *Registry) Get(name string) (Manager, bool) {
	manager, ok := r.managers[name]
	return manager, ok
}

// Available lists the names of every detected manager, sorted.
func (r *Registry) Available() []string {
	out := make([]string, 0, len(r.managers))
	for name := range r.managers {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Has reports whether a manager is available.
func (r *Registry) Has(name string) bool {
	_, ok := r.managers[name]
	return ok
}

// Register adds a manager, which tests use to inject fakes.
func (r *Registry) Register(manager Manager) { r.managers[manager.Name()] = manager }

// NewEmptyRegistry returns a registry with no managers, for tests.
func NewEmptyRegistry() *Registry { return &Registry{managers: map[string]Manager{}} }
