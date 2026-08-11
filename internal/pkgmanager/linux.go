package pkgmanager

import (
	"context"
	"fmt"
	"strings"
)

// --- apt (Debian, Ubuntu and derivatives) --------------------------------

// Apt adapts Debian's package manager. apt-get is used rather than apt
// because apt's output format is explicitly not intended for scripts.
type Apt struct{ base }

// NewApt returns the apt adapter.
func NewApt(options Options) *Apt {
	return &Apt{base{
		runner: options.Runner, elevated: options.Elevated, dryRun: options.DryRun,
		executable: "apt-get",
	}}
}

// Name implements Manager.
func (a *Apt) Name() string { return "apt" }

// IsAvailable implements Manager.
func (a *Apt) IsAvailable() bool { return a.available() }

// Refresh implements Manager. A fresh machine frequently has no package index
// at all, so this runs once before the first apt install of a session.
func (a *Apt) Refresh(ctx context.Context) error {
	if a.refreshed {
		return nil
	}
	a.refreshed = true
	_, err := a.run(ctx, "apt-get", []string{"update"}, true)
	return err
}

// aptEnv keeps apt from opening interactive configuration dialogs.
var aptEnv = []string{"DEBIAN_FRONTEND=noninteractive"}

// Install implements Manager.
func (a *Apt) Install(ctx context.Context, request Request) (Result, error) {
	args := append([]string{"install", "-y", "--no-install-recommends"}, request.ExtraArgs...)
	return a.runWithEnv(ctx, "apt-get", append(args, request.Packages...), request.Elevate)
}

// Upgrade implements Manager.
func (a *Apt) Upgrade(ctx context.Context, request Request) (Result, error) {
	args := append([]string{"install", "-y", "--only-upgrade"}, request.ExtraArgs...)
	return a.runWithEnv(ctx, "apt-get", append(args, request.Packages...), request.Elevate)
}

func (a *Apt) runWithEnv(ctx context.Context, name string, args []string, elevate bool) (Result, error) {
	if elevate && !a.elevated {
		// The environment has to survive the sudo boundary, so it is passed
		// as an explicit assignment rather than relying on inheritance.
		args = append([]string{"env", "DEBIAN_FRONTEND=noninteractive", name}, args...)
		return a.run(ctx, "sudo", append([]string{"-n"}, args...), false)
	}
	return a.runEnv(ctx, name, args, false, aptEnv)
}

// IsInstalled implements Manager.
func (a *Apt) IsInstalled(ctx context.Context, pkg string) bool {
	result := a.query(ctx, "dpkg-query", "-W", "-f=${Status}", pkg)
	return result.Success() && strings.Contains(result.Stdout, "install ok installed")
}

// Version implements Manager.
func (a *Apt) Version(ctx context.Context, pkg string) string {
	result := a.query(ctx, "dpkg-query", "-W", "-f=${Version}", pkg)
	if !result.Success() {
		return ""
	}
	return strings.TrimSpace(result.Stdout)
}

// InstallLocal implements Manager. Passing a path to apt-get lets it resolve
// the downloaded package's dependencies from the archive, which `dpkg -i`
// cannot do.
func (a *Apt) InstallLocal(ctx context.Context, path string, elevate bool) (Result, error) {
	return a.runWithEnv(ctx, "apt-get", []string{"install", "-y", localPath(path)}, elevate)
}

// --- dnf (Fedora, RHEL and derivatives) ----------------------------------

// Dnf adapts Fedora's package manager.
type Dnf struct{ base }

// NewDnf returns the dnf adapter.
func NewDnf(options Options) *Dnf {
	return &Dnf{base{
		runner: options.Runner, elevated: options.Elevated, dryRun: options.DryRun,
		executable: "dnf",
	}}
}

// Name implements Manager.
func (d *Dnf) Name() string { return "dnf" }

// IsAvailable implements Manager.
func (d *Dnf) IsAvailable() bool { return d.available() }

// Refresh implements Manager. dnf refreshes its metadata automatically when
// it is stale, so nothing is needed here.
func (d *Dnf) Refresh(context.Context) error { return nil }

// Install implements Manager.
func (d *Dnf) Install(ctx context.Context, request Request) (Result, error) {
	args := append([]string{"install", "-y"}, request.ExtraArgs...)
	return d.run(ctx, "dnf", append(args, request.Packages...), request.Elevate)
}

// Upgrade implements Manager.
func (d *Dnf) Upgrade(ctx context.Context, request Request) (Result, error) {
	args := append([]string{"upgrade", "-y"}, request.ExtraArgs...)
	return d.run(ctx, "dnf", append(args, request.Packages...), request.Elevate)
}

// IsInstalled implements Manager.
func (d *Dnf) IsInstalled(ctx context.Context, pkg string) bool {
	return d.query(ctx, "rpm", "-q", pkg).Success()
}

// Version implements Manager.
func (d *Dnf) Version(ctx context.Context, pkg string) string {
	result := d.query(ctx, "rpm", "-q", "--queryformat", "%{VERSION}", pkg)
	if !result.Success() {
		return ""
	}
	return strings.TrimSpace(result.Stdout)
}

// InstallLocal implements Manager.
func (d *Dnf) InstallLocal(ctx context.Context, path string, elevate bool) (Result, error) {
	return d.run(ctx, "dnf", []string{"install", "-y", localPath(path)}, elevate)
}

// --- zypper (openSUSE) ----------------------------------------------------

// Zypper adapts openSUSE's package manager.
type Zypper struct{ base }

// NewZypper returns the zypper adapter.
func NewZypper(options Options) *Zypper {
	return &Zypper{base{
		runner: options.Runner, elevated: options.Elevated, dryRun: options.DryRun,
		executable: "zypper",
	}}
}

// Name implements Manager.
func (z *Zypper) Name() string { return "zypper" }

// IsAvailable implements Manager.
func (z *Zypper) IsAvailable() bool { return z.available() }

// Refresh implements Manager.
func (z *Zypper) Refresh(ctx context.Context) error {
	if z.refreshed {
		return nil
	}
	z.refreshed = true
	_, err := z.run(ctx, "zypper", []string{"--non-interactive", "refresh"}, true)
	return err
}

// Install implements Manager.
func (z *Zypper) Install(ctx context.Context, request Request) (Result, error) {
	args := append([]string{"--non-interactive", "install", "--auto-agree-with-licenses"}, request.ExtraArgs...)
	return z.run(ctx, "zypper", append(args, request.Packages...), request.Elevate)
}

// Upgrade implements Manager.
func (z *Zypper) Upgrade(ctx context.Context, request Request) (Result, error) {
	args := append([]string{"--non-interactive", "update"}, request.ExtraArgs...)
	return z.run(ctx, "zypper", append(args, request.Packages...), request.Elevate)
}

// IsInstalled implements Manager.
func (z *Zypper) IsInstalled(ctx context.Context, pkg string) bool {
	return z.query(ctx, "rpm", "-q", pkg).Success()
}

// Version implements Manager.
func (z *Zypper) Version(ctx context.Context, pkg string) string {
	result := z.query(ctx, "rpm", "-q", "--queryformat", "%{VERSION}", pkg)
	if !result.Success() {
		return ""
	}
	return strings.TrimSpace(result.Stdout)
}

// InstallLocal implements Manager.
func (z *Zypper) InstallLocal(ctx context.Context, path string, elevate bool) (Result, error) {
	return z.run(ctx, "zypper", []string{"--non-interactive", "install", "--allow-unsigned-rpm", localPath(path)}, elevate)
}

// --- pacman (Arch and derivatives) ---------------------------------------

// Pacman adapts Arch Linux's package manager.
type Pacman struct{ base }

// NewPacman returns the pacman adapter.
func NewPacman(options Options) *Pacman {
	return &Pacman{base{
		runner: options.Runner, elevated: options.Elevated, dryRun: options.DryRun,
		executable: "pacman",
	}}
}

// Name implements Manager.
func (p *Pacman) Name() string { return "pacman" }

// IsAvailable implements Manager.
func (p *Pacman) IsAvailable() bool { return p.available() }

// Refresh implements Manager.
func (p *Pacman) Refresh(ctx context.Context) error {
	if p.refreshed {
		return nil
	}
	p.refreshed = true
	_, err := p.run(ctx, "pacman", []string{"-Sy", "--noconfirm"}, true)
	return err
}

// Install implements Manager. --needed makes the call idempotent: pacman skips
// packages that are already at the target version instead of reinstalling.
func (p *Pacman) Install(ctx context.Context, request Request) (Result, error) {
	args := append([]string{"-S", "--noconfirm", "--needed"}, request.ExtraArgs...)
	return p.run(ctx, "pacman", append(args, request.Packages...), request.Elevate)
}

// Upgrade implements Manager.
func (p *Pacman) Upgrade(ctx context.Context, request Request) (Result, error) {
	args := append([]string{"-S", "--noconfirm"}, request.ExtraArgs...)
	return p.run(ctx, "pacman", append(args, request.Packages...), request.Elevate)
}

// IsInstalled implements Manager.
func (p *Pacman) IsInstalled(ctx context.Context, pkg string) bool {
	return p.query(ctx, "pacman", "-Q", pkg).Success()
}

// Version implements Manager. `pacman -Q name` prints "name version".
func (p *Pacman) Version(ctx context.Context, pkg string) string {
	result := p.query(ctx, "pacman", "-Q", pkg)
	if !result.Success() {
		return ""
	}
	fields := strings.Fields(result.Stdout)
	if len(fields) < 2 {
		return ""
	}
	return fields[1]
}

// InstallLocal implements Manager.
func (p *Pacman) InstallLocal(ctx context.Context, path string, elevate bool) (Result, error) {
	return p.run(ctx, "pacman", []string{"-U", "--noconfirm", path}, elevate)
}

// --- snap -----------------------------------------------------------------

// Snap adapts snapd, which is how several vendors publish desktop
// applications for Linux.
type Snap struct{ base }

// NewSnap returns the snap adapter.
func NewSnap(options Options) *Snap {
	return &Snap{base{
		runner: options.Runner, elevated: options.Elevated, dryRun: options.DryRun,
		executable: "snap",
	}}
}

// Name implements Manager.
func (s *Snap) Name() string { return "snap" }

// IsAvailable implements Manager.
func (s *Snap) IsAvailable() bool { return s.available() }

// Refresh implements Manager. snapd tracks its store continuously.
func (s *Snap) Refresh(context.Context) error { return nil }

// Install implements Manager.
func (s *Snap) Install(ctx context.Context, request Request) (Result, error) {
	var combined Result
	for _, pkg := range request.Packages {
		args := []string{"install", pkg}
		if request.Classic {
			args = append(args, "--classic")
		}
		args = append(args, request.ExtraArgs...)

		result, err := s.run(ctx, "snap", args, request.Elevate)
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

// Upgrade implements Manager.
func (s *Snap) Upgrade(ctx context.Context, request Request) (Result, error) {
	return s.run(ctx, "snap", append([]string{"refresh"}, request.Packages...), request.Elevate)
}

// IsInstalled implements Manager.
func (s *Snap) IsInstalled(ctx context.Context, pkg string) bool {
	return s.query(ctx, "snap", "list", pkg).Success()
}

// Version implements Manager. `snap list name` prints a header row followed by
// the package row, whose second column is the version.
func (s *Snap) Version(ctx context.Context, pkg string) string {
	result := s.query(ctx, "snap", "list", pkg)
	if !result.Success() {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	if len(lines) < 2 {
		return ""
	}
	fields := strings.Fields(lines[1])
	if len(fields) < 2 {
		return ""
	}
	return fields[1]
}

// InstallLocal implements Manager.
func (s *Snap) InstallLocal(_ context.Context, path string, _ bool) (Result, error) {
	err := fmt.Errorf("snap cannot install the downloaded package file %s", path)
	return Result{Message: err.Error()}, err
}

// localPath makes a package file path explicitly relative or absolute. apt-get
// and dnf treat a bare "code.deb" as a package *name* to look up in the
// archive; "./code.deb" is what makes them read the file.
func localPath(path string) string {
	if strings.ContainsAny(path, "/\\") {
		return path
	}
	return "./" + path
}
