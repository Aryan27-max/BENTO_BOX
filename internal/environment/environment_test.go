package environment

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Aryan27-max/bento-box/internal/command"
	"github.com/Aryan27-max/bento-box/internal/dependency"
)

func TestMergePathNeverDuplicates(t *testing.T) {
	entries := []string{"/usr/bin", "/usr/local/bin"}

	entries, changed := MergePath(entries, "/opt/go/bin", false)
	if !changed || len(entries) != 3 {
		t.Fatalf("adding a new entry: entries=%v changed=%v", entries, changed)
	}

	entries, changed = MergePath(entries, "/opt/go/bin", false)
	if changed || len(entries) != 3 {
		t.Errorf("adding the same entry twice changed the list: %v", entries)
	}

	// Trailing separators and mixed separators describe the same directory.
	entries, changed = MergePath(entries, "/opt/go/bin/", false)
	if changed {
		t.Errorf("a trailing separator should not create a second entry: %v", entries)
	}
}

func TestMergePathIsCaseInsensitiveOnWindows(t *testing.T) {
	entries := []string{`C:\Program Files\Go\bin`}

	_, changed := MergePath(entries, `c:\program files\go\bin`, true)
	if changed {
		t.Error("Windows paths differing only in case are the same directory")
	}
	_, changed = MergePath(entries, `C:\Program Files\Go\bin`, false)
	if changed {
		t.Error("identical paths should never be added twice")
	}
}

func TestRenderManagedBlockIsDeterministic(t *testing.T) {
	state := State{
		Variables: map[string]string{"GOPATH": "/home/dev/go", "CARGO_HOME": "/home/dev/.cargo"},
		Path:      []string{"/home/dev/go/bin", "/home/dev/.cargo/bin"},
	}

	first := RenderManagedBlock(state, "bash")
	for i := 0; i < 5; i++ {
		if got := RenderManagedBlock(state, "bash"); got != first {
			t.Fatal("RenderManagedBlock produced different output for the same state")
		}
	}
	for _, want := range []string{StartMarker, EndMarker, `export CARGO_HOME="/home/dev/.cargo"`, "/home/dev/go/bin"} {
		if !strings.Contains(first, want) {
			t.Errorf("block is missing %q:\n%s", want, first)
		}
	}
}

func TestRenderManagedBlockUsesFishSyntax(t *testing.T) {
	state := State{Variables: map[string]string{"GOPATH": "/home/dev/go"}, Path: []string{"/home/dev/go/bin"}}

	block := RenderManagedBlock(state, "fish")
	if !strings.Contains(block, "set -gx GOPATH") {
		t.Errorf("fish block should use set -gx:\n%s", block)
	}
	if strings.Contains(block, "export ") {
		t.Errorf("fish does not understand export:\n%s", block)
	}
}

func TestReplaceManagedBlockPreservesUserContent(t *testing.T) {
	userFile := "# my shell config\nalias ll='ls -la'\nexport EDITOR=vim\n"
	block := RenderManagedBlock(State{Variables: map[string]string{"GOPATH": "/home/dev/go"}}, "bash")

	updated := ReplaceManagedBlock(userFile, block)
	for _, want := range []string{"alias ll='ls -la'", "export EDITOR=vim", "GOPATH"} {
		if !strings.Contains(updated, want) {
			t.Errorf("updated file is missing %q:\n%s", want, updated)
		}
	}

	// Applying a new block replaces the old one instead of stacking.
	newBlock := RenderManagedBlock(State{Variables: map[string]string{"GOPATH": "/home/dev/go", "CARGO_HOME": "/home/dev/.cargo"}}, "bash")
	twice := ReplaceManagedBlock(updated, newBlock)
	if count := strings.Count(twice, StartMarker); count != 1 {
		t.Errorf("file has %d managed blocks, want 1:\n%s", count, twice)
	}
	if !strings.Contains(twice, "CARGO_HOME") {
		t.Error("the replacement block was not written")
	}
	if !strings.Contains(twice, "alias ll='ls -la'") {
		t.Error("replacing the block destroyed the user's own configuration")
	}
}

func TestReplaceManagedBlockIsIdempotent(t *testing.T) {
	block := RenderManagedBlock(State{Path: []string{"/opt/go/bin"}}, "zsh")

	once := ReplaceManagedBlock("export EDITOR=vim\n", block)
	twice := ReplaceManagedBlock(once, block)
	if once != twice {
		t.Errorf("applying the same block twice changed the file:\n--- once ---\n%s\n--- twice ---\n%s", once, twice)
	}
}

// --- Manager behaviour against a temporary home ---------------------------

func newTestManager(t *testing.T, dryRun bool) *Manager {
	t.Helper()
	home := t.TempDir()
	configFile := filepath.Join(home, ".bashrc")
	return New("linux", home, configFile, "bash", dryRun)
}

func TestManagerWritesAndReloadsState(t *testing.T) {
	home := t.TempDir()
	configFile := filepath.Join(home, ".bashrc")

	// A name that is not already in the ambient environment, so the test
	// measures Bento's own state rather than the developer's shell.
	const name = "BENTO_FIXTURE_HOME"
	value := filepath.Join(home, "fixture")

	manager := New("linux", home, configFile, "bash", false)
	if change := manager.SetVariable(name, value); !change.Applied {
		t.Fatalf("SetVariable was not applied: %+v", change)
	}
	if err := manager.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	content, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("reading shell config: %v", err)
	}
	if !strings.Contains(string(content), name) {
		t.Errorf("shell config is missing %s:\n%s", name, content)
	}

	// A second Manager must see the first one's work and not repeat it.
	again := New("linux", home, configFile, "bash", false)
	change := again.SetVariable(name, value)
	if change.Applied {
		t.Error("re-setting an unchanged variable should not be applied again")
	}
	if change.Reason != "already set" {
		t.Errorf("Reason = %q, want 'already set'", change.Reason)
	}
}

func TestRepeatedRunsDoNotAccumulatePathEntries(t *testing.T) {
	home := t.TempDir()
	configFile := filepath.Join(home, ".zshrc")
	binDir := filepath.Join(home, "go", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}

	for run := 0; run < 3; run++ {
		manager := New("linux", home, configFile, "zsh", false)
		manager.AddPath(binDir)
		if err := manager.Flush(); err != nil {
			t.Fatalf("run %d Flush: %v", run, err)
		}
	}

	content, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("reading shell config: %v", err)
	}
	// One PATH assignment, no matter how many times Bento ran. Counting the
	// assignment rather than the directory keeps this honest on any host,
	// since the rendered path is quoted and escaped.
	if count := strings.Count(string(content), "export PATH="); count != 1 {
		t.Errorf("shell config has %d PATH assignments after three runs, want 1:\n%s", count, content)
	}
	if count := strings.Count(string(content), StartMarker); count != 1 {
		t.Errorf("shell config has %d managed blocks after three runs, want 1", count)
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	home := t.TempDir()
	configFile := filepath.Join(home, ".bashrc")

	manager := New("linux", home, configFile, "bash", true)
	change := manager.SetVariable("GOPATH", "/somewhere")
	if change.Applied {
		t.Error("dry run must not apply changes")
	}
	manager.AddPath("/somewhere/bin")
	if err := manager.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	if _, err := os.Stat(configFile); !os.IsNotExist(err) {
		t.Error("dry run created the shell configuration file")
	}
	if _, err := os.Stat(filepath.Join(home, ".bento", "environment.json")); !os.IsNotExist(err) {
		t.Error("dry run wrote a state file")
	}
}

func TestRestartNoticeOnlyAppearsWhenSomethingChanged(t *testing.T) {
	manager := newTestManager(t, false)
	if notice := manager.RestartNotice(); notice != "" {
		t.Errorf("no changes should produce no notice, got %q", notice)
	}

	manager.SetVariable("BENTO_FIXTURE_NOTICE", "1")
	notice := manager.RestartNotice()
	if notice == "" {
		t.Fatal("expected a restart notice after a change")
	}
	// Bento must never claim the running shell was updated, because it wasn't.
	if !strings.Contains(notice, "new terminal") {
		t.Errorf("notice should tell the user to open a new terminal: %q", notice)
	}
}

// --- Applier --------------------------------------------------------------

func TestApplierResolvesValuesFromTheToolItself(t *testing.T) {
	home := t.TempDir()
	goPath := filepath.Join(home, "go")
	binDir := filepath.Join(goPath, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}

	runner := command.NewMock()
	runner.AddPath("go", "/mock/go")
	runner.RespondOK("/mock/go env GOROOT", "/usr/local/go\n")
	runner.RespondOK("/mock/go env GOPATH", goPath+"\n")

	manager := New("linux", home, filepath.Join(home, ".bashrc"), "bash", false)
	applier := &Applier{Manager: manager, Runner: runner, OS: "linux", Home: home}

	spec := dependency.Spec{
		Name: "go",
		Environment: []dependency.EnvVar{
			{Name: "GOROOT", FromCommand: []string{"go", "env", "GOROOT"}},
			{Name: "GOPATH", FromCommand: []string{"go", "env", "GOPATH"}},
		},
		PathEntries: []dependency.PathEntry{
			{Path: "bin", FromCommand: []string{"go", "env", "GOPATH"}},
		},
	}

	changes := applier.Apply(context.Background(), spec)
	if len(changes) != 3 {
		t.Fatalf("got %d changes, want 3: %+v", len(changes), changes)
	}
	for _, change := range changes {
		if !change.Applied {
			t.Errorf("change %s was not applied: %s", change, change.Reason)
		}
	}
	if got := changes[0].Value; got != "/usr/local/go" {
		t.Errorf("GOROOT = %q, want the value go itself reported", got)
	}
	if got := changes[2].Value; got != filepath.Clean(binDir) {
		t.Errorf("PATH entry = %q, want %q", got, binDir)
	}
}

func TestApplierSkipsVariablesWhoseDirectoryIsMissing(t *testing.T) {
	home := t.TempDir()
	manager := New("linux", home, filepath.Join(home, ".bashrc"), "bash", false)
	applier := &Applier{Manager: manager, Runner: command.NewMock(), OS: "linux", Home: home}

	spec := dependency.Spec{
		Name: "android-studio",
		Environment: []dependency.EnvVar{
			{Name: "ANDROID_HOME", Value: "${HOME}/Android/Sdk", OS: "linux", RequirePath: true},
		},
	}

	changes := applier.Apply(context.Background(), spec)
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1", len(changes))
	}
	if changes[0].Applied {
		t.Error("ANDROID_HOME must not be set when the SDK directory does not exist")
	}
	if !strings.Contains(changes[0].Reason, "does not exist") {
		t.Errorf("Reason = %q, want an explanation", changes[0].Reason)
	}
}

func TestApplierSetsVariableOnceTheDirectoryExists(t *testing.T) {
	home := t.TempDir()
	sdk := filepath.Join(home, "Android", "Sdk")
	if err := os.MkdirAll(sdk, 0o755); err != nil {
		t.Fatal(err)
	}

	manager := New("linux", home, filepath.Join(home, ".bashrc"), "bash", false)
	applier := &Applier{Manager: manager, Runner: command.NewMock(), OS: "linux", Home: home}

	spec := dependency.Spec{
		Name:        "android-studio",
		Environment: []dependency.EnvVar{{Name: "ANDROID_HOME", Value: "${HOME}/Android/Sdk", RequirePath: true}},
	}

	changes := applier.Apply(context.Background(), spec)
	if !changes[0].Applied {
		t.Fatalf("ANDROID_HOME should be set once the SDK exists: %s", changes[0].Reason)
	}
	if changes[0].Value != sdk {
		t.Errorf("ANDROID_HOME = %q, want %q", changes[0].Value, sdk)
	}
}

func TestApplierSkipsPathEntriesThatDoNotExist(t *testing.T) {
	home := t.TempDir()
	manager := New("linux", home, filepath.Join(home, ".bashrc"), "bash", false)
	applier := &Applier{Manager: manager, Runner: command.NewMock(), OS: "linux", Home: home}

	spec := dependency.Spec{
		Name:        "android-studio",
		PathEntries: []dependency.PathEntry{{Path: "${HOME}/Android/Sdk/platform-tools"}},
	}

	changes := applier.Apply(context.Background(), spec)
	if changes[0].Applied {
		t.Error("a directory that does not exist must not be put on PATH")
	}
}

func TestApplierIgnoresOtherPlatforms(t *testing.T) {
	home := t.TempDir()
	manager := New("linux", home, filepath.Join(home, ".bashrc"), "bash", false)
	applier := &Applier{Manager: manager, Runner: command.NewMock(), OS: "linux", Home: home}

	spec := dependency.Spec{
		Name: "android-studio",
		Environment: []dependency.EnvVar{
			{Name: "ANDROID_HOME", Value: `${LOCALAPPDATA}/Android/Sdk`, OS: "windows"},
			{Name: "ANDROID_HOME", Value: "${HOME}/Android/Sdk", OS: "darwin"},
		},
	}

	if changes := applier.Apply(context.Background(), spec); len(changes) != 0 {
		t.Errorf("Windows and macOS variables must not be applied on Linux: %+v", changes)
	}
}

func TestApplierReportsUnresolvableValues(t *testing.T) {
	home := t.TempDir()
	runner := command.NewMock() // no commands registered: everything fails
	manager := New("linux", home, filepath.Join(home, ".bashrc"), "bash", false)
	applier := &Applier{Manager: manager, Runner: runner, OS: "linux", Home: home}

	spec := dependency.Spec{
		Name:        "go",
		Environment: []dependency.EnvVar{{Name: "GOROOT", FromCommand: []string{"go", "env", "GOROOT"}}},
	}

	changes := applier.Apply(context.Background(), spec)
	if changes[0].Applied {
		t.Error("a variable whose command failed must not be set")
	}
	if changes[0].Value != "" {
		t.Errorf("Value = %q, want empty rather than a guess", changes[0].Value)
	}
}
