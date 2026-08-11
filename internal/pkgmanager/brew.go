package pkgmanager

import (
	"context"
	"fmt"
	"strings"
)

// Brew adapts Homebrew. Homebrew refuses to run under sudo by design, so this
// adapter never elevates regardless of what a dependency asks for.
type Brew struct{ base }

// NewBrew returns the Homebrew adapter.
func NewBrew(options Options) *Brew {
	return &Brew{base{
		runner: options.Runner, elevated: options.Elevated, dryRun: options.DryRun,
		executable: "brew",
	}}
}

// Name implements Manager.
func (b *Brew) Name() string { return "brew" }

// IsAvailable implements Manager.
func (b *Brew) IsAvailable() bool { return b.available() }

// Refresh implements Manager.
func (b *Brew) Refresh(ctx context.Context) error {
	if b.refreshed {
		return nil
	}
	b.refreshed = true
	_, err := b.run(ctx, "brew", []string{"update"}, false)
	return err
}

// Install implements Manager.
func (b *Brew) Install(ctx context.Context, request Request) (Result, error) {
	return b.run(ctx, "brew", b.args("install", request), false)
}

// Upgrade implements Manager.
func (b *Brew) Upgrade(ctx context.Context, request Request) (Result, error) {
	return b.run(ctx, "brew", b.args("upgrade", request), false)
}

func (b *Brew) args(verb string, request Request) []string {
	args := []string{verb}
	if request.Cask {
		args = append(args, "--cask")
	}
	args = append(args, request.ExtraArgs...)
	return append(args, request.Packages...)
}

// IsInstalled implements Manager. `brew list --versions` exits non-zero when
// the package is absent, which makes it a clean presence check.
func (b *Brew) IsInstalled(ctx context.Context, pkg string) bool {
	if b.query(ctx, "brew", "list", "--versions", pkg).Success() {
		return true
	}
	return b.query(ctx, "brew", "list", "--cask", "--versions", pkg).Success()
}

// Version implements Manager. The output is "name version [version...]".
func (b *Brew) Version(ctx context.Context, pkg string) string {
	for _, args := range [][]string{
		{"list", "--versions", pkg},
		{"list", "--cask", "--versions", pkg},
	} {
		result := b.query(ctx, "brew", args...)
		if !result.Success() {
			continue
		}
		fields := strings.Fields(result.Stdout)
		if len(fields) >= 2 {
			return fields[1]
		}
	}
	return ""
}

// InstallLocal implements Manager.
func (b *Brew) InstallLocal(_ context.Context, path string, _ bool) (Result, error) {
	err := fmt.Errorf("Homebrew cannot install the downloaded package file %s", path)
	return Result{Message: err.Error()}, err
}
