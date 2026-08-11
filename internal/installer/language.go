package installer

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Aryan27-max/bento-box/internal/command"
	"github.com/Aryan27-max/bento-box/internal/dependency"
	"github.com/Aryan27-max/bento-box/internal/paths"
)

// installLanguagePackage installs through a language ecosystem's own package
// manager. Each of these has one well-known way to go wrong on a fresh
// machine, and handling those explicitly is the difference between Bento
// working on a real Ubuntu box and only working in a demo.
func (i *Installer) installLanguagePackage(ctx context.Context, step dependency.Step, outcome *dependency.Outcome) error {
	switch step.Via {
	case "pip":
		return i.installWithPip(ctx, step, outcome)
	case "npm":
		return i.installWithNpm(ctx, step, outcome)
	case "cargo":
		return i.installWithCargo(ctx, step)
	case "uv":
		return i.installWithUv(ctx, step)
	default:
		return fmt.Errorf("unknown language package manager %q", step.Via)
	}
}

// installWithPip installs into the user's site-packages rather than the system
// one, so nothing needs root and the distribution's own Python stays intact.
//
// Modern Debian, Ubuntu and Fedora mark their system Python as
// externally-managed (PEP 668) and refuse even a --user install. Bento retries
// once with --break-system-packages, which stays inside the user's own
// site-packages directory and cannot damage system packages, and records a
// warning so the user knows it happened.
func (i *Installer) installWithPip(ctx context.Context, step dependency.Step, outcome *dependency.Outcome) error {
	python, err := i.pythonCommand()
	if err != nil {
		return err
	}

	base := append([]string{"-m", "pip", "install", "--user", "--upgrade"}, step.Args...)
	args := append(base, step.Packages...)

	result, err := i.runTool(ctx, python, args)
	if err == nil && result.Success() {
		return nil
	}

	combined := result.Stdout + result.Stderr
	if !strings.Contains(combined, "externally-managed-environment") {
		if err != nil {
			return err
		}
		return fmt.Errorf("pip exited with code %d: %s", result.ExitCode, lastLine(combined))
	}

	i.Progress.Step("retrying with --break-system-packages for this distribution's managed Python")
	i.log.Printf("pip refused a --user install because this Python is externally managed; retrying with --break-system-packages")
	outcome.AddWarning("this distribution marks its Python as externally managed; packages were installed into your user site-packages with --break-system-packages")

	retryArgs := append(append([]string{}, base...), "--break-system-packages")
	retryArgs = append(retryArgs, step.Packages...)

	result, err = i.runTool(ctx, python, retryArgs)
	if err != nil {
		return err
	}
	if !result.Success() {
		return fmt.Errorf("pip exited with code %d: %s", result.ExitCode, lastLine(result.Stdout+result.Stderr))
	}
	return nil
}

func (i *Installer) pythonCommand() (string, error) {
	candidates := []string{"python3", "python"}
	if i.OS == "windows" {
		candidates = []string{"python", "python3"}
	}
	for _, candidate := range candidates {
		if resolved, err := i.Runner.LookPath(candidate); err == nil {
			return resolved, nil
		}
		if resolved, ok := i.findOnBentoPath(candidate); ok {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("no Python interpreter is available")
}

// installWithNpm installs a global npm package.
//
// On Linux the system Node.js puts its global prefix under /usr, which is not
// writable without root. Rather than running npm under sudo — which leaves
// root-owned files in the user's cache and is widely discouraged — Bento gives
// npm a prefix inside its own directory and puts that on PATH.
func (i *Installer) installWithNpm(ctx context.Context, step dependency.Step, outcome *dependency.Outcome) error {
	npm, err := i.toolPath("npm")
	if err != nil {
		return err
	}

	args := append([]string{"install", "--global"}, step.Args...)
	args = append(args, step.Packages...)

	var env []string
	if i.OS == "linux" {
		i.npmPrefix = filepath.Join(paths.Home(i.Home), "npm")
		env = append(env, "npm_config_prefix="+i.npmPrefix)
	}

	result, err := i.runToolWithEnv(ctx, npm, args, env)
	if err != nil {
		return err
	}
	if !result.Success() {
		return fmt.Errorf("npm exited with code %d: %s", result.ExitCode, lastLine(result.Stdout+result.Stderr))
	}

	if i.npmPrefix != "" {
		change := i.Environment.AddPath(i.npmBinDir())
		if change.Applied {
			outcome.Environment = append(outcome.Environment, change.String())
		}
		i.refreshVerifierPath()
	}
	return nil
}

// npmBinDir returns the directory global npm packages land in under Bento's
// prefix. Windows puts executables directly in the prefix; Unix uses bin.
func (i *Installer) npmBinDir() string {
	if i.OS == "windows" {
		return i.npmPrefix
	}
	return filepath.Join(i.npmPrefix, "bin")
}

// installWithCargo builds and installs a Rust binary. --locked is passed by the
// catalog where it matters so the build uses the versions the project tested.
func (i *Installer) installWithCargo(ctx context.Context, step dependency.Step) error {
	cargo, err := i.toolPath("cargo")
	if err != nil {
		return err
	}

	args := append([]string{"install"}, step.Args...)
	args = append(args, step.Packages...)

	result, err := i.runTool(ctx, cargo, args)
	if err != nil {
		return err
	}
	if !result.Success() {
		return fmt.Errorf("cargo exited with code %d: %s", result.ExitCode, lastLine(result.Stdout+result.Stderr))
	}
	return nil
}

func (i *Installer) installWithUv(ctx context.Context, step dependency.Step) error {
	uv, err := i.toolPath("uv")
	if err != nil {
		return err
	}

	args := append([]string{"pip", "install"}, step.Args...)
	args = append(args, step.Packages...)

	result, err := i.runTool(ctx, uv, args)
	if err != nil {
		return err
	}
	if !result.Success() {
		return fmt.Errorf("uv exited with code %d: %s", result.ExitCode, lastLine(result.Stdout+result.Stderr))
	}
	return nil
}

// toolPath resolves a tool on PATH or in the directories Bento added during
// this run, which is how npm works immediately after Node was installed from
// an archive minutes earlier.
func (i *Installer) toolPath(name string) (string, error) {
	if resolved, err := i.Runner.LookPath(name); err == nil {
		return resolved, nil
	}
	if resolved, ok := i.findOnBentoPath(name); ok {
		return resolved, nil
	}
	return "", fmt.Errorf("%s is not available", name)
}

func (i *Installer) runTool(ctx context.Context, name string, args []string) (command.Result, error) {
	return i.runToolWithEnv(ctx, name, args, nil)
}

func (i *Installer) runToolWithEnv(ctx context.Context, name string, args, env []string) (command.Result, error) {
	return i.Runner.Run(ctx, command.Spec{
		Name: name, Args: args, Env: append(i.commandEnv(), env...), AllowFailure: true,
	})
}
