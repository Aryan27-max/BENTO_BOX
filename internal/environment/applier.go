package environment

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/Aryan27-max/bento-box/internal/command"
	"github.com/Aryan27-max/bento-box/internal/dependency"
	"github.com/Aryan27-max/bento-box/internal/paths"
)

// Applier turns the environment declarations in a dependency definition into
// concrete changes. Values are resolved from the tool itself wherever possible
// — GOROOT comes from `go env GOROOT`, never from a guess about where Go was
// installed — so that Bento configures what is actually on the machine.
type Applier struct {
	Manager *Manager
	Runner  command.Runner
	OS      string
	Home    string
	Getenv  func(string) string
	Exists  func(string) bool
	// ExtraPath holds directories added earlier in this run, so a command like
	// `go env GOPATH` can be found even though Go was installed minutes ago
	// and is not on this process's inherited PATH.
	ExtraPath []string
}

// Apply configures the environment for one dependency and returns what
// changed.
func (a *Applier) Apply(ctx context.Context, spec dependency.Spec) []Change {
	var changes []Change

	for _, variable := range spec.EnvironmentFor(a.OS) {
		changes = append(changes, a.applyVariable(ctx, variable))
	}
	for _, entry := range spec.PathEntriesFor(a.OS) {
		changes = append(changes, a.applyPath(ctx, entry))
	}
	return changes
}

func (a *Applier) applyVariable(ctx context.Context, variable dependency.EnvVar) Change {
	value, ok := a.resolveValue(ctx, variable.Value, variable.FromCommand)
	if !ok {
		return Change{
			Kind: ChangeVariable, Name: variable.Name,
			Reason: "value could not be resolved on this machine",
		}
	}

	// A variable that names a directory is only worth setting if that
	// directory exists. ANDROID_HOME pointing at an SDK that was never
	// downloaded would be worse than leaving it unset.
	if variable.RequirePath && !a.exists(value) {
		return Change{
			Kind: ChangeVariable, Name: variable.Name, Value: value,
			Reason: "skipped because " + value + " does not exist yet",
		}
	}

	return a.Manager.SetVariable(variable.Name, value)
}

func (a *Applier) applyPath(ctx context.Context, entry dependency.PathEntry) Change {
	var directory string

	if len(entry.FromCommand) > 0 {
		base, ok := a.runForValue(ctx, entry.FromCommand)
		if !ok {
			return Change{Kind: ChangePath, Reason: "could not determine the directory from " + strings.Join(entry.FromCommand, " ")}
		}
		directory = base
		if entry.Path != "" {
			directory = filepath.Join(base, paths.Expand(entry.Path, a.lookup()))
		}
	} else {
		directory = paths.Expand(entry.Path, a.lookup())
		if strings.Contains(directory, "${") {
			return Change{Kind: ChangePath, Value: directory, Reason: "contains an unresolved placeholder"}
		}
		// A literal directory that does not exist is not put on PATH. A
		// directory reported by the tool itself is trusted, because the tool
		// knows where it lives even when the directory is created on demand.
		if !a.exists(directory) {
			return Change{Kind: ChangePath, Value: directory, Reason: "skipped because the directory does not exist"}
		}
	}

	return a.Manager.AddPath(directory)
}

func (a *Applier) resolveValue(ctx context.Context, literal string, fromCommand []string) (string, bool) {
	if len(fromCommand) > 0 {
		return a.runForValue(ctx, fromCommand)
	}
	value := paths.Expand(literal, a.lookup())
	if value == "" || strings.Contains(value, "${") {
		return "", false
	}
	return value, true
}

// runForValue executes a command and takes its first line of output as the
// value. Anything that fails yields no value at all, rather than an empty
// string that would look like a successful answer.
func (a *Applier) runForValue(ctx context.Context, argv []string) (string, bool) {
	if len(argv) == 0 || a.Runner == nil {
		return "", false
	}

	name := argv[0]
	if resolved, err := a.Runner.LookPath(name); err == nil {
		name = resolved
	} else if found, ok := a.findInExtraPath(argv[0]); ok {
		name = found
	} else {
		return "", false
	}

	result, err := a.Runner.Run(ctx, command.Spec{
		Name: name, Args: argv[1:], AllowFailure: true, Env: a.pathEnv(),
	})
	if err != nil || !result.Success() {
		return "", false
	}

	value, _, _ := strings.Cut(strings.TrimSpace(result.Output()), "\n")
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	return value, true
}

func (a *Applier) findInExtraPath(name string) (string, bool) {
	candidates := []string{name}
	if a.OS == "windows" {
		candidates = []string{name + ".exe", name + ".cmd", name + ".bat", name}
	}
	for _, dir := range a.ExtraPath {
		for _, candidate := range candidates {
			full := filepath.Join(dir, candidate)
			if a.exists(full) {
				return full, true
			}
		}
	}
	return "", false
}

func (a *Applier) pathEnv() []string {
	if len(a.ExtraPath) == 0 {
		return nil
	}
	separator := string(os.PathListSeparator)
	return []string{"PATH=" + strings.Join(a.ExtraPath, separator) + separator + a.getenv("PATH")}
}

func (a *Applier) lookup() func(string) string {
	return paths.Lookup(a.Home, a.getenv)
}

func (a *Applier) getenv(name string) string {
	if a.Getenv != nil {
		return a.Getenv(name)
	}
	return os.Getenv(name)
}

func (a *Applier) exists(path string) bool {
	if a.Exists != nil {
		return a.Exists(path)
	}
	_, err := os.Stat(path)
	return err == nil
}
