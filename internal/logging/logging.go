// Package logging writes Bento's audit trail. Every external command Bento
// runs is recorded with its exit status and duration, which is what makes a
// failed installation diagnosable after the fact instead of a mystery.
package logging

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Aryan27-max/bento-box/internal/command"
)

// Logger writes timestamped lines to a file.
type Logger struct {
	mu     sync.Mutex
	writer io.WriteCloser
	path   string
}

// New opens a log file inside Bento's home directory. A logger that fails to
// open still works: it discards output rather than taking the whole run down
// over a log file.
func New(bentoHome string) *Logger {
	directory := filepath.Join(bentoHome, "logs")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return &Logger{writer: nopCloser{io.Discard}}
	}

	path := filepath.Join(directory, fmt.Sprintf("bento-%s.log", time.Now().Format("20060102-150405")))
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return &Logger{writer: nopCloser{io.Discard}}
	}
	return &Logger{writer: file, path: path}
}

// NewWriter returns a Logger writing to an arbitrary destination, for tests.
func NewWriter(writer io.Writer) *Logger {
	return &Logger{writer: nopCloser{writer}}
}

// Path returns the log file's location, or an empty string when logging is
// being discarded.
func (l *Logger) Path() string { return l.path }

// Close releases the log file.
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.writer.Close()
}

// Printf writes one timestamped line.
func (l *Logger) Printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.writer, "%s %s\n", time.Now().Format(time.RFC3339), fmt.Sprintf(format, args...))
}

// Section writes a heading, used to separate one dependency from the next.
func (l *Logger) Section(name string) {
	l.Printf("=== %s ===", name)
}

// Command records an executed command. Output is included because it is the
// only thing that explains a failure, but truncated so one noisy installer
// cannot bury the rest of the log.
func (l *Logger) Command(spec command.Spec, result command.Result) {
	status := "ok"
	switch {
	case result.TimedOut:
		status = "timeout"
	case result.ExitCode != 0:
		status = fmt.Sprintf("exit %d", result.ExitCode)
	}

	l.Printf("$ %s [%s in %s]", spec.String(), status, result.Duration.Round(time.Millisecond))
	if env := redactEnvironment(spec.Env); len(env) > 0 {
		l.Printf("  env: %s", strings.Join(env, " "))
	}
	if output := strings.TrimSpace(result.Stdout); output != "" {
		l.Printf("  stdout: %s", truncate(output))
	}
	if output := strings.TrimSpace(result.Stderr); output != "" {
		l.Printf("  stderr: %s", truncate(output))
	}
}

// Observer returns a function suitable for command.SystemRunner.Observer, so
// that attaching the log to the runner records every command automatically.
func (l *Logger) Observer() func(command.Spec, command.Result) {
	return func(spec command.Spec, result command.Result) { l.Command(spec, result) }
}

// sensitiveNames are environment variables whose values must never reach the
// log. Bento does not set any of these itself, but it inherits the user's
// environment and a careless future change should not leak a token.
var sensitiveNames = []string{
	"TOKEN", "SECRET", "PASSWORD", "PASSWD", "APIKEY", "API_KEY",
	"CREDENTIAL", "PRIVATE_KEY", "SESSION", "AUTH",
}

// redactEnvironment replaces the value of anything that looks like a secret
// with a placeholder, keeping the variable name so the log still shows what
// was set.
func redactEnvironment(env []string) []string {
	if len(env) == 0 {
		return nil
	}
	out := make([]string, 0, len(env))
	for _, entry := range env {
		name, _, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		if isSensitive(name) {
			out = append(out, name+"=<redacted>")
			continue
		}
		out = append(out, entry)
	}
	return out
}

func isSensitive(name string) bool {
	upper := strings.ToUpper(name)
	for _, marker := range sensitiveNames {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return false
}

func truncate(text string) string {
	const limit = 4000
	text = strings.ReplaceAll(text, "\n", "\n    ")
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "… (truncated)"
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }
