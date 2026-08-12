// Package environment configures environment variables and PATH entries after
// a dependency is installed.
//
// Two rules govern everything here. The first is idempotence: running Bento
// twice must not add a PATH entry twice, and must not accumulate duplicate
// blocks in a shell configuration file. The second is minimalism: Bento writes
// to exactly one place per platform — the user's registry environment on
// Windows, one clearly-marked block in one shell file on Unix — and never
// touches a variable it does not own.
package environment

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Aryan27-max/bento-box/internal/paths"
)

// StartMarker and EndMarker delimit the block Bento manages in a shell
// configuration file. Everything outside them belongs to the user and is never
// read, moved or rewritten.
const (
	StartMarker = "# >>> bento box >>>"
	EndMarker   = "# <<< bento box <<<"
)

// ChangeKind distinguishes a variable assignment from a PATH addition.
type ChangeKind string

const (
	ChangeVariable ChangeKind = "variable"
	ChangePath     ChangeKind = "path"
)

// Change records one environment modification, applied or not.
type Change struct {
	Kind  ChangeKind `json:"kind"`
	Name  string     `json:"name,omitempty"`
	Value string     `json:"value"`
	// Applied reports whether this run actually changed something. A value
	// that was already correct is reported with Applied false and a reason,
	// which is how the report can say "already configured".
	Applied bool   `json:"applied"`
	Reason  string `json:"reason,omitempty"`
}

// String renders the change for the report.
func (c Change) String() string {
	if c.Kind == ChangePath {
		return "PATH += " + c.Value
	}
	return c.Name + "=" + c.Value
}

// State is Bento's record of what it has configured. It is the source of truth
// for regenerating the shell block, which is what makes repeated runs converge
// instead of appending.
type State struct {
	Variables map[string]string `json:"variables"`
	Path      []string          `json:"path"`
}

// Manager applies environment changes for one machine.
type Manager struct {
	// OS is windows, linux or darwin.
	OS string
	// Home is the user's home directory.
	Home string
	// ConfigFile is the shell file to write on Unix. Empty disables shell
	// configuration entirely.
	ConfigFile string
	// Shell names the user's shell, which decides the syntax of the block.
	Shell string
	// DryRun reports what would change without writing anything.
	DryRun bool

	backend backend
	state   State
	changes []Change
	added   []string
	dirty   bool
}

// backend abstracts where environment changes are persisted. Which one is used
// is decided by the target operating system, not by the platform Bento happens
// to be compiled for — a Manager configured for Linux writes a shell block
// even when the test running it is on Windows, which is what keeps the test
// suite from touching the developer's own registry.
type backend interface {
	variable(name string) (string, bool)
	path() []string
	setVariable(name, value string) error
	addPath(dir string) error
	// shellManaged reports whether Flush should regenerate a shell
	// configuration file. Windows manages its environment centrally and has
	// no such file.
	shellManaged() bool
}

// stateBackend is the Unix backend. It stores nothing itself: Bento's state
// file and the managed shell block, both written in Flush, are the record.
type stateBackend struct{}

func (stateBackend) variable(name string) (string, bool) { return os.LookupEnv(name) }
func (stateBackend) path() []string                      { return nil }
func (stateBackend) setVariable(string, string) error    { return nil }
func (stateBackend) addPath(string) error                { return nil }
func (stateBackend) shellManaged() bool                  { return true }

// New returns a Manager and loads any state from a previous run.
func New(osName, home, configFile, shell string, dryRun bool) *Manager {
	manager := &Manager{
		OS: osName, Home: home, ConfigFile: configFile, Shell: shell, DryRun: dryRun,
		backend: platformBackend(osName),
		state:   State{Variables: map[string]string{}},
	}
	manager.loadState()
	return manager
}

func (m *Manager) statePath() string {
	if m.Home == "" {
		return ""
	}
	return filepath.Join(paths.Home(m.Home), "environment.json")
}

func (m *Manager) loadState() {
	path := m.statePath()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var loaded State
	if err := json.Unmarshal(data, &loaded); err != nil {
		return
	}
	if loaded.Variables == nil {
		loaded.Variables = map[string]string{}
	}
	m.state = loaded
}

// Changes returns every change considered during this run.
func (m *Manager) Changes() []Change { return m.changes }

// AddedPath returns the directories added to PATH during this run. The
// verifier searches these, because a tool installed a moment ago is not on the
// PATH this process inherited at start-up.
func (m *Manager) AddedPath() []string { return m.added }

// SetVariable sets a user environment variable. Setting a variable to the
// value it already has is recorded but changes nothing.
func (m *Manager) SetVariable(name, value string) Change {
	change := Change{Kind: ChangeVariable, Name: name, Value: value}
	if name == "" || value == "" {
		change.Reason = "no value resolved"
		m.changes = append(m.changes, change)
		return change
	}

	if current, ok := m.currentVariable(name); ok && current == value {
		change.Reason = "already set"
		m.changes = append(m.changes, change)
		return change
	}

	if m.DryRun {
		change.Reason = "dry run: not written"
		m.changes = append(m.changes, change)
		return change
	}

	if err := m.backend.setVariable(name, value); err != nil {
		change.Reason = err.Error()
		m.changes = append(m.changes, change)
		return change
	}

	m.state.Variables[name] = value
	m.dirty = true
	change.Applied = true
	m.changes = append(m.changes, change)
	return change
}

// AddPath adds a directory to PATH exactly once. This is the guarantee that a
// user who runs Bento five times does not end up with five copies of the same
// directory on their PATH.
func (m *Manager) AddPath(dir string) Change {
	change := Change{Kind: ChangePath, Value: dir}
	if dir == "" {
		change.Reason = "no directory resolved"
		m.changes = append(m.changes, change)
		return change
	}
	windows := m.OS == "windows"
	dir = paths.NormaliseFor(dir, windows)
	change.Value = dir

	if containsEntry(m.currentPath(), dir, windows) {
		change.Reason = "already on PATH"
		m.changes = append(m.changes, change)
		m.rememberAdded(dir)
		return change
	}

	if m.DryRun {
		change.Reason = "dry run: not written"
		m.changes = append(m.changes, change)
		return change
	}

	if err := m.backend.addPath(dir); err != nil {
		change.Reason = err.Error()
		m.changes = append(m.changes, change)
		return change
	}

	if !containsEntry(m.state.Path, dir, windows) {
		m.state.Path = append(m.state.Path, dir)
	}
	m.dirty = true
	m.rememberAdded(dir)
	change.Applied = true
	m.changes = append(m.changes, change)
	return change
}

func (m *Manager) rememberAdded(dir string) {
	if !containsEntry(m.added, dir, m.OS == "windows") {
		m.added = append(m.added, dir)
	}
}

// currentPath returns the PATH entries Bento knows about: what the platform
// reports plus what Bento has recorded for this user.
func (m *Manager) currentPath() []string {
	entries := m.backend.path()
	entries = append(entries, m.state.Path...)
	if inherited := os.Getenv("PATH"); inherited != "" {
		entries = append(entries, strings.Split(inherited, string(os.PathListSeparator))...)
	}
	return entries
}

func (m *Manager) currentVariable(name string) (string, bool) {
	if value, ok := m.backend.variable(name); ok {
		return value, true
	}
	value, ok := m.state.Variables[name]
	return value, ok
}

// Flush persists Bento's state and rewrites the managed shell block. It is
// safe to call when nothing changed.
func (m *Manager) Flush() error {
	if m.DryRun || !m.dirty {
		return nil
	}
	if err := m.saveState(); err != nil {
		return err
	}
	if !m.backend.shellManaged() {
		// Windows keeps the user environment in the registry, which the
		// backend has already updated; there is no shell file to write.
		return nil
	}
	return m.writeShellConfig()
}

func (m *Manager) saveState() error {
	path := m.statePath()
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	sort.Strings(m.state.Path)
	data, err := json.MarshalIndent(m.state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func (m *Manager) writeShellConfig() error {
	if m.ConfigFile == "" {
		return nil
	}
	existing, err := os.ReadFile(m.ConfigFile)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading %s: %w", m.ConfigFile, err)
	}

	updated := ReplaceManagedBlock(string(existing), RenderManagedBlock(m.state, m.Shell))
	if updated == string(existing) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(m.ConfigFile), 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(m.ConfigFile), err)
	}
	return os.WriteFile(m.ConfigFile, []byte(updated), 0o644)
}

// RestartNotice explains what the user has to do for the changes to take
// effect, or returns an empty string when nothing changed. Bento never claims
// to have updated the shell it is running inside, because it cannot.
func (m *Manager) RestartNotice() string {
	applied := 0
	for _, change := range m.changes {
		if change.Applied {
			applied++
		}
	}
	if applied == 0 {
		return ""
	}
	switch m.OS {
	case "windows":
		return "Environment changes were written to your user environment. Open a new terminal for them to take effect; already-running programs keep the old values."
	default:
		target := m.ConfigFile
		if target == "" {
			target = "your shell configuration"
		}
		return fmt.Sprintf("Environment changes were written to %s. Run `source %s` or open a new terminal for them to take effect.", target, target)
	}
}

// --- Pure helpers, shared by both platforms and tested directly -----------

// containsEntry reports whether a PATH entry is already present, comparing
// case-insensitively on Windows.
func containsEntry(entries []string, dir string, windows bool) bool {
	for _, entry := range entries {
		if paths.SameEntry(entry, dir, windows) {
			return true
		}
	}
	return false
}

// MergePath appends a directory to a PATH list unless it is already there. It
// reports whether the list changed.
func MergePath(entries []string, dir string, windows bool) ([]string, bool) {
	dir = paths.NormaliseFor(dir, windows)
	if dir == "" || containsEntry(entries, dir, windows) {
		return entries, false
	}
	return append(entries, dir), true
}

// RenderManagedBlock produces the block Bento writes into a shell
// configuration file. The output is deterministic so that re-running Bento
// with no new dependencies produces a byte-identical file.
func RenderManagedBlock(state State, shell string) string {
	names := make([]string, 0, len(state.Variables))
	for name := range state.Variables {
		names = append(names, name)
	}
	sort.Strings(names)

	entries := make([]string, len(state.Path))
	copy(entries, state.Path)
	sort.Strings(entries)

	var builder strings.Builder
	builder.WriteString(StartMarker + "\n")
	builder.WriteString("# Managed by Bento Box. Changes between these markers are regenerated.\n")

	if shell == "fish" {
		for _, name := range names {
			fmt.Fprintf(&builder, "set -gx %s %q\n", name, state.Variables[name])
		}
		for _, entry := range entries {
			fmt.Fprintf(&builder, "fish_add_path %q\n", entry)
		}
	} else {
		for _, name := range names {
			fmt.Fprintf(&builder, "export %s=%q\n", name, state.Variables[name])
		}
		for _, entry := range entries {
			// The guard keeps the entry from being prepended again every time
			// an interactive shell sources this file.
			fmt.Fprintf(&builder, "case \":$PATH:\" in *:%q:*) ;; *) export PATH=%q:\"$PATH\" ;; esac\n", entry, entry)
		}
	}

	builder.WriteString(EndMarker)
	return builder.String()
}

// ReplaceManagedBlock swaps Bento's block into a file, replacing an existing
// one if present and appending otherwise. Content outside the markers is
// preserved exactly, including a file that has never been touched by Bento.
func ReplaceManagedBlock(content, block string) string {
	start := strings.Index(content, StartMarker)
	end := strings.Index(content, EndMarker)

	if start >= 0 && end > start {
		tail := content[end+len(EndMarker):]
		return content[:start] + block + tail
	}

	if strings.TrimSpace(content) == "" {
		return block + "\n"
	}
	separator := "\n"
	if strings.HasSuffix(content, "\n") {
		separator = ""
	}
	return content + separator + "\n" + block + "\n"
}
