// Package command provides the single abstraction through which Bento executes
// external programs. Nothing else in the codebase is allowed to call os/exec
// directly: routing every invocation through a Runner keeps execution
// traceable, loggable, timeout-bounded and — most importantly — mockable in
// unit tests so that `go test ./...` never touches the real system.
package command

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// DefaultTimeout bounds any command that does not specify its own timeout.
// Package-manager installs are slow, so this is generous rather than tight.
const DefaultTimeout = 30 * time.Minute

// Spec describes a single external command invocation.
type Spec struct {
	// Name is the executable to run (resolved via PATH unless absolute).
	Name string
	// Args are passed to the executable verbatim; they are never shell-expanded
	// because Bento does not run commands through a shell.
	Args []string
	// Dir is the working directory. Empty means "inherit".
	Dir string
	// Env holds additional KEY=VALUE pairs layered on top of the parent
	// environment. It does not replace the parent environment.
	Env []string
	// Stdin, when non-empty, is written to the process's standard input.
	Stdin string
	// Timeout overrides DefaultTimeout when non-zero.
	Timeout time.Duration
	// Stream, when non-nil, receives combined output as it is produced. The
	// captured Result still contains the full output.
	Stream io.Writer
	// AllowFailure marks a command whose non-zero exit is an expected outcome
	// (probes, "is it installed?" checks) rather than an error condition. It is
	// advisory metadata for the logger; Run reports the exit code either way.
	AllowFailure bool
}

// String renders the invocation for logs. Arguments are shown space-separated;
// this is a display form, not something that can be pasted into a shell safely.
func (s Spec) String() string {
	if len(s.Args) == 0 {
		return s.Name
	}
	return s.Name + " " + strings.Join(s.Args, " ")
}

// Result captures everything observed about a finished command.
type Result struct {
	Command  string
	Stdout   string
	Stderr   string
	ExitCode int
	Duration time.Duration
	// TimedOut reports that the command was killed because it exceeded its
	// timeout rather than exiting on its own.
	TimedOut bool
}

// Success reports whether the command exited cleanly.
func (r Result) Success() bool { return r.ExitCode == 0 && !r.TimedOut }

// Output returns stdout with surrounding whitespace removed, falling back to
// stderr when stdout is empty. Many tools (notably `java -version`) report
// their version on stderr.
func (r Result) Output() string {
	if out := strings.TrimSpace(r.Stdout); out != "" {
		return out
	}
	return strings.TrimSpace(r.Stderr)
}

// ErrNotFound is returned by LookPath when an executable is not on PATH.
var ErrNotFound = errors.New("executable not found")

// Runner executes commands and resolves executables. Production code uses
// SystemRunner; tests use Mock.
type Runner interface {
	// Run executes the spec. A non-zero exit code is reported in the Result and
	// is not an error: only failure to start, context cancellation and timeouts
	// produce a non-nil error.
	Run(ctx context.Context, spec Spec) (Result, error)
	// LookPath resolves an executable name to an absolute path, returning a
	// wrapped ErrNotFound when it is not available.
	LookPath(name string) (string, error)
}

// SystemRunner is the real Runner, backed by os/exec.
type SystemRunner struct {
	// Observer, when non-nil, is invoked after every command with the spec and
	// its result. The logger uses this to record an audit trail.
	Observer func(Spec, Result)
}

// NewSystemRunner returns a Runner that executes commands on this machine.
func NewSystemRunner() *SystemRunner { return &SystemRunner{} }

// Run implements Runner.
func (r *SystemRunner) Run(ctx context.Context, spec Spec) (Result, error) {
	timeout := spec.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, spec.Name, spec.Args...)
	cmd.Dir = spec.Dir
	if len(spec.Env) > 0 {
		cmd.Env = append(os.Environ(), spec.Env...)
	}
	if spec.Stdin != "" {
		cmd.Stdin = strings.NewReader(spec.Stdin)
	}

	var stdout, stderr bytes.Buffer
	if spec.Stream != nil {
		cmd.Stdout = io.MultiWriter(&stdout, spec.Stream)
		cmd.Stderr = io.MultiWriter(&stderr, spec.Stream)
	} else {
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
	}

	start := time.Now()
	err := cmd.Run()
	result := Result{
		Command:  spec.String(),
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: time.Since(start),
	}

	switch {
	case err == nil:
		result.ExitCode = 0
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		result.TimedOut = true
		result.ExitCode = -1
		err = fmt.Errorf("command %q timed out after %s", spec.String(), timeout)
	case errors.Is(ctx.Err(), context.Canceled):
		result.ExitCode = -1
		err = fmt.Errorf("command %q canceled: %w", spec.String(), ctx.Err())
	default:
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			// A non-zero exit is data, not an error: callers decide what it means.
			result.ExitCode = exitErr.ExitCode()
			err = nil
		} else {
			result.ExitCode = -1
			err = fmt.Errorf("failed to start %q: %w", spec.String(), err)
		}
	}

	if r.Observer != nil {
		r.Observer(spec, result)
	}
	return result, err
}

// LookPath implements Runner.
func (r *SystemRunner) LookPath(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%q: %w", name, ErrNotFound)
	}
	return path, nil
}

// Available reports whether an executable can be resolved on PATH. It is the
// cheapest possible presence check and never runs the program.
func Available(r Runner, name string) bool {
	_, err := r.LookPath(name)
	return err == nil
}
