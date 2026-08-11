package command

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Mock is a Runner for tests. It never touches the operating system: every
// invocation is matched against registered responses, and unmatched commands
// fail loudly so a test can never silently exercise the real machine.
type Mock struct {
	mu sync.Mutex

	// responses maps a command prefix ("winget install") to the canned result
	// returned for any invocation starting with that prefix. The longest
	// matching prefix wins, so specific stubs override general ones.
	responses map[string]Result
	// paths holds executables that LookPath should resolve.
	paths map[string]string
	// Calls records every invocation in order, for assertions.
	Calls []Spec
	// Default is returned when no response matches and Strict is false.
	Default Result
	// Strict makes unmatched commands return an error instead of Default.
	Strict bool
}

// NewMock returns an empty Mock. By default unmatched commands return exit
// code 127 ("command not found"), which models a bare machine.
func NewMock() *Mock {
	return &Mock{
		responses: map[string]Result{},
		paths:     map[string]string{},
		Default:   Result{ExitCode: 127, Stderr: "command not found"},
	}
}

// Respond registers the result returned for commands whose rendered form
// starts with prefix. It also makes the executable resolvable via LookPath.
func (m *Mock) Respond(prefix string, result Result) *Mock {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.responses[prefix] = result
	if name, _, ok := strings.Cut(prefix, " "); ok {
		m.paths[name] = "/mock/" + name
	} else {
		m.paths[prefix] = "/mock/" + prefix
	}
	return m
}

// RespondOK registers a successful result with the given stdout.
func (m *Mock) RespondOK(prefix, stdout string) *Mock {
	return m.Respond(prefix, Result{Stdout: stdout})
}

// RespondFail registers a failing result.
func (m *Mock) RespondFail(prefix string, exitCode int, stderr string) *Mock {
	return m.Respond(prefix, Result{ExitCode: exitCode, Stderr: stderr})
}

// AddPath makes an executable resolvable without registering a result.
func (m *Mock) AddPath(name, path string) *Mock {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.paths[name] = path
	return m
}

// Run implements Runner.
func (m *Mock) Run(_ context.Context, spec Spec) (Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.Calls = append(m.Calls, spec)
	rendered := spec.String()

	best, bestLen, found := Result{}, -1, false
	for prefix, result := range m.responses {
		if strings.HasPrefix(rendered, prefix) && len(prefix) > bestLen {
			best, bestLen, found = result, len(prefix), true
		}
	}
	if !found {
		if m.Strict {
			return Result{ExitCode: -1}, fmt.Errorf("mock: unexpected command %q", rendered)
		}
		best = m.Default
	}
	best.Command = rendered
	return best, nil
}

// LookPath implements Runner.
func (m *Mock) LookPath(name string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if path, ok := m.paths[name]; ok {
		return path, nil
	}
	return "", fmt.Errorf("%q: %w", name, ErrNotFound)
}

// Invocations returns the rendered form of every command that was run.
func (m *Mock) Invocations() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.Calls))
	for _, call := range m.Calls {
		out = append(out, call.String())
	}
	return out
}

// Ran reports whether any invocation started with the given prefix.
func (m *Mock) Ran(prefix string) bool {
	for _, invocation := range m.Invocations() {
		if strings.HasPrefix(invocation, prefix) {
			return true
		}
	}
	return false
}

// Reset clears recorded calls while keeping registered responses.
func (m *Mock) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = nil
}
