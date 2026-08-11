package verifier

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Aryan27-max/bento-box/internal/command"
	"github.com/Aryan27-max/bento-box/internal/dependency"
)

func newVerifier(runner *command.Mock, osName string, present ...string) *Verifier {
	existing := map[string]bool{}
	for _, path := range present {
		existing[path] = true
	}
	return &Verifier{
		Runner: runner,
		OS:     osName,
		Home:   "/home/dev",
		Getenv: func(string) string { return "" },
		Exists: func(path string) bool { return existing[path] },
	}
}

func cliSpec(name, executable string, args ...string) dependency.Spec {
	return dependency.Spec{
		Name:     name,
		Category: dependency.CategoryCLITool,
		Verify:   dependency.Verification{Command: executable, Args: args},
	}
}

func TestVerifyReadsRealVersionOutput(t *testing.T) {
	runner := command.NewMock()
	runner.AddPath("go", "/usr/local/go/bin/go")
	runner.RespondOK("/usr/local/go/bin/go version", "go version go1.26.4 linux/amd64")

	result := newVerifier(runner, "linux").Verify(context.Background(), cliSpec("go", "go", "version"))

	if !result.Present {
		t.Fatalf("go should be present: %s", result.Problem)
	}
	if result.VersionString() != "1.26.4" {
		t.Errorf("version = %q, want 1.26.4", result.VersionString())
	}
	if result.Location != "/usr/local/go/bin/go" {
		t.Errorf("Location = %q", result.Location)
	}
}

func TestVerifyReportsAbsenceWithoutGuessing(t *testing.T) {
	result := newVerifier(command.NewMock(), "linux").Verify(context.Background(), cliSpec("go", "go", "version"))

	if result.Present {
		t.Error("a tool that is not on PATH must not be reported as present")
	}
	if result.VersionString() != "" {
		t.Errorf("version = %q, want empty for a missing tool", result.VersionString())
	}
	if !strings.Contains(result.Problem, "not found") {
		t.Errorf("Problem = %q, want an explanation", result.Problem)
	}
}

// TestVerifyReadsVersionFromStderr covers tools such as java and ssh that print
// their version to stderr.
func TestVerifyReadsVersionFromStderr(t *testing.T) {
	runner := command.NewMock()
	runner.AddPath("java", "/usr/bin/java")
	runner.Respond("/usr/bin/java -version", command.Result{
		Stderr: "openjdk version \"21.0.6\" 2025-01-21\nOpenJDK Runtime Environment",
	})

	spec := dependency.Spec{
		Name:   "java",
		Verify: dependency.Verification{Command: "java", Args: []string{"-version"}, Pattern: `version "([0-9][0-9._]*)"`},
	}
	result := newVerifier(runner, "linux").Verify(context.Background(), spec)

	if !result.Present || result.VersionString() != "21.0.6" {
		t.Errorf("java verification = %+v, want version 21.0.6", result)
	}
}

func TestVerifyPresentButUnreadableVersionIsStillPresent(t *testing.T) {
	runner := command.NewMock()
	runner.AddPath("weird", "/usr/bin/weird")
	runner.RespondOK("/usr/bin/weird --version", "the finest tool ever made")

	result := newVerifier(runner, "linux").Verify(context.Background(), cliSpec("weird", "weird", "--version"))

	if !result.Present {
		t.Error("the tool ran, so it is present")
	}
	if result.VersionKnown {
		t.Error("no version was observed, so none should be claimed")
	}
	if !strings.Contains(result.Problem, "could not read a version") {
		t.Errorf("Problem = %q", result.Problem)
	}
}

func TestVerifySkipVersionForToolsWithoutOne(t *testing.T) {
	runner := command.NewMock()
	runner.AddPath("xcode-select", "/usr/bin/xcode-select")

	spec := dependency.Spec{
		Name:   "xcode-command-line-tools",
		Verify: dependency.Verification{Command: "xcode-select", Args: []string{"-p"}, SkipVersion: true},
	}
	result := newVerifier(runner, "darwin").Verify(context.Background(), spec)

	if !result.Present {
		t.Error("expected the tool to be present")
	}
	if result.VersionKnown {
		t.Error("a skip_version dependency must not report a version")
	}
	if len(runner.Calls) != 0 {
		t.Error("skip_version should not need to run the tool")
	}
}

// TestVerifyGUIApplicationByPath is the case GUI applications exist for: VS
// Code on Windows is routinely installed without ever landing on PATH.
func TestVerifyGUIApplicationByPath(t *testing.T) {
	const installed = `C:\Users\dev\AppData\Local\Programs\Microsoft VS Code\Code.exe`

	runner := command.NewMock() // nothing on PATH at all
	verifier := newVerifier(runner, "windows", installed)
	verifier.Getenv = func(name string) string {
		if name == "LOCALAPPDATA" {
			return `C:\Users\dev\AppData\Local`
		}
		return ""
	}

	spec := dependency.Spec{
		Name:     "vscode",
		Category: dependency.CategoryGUIApplication,
		Verify: dependency.Verification{
			Command: "code", Args: []string{"--version"},
			Paths: map[string][]string{
				"windows": {"${LOCALAPPDATA}/Programs/Microsoft VS Code/Code.exe"},
			},
		},
	}

	result := verifier.Verify(context.Background(), spec)
	if !result.Present {
		t.Fatalf("VS Code should be found on disk even when it is not on PATH: %s", result.Problem)
	}
	if result.Location != installed {
		t.Errorf("Location = %q, want %q", result.Location, installed)
	}
}

func TestVerifyGUIApplicationMissing(t *testing.T) {
	spec := dependency.Spec{
		Name:     "postman",
		Category: dependency.CategoryGUIApplication,
		Verify: dependency.Verification{
			SkipVersion: true,
			Paths:       map[string][]string{"darwin": {"/Applications/Postman.app"}},
		},
	}

	result := newVerifier(command.NewMock(), "darwin").Verify(context.Background(), spec)
	if result.Present {
		t.Error("Postman is not installed and must not be reported as present")
	}
}

func TestVerifyComponents(t *testing.T) {
	runner := command.NewMock()
	runner.AddPath("rustc", "/home/dev/.cargo/bin/rustc")
	runner.AddPath("cargo", "/home/dev/.cargo/bin/cargo")
	runner.AddPath("rustfmt", "/home/dev/.cargo/bin/rustfmt")
	runner.RespondOK("/home/dev/.cargo/bin/rustc --version", "rustc 1.85.0 (4d91de4e4 2025-02-17)")
	runner.RespondOK("/home/dev/.cargo/bin/cargo --version", "cargo 1.85.0 (d73d2caf9 2024-12-31)")
	runner.RespondOK("/home/dev/.cargo/bin/rustfmt --version", "rustfmt 1.8.0-stable")

	spec := dependency.Spec{
		Name: "rust",
		Verify: dependency.Verification{
			Command: "rustc", Args: []string{"--version"},
			Components: []dependency.Component{
				{Name: "cargo", Command: "cargo", Args: []string{"--version"}},
				{Name: "rustfmt", Command: "rustfmt", Args: []string{"--version"}},
				{Name: "clippy", Command: "cargo", Args: []string{"clippy", "--version"}},
			},
		},
	}

	result := newVerifier(runner, "linux").Verify(context.Background(), spec)
	if !result.Present || result.VersionString() != "1.85.0" {
		t.Fatalf("rust verification = %+v", result)
	}

	byName := map[string]dependency.ComponentStatus{}
	for _, component := range result.Components {
		byName[component.Name] = component
	}
	if !byName["cargo"].Present || byName["cargo"].Version != "1.85.0" {
		t.Errorf("cargo = %+v, want present at 1.85.0", byName["cargo"])
	}
	if !byName["rustfmt"].Present {
		t.Error("rustfmt should be present")
	}
	// clippy was never installed; the mock has no response for it.
	if byName["clippy"].Present {
		t.Error("clippy is missing and must be reported as missing")
	}
	if missing := result.MissingComponents(); len(missing) != 1 || missing[0] != "clippy" {
		t.Errorf("MissingComponents = %v, want [clippy]", missing)
	}
}

// TestVerifyFindsToolsInstalledDuringThisRun covers the trap that a tool
// installed minutes ago is not on the PATH this process inherited at start-up.
func TestVerifyFindsToolsInstalledDuringThisRun(t *testing.T) {
	// Built with filepath.Join so the case reads the same on any host.
	binDir := filepath.Join("/home", "dev", ".bento", "opt", "node", "bin")
	nodeBinary := filepath.Join(binDir, "node")

	runner := command.NewMock() // node is not on the inherited PATH
	runner.RespondOK(nodeBinary+" --version", "v22.14.0")

	verifier := newVerifier(runner, "linux", nodeBinary)
	verifier.ExtraPath = []string{binDir}

	result := verifier.Verify(context.Background(), cliSpec("node", "node", "--version"))
	if !result.Present {
		t.Fatalf("node was installed during this run and should be found: %s", result.Problem)
	}
	if result.VersionString() != "22.14.0" {
		t.Errorf("version = %q, want 22.14.0", result.VersionString())
	}
}

func TestExecutableNamesOnWindows(t *testing.T) {
	// npm is npm.cmd on Windows; looking only for "npm" would miss it.
	got := executableNames("npm", "windows")
	want := []string{"npm.exe", "npm.cmd", "npm.bat", "npm"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("executableNames = %v, want %v", got, want)
	}
	if got := executableNames("npm", "linux"); len(got) != 1 || got[0] != "npm" {
		t.Errorf("executableNames on linux = %v, want [npm]", got)
	}
}

func TestVerifyWithoutAnyStrategyIsReportedNotAssumed(t *testing.T) {
	spec := dependency.Spec{Name: "mystery", Category: dependency.CategoryCLITool}

	result := newVerifier(command.NewMock(), "linux").Verify(context.Background(), spec)
	if result.Present {
		t.Error("a dependency with no verification strategy must not be assumed present")
	}
	if !strings.Contains(result.Problem, "no way to be verified") {
		t.Errorf("Problem = %q", result.Problem)
	}
}

func TestPlatformVerificationOverride(t *testing.T) {
	runner := command.NewMock()
	runner.AddPath("python", `C:\Python313\python.exe`)
	runner.RespondOK(`C:\Python313\python.exe --version`, "Python 3.13.1")

	spec := dependency.Spec{
		Name:   "python",
		Verify: dependency.Verification{Command: "python3", Args: []string{"--version"}},
		Platforms: map[string]dependency.Platform{
			"windows": {
				Install: []dependency.Step{{Method: dependency.MethodPackageManager, Manager: "winget", Packages: []string{"Python.Python.3.13"}}},
				Verify:  &dependency.Verification{Command: "python", Args: []string{"--version"}},
			},
		},
	}

	result := newVerifier(runner, "windows").Verify(context.Background(), spec)
	if !result.Present || result.VersionString() != "3.13.1" {
		t.Errorf("windows override not used: %+v", result)
	}
}
