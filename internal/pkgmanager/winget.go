package pkgmanager

import (
	"context"
	"fmt"
	"strings"
)

// Winget adapts the Windows Package Manager.
type Winget struct{ base }

// NewWinget returns the winget adapter.
func NewWinget(options Options) *Winget {
	return &Winget{base{
		runner:     options.Runner,
		elevated:   options.Elevated,
		dryRun:     options.DryRun,
		executable: "winget",
	}}
}

// Name implements Manager.
func (w *Winget) Name() string { return "winget" }

// IsAvailable implements Manager.
func (w *Winget) IsAvailable() bool { return w.available() }

// Refresh implements Manager. winget resolves package metadata from its
// sources on every call, so there is no separate index to update.
func (w *Winget) Refresh(context.Context) error { return nil }

// wingetFlags make installs non-interactive. Without the agreement flags
// winget stops and waits for a keypress, which would hang an unattended run.
var wingetFlags = []string{
	"--exact",
	"--silent",
	"--accept-package-agreements",
	"--accept-source-agreements",
	"--disable-interactivity",
}

// Install implements Manager.
func (w *Winget) Install(ctx context.Context, request Request) (Result, error) {
	return w.each(ctx, "install", request)
}

// Upgrade implements Manager.
func (w *Winget) Upgrade(ctx context.Context, request Request) (Result, error) {
	return w.each(ctx, "upgrade", request)
}

// each runs one winget command per package. winget accepts several ids in one
// invocation but reports a single aggregate exit code, which would hide which
// package actually failed.
func (w *Winget) each(ctx context.Context, verb string, request Request) (Result, error) {
	var combined Result
	for _, pkg := range request.Packages {
		args := append([]string{verb, "--id", pkg}, wingetFlags...)
		args = append(args, request.ExtraArgs...)

		result, err := w.run(ctx, "winget", args, false)
		combined.Command = joinCommands(combined.Command, result.Command)
		combined.Output = joinOutput(combined.Output, result.Output)
		if err != nil {
			combined.Message = result.Message
			return combined, err
		}
	}
	combined.Success = true
	return combined, nil
}

// IsInstalled implements Manager.
func (w *Winget) IsInstalled(ctx context.Context, pkg string) bool {
	result := w.query(ctx, "winget", "list", "--id", pkg, "--exact", "--disable-interactivity")
	if !result.Success() {
		return false
	}
	// winget can exit zero while reporting that it found nothing.
	return !strings.Contains(result.Stdout, "No installed package found")
}

// Version implements Manager. winget's list output is a fixed-width table, so
// the version is the last column of the row naming the package.
func (w *Winget) Version(ctx context.Context, pkg string) string {
	result := w.query(ctx, "winget", "list", "--id", pkg, "--exact", "--disable-interactivity")
	if !result.Success() {
		return ""
	}
	for _, line := range strings.Split(result.Stdout, "\n") {
		if !strings.Contains(line, pkg) {
			continue
		}
		fields := strings.Fields(line)
		for i := len(fields) - 1; i >= 0; i-- {
			if looksLikeVersion(fields[i]) {
				return fields[i]
			}
		}
	}
	return ""
}

// InstallLocal implements Manager.
func (w *Winget) InstallLocal(_ context.Context, path string, _ bool) (Result, error) {
	err := fmt.Errorf("winget cannot install a downloaded package file (%s)", path)
	return Result{Message: err.Error()}, err
}

func looksLikeVersion(field string) bool {
	if !strings.ContainsAny(field, "0123456789") {
		return false
	}
	for _, char := range field {
		if !strings.ContainsRune("0123456789.-_+abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ", char) {
			return false
		}
	}
	return strings.Contains(field, ".")
}

func joinCommands(existing, next string) string {
	if existing == "" {
		return next
	}
	return existing + "; " + next
}

func joinOutput(existing, next string) string {
	switch {
	case existing == "":
		return next
	case next == "":
		return existing
	default:
		return existing + "\n" + next
	}
}
