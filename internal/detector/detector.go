// Package detector works out what machine Bento is running on. Everything it
// reports is observed — from /etc/os-release, from sw_vers, from the presence
// of a package manager on PATH — never assumed from the OS name alone.
package detector

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/Aryan27-max/bento-box/internal/command"
)

// System is everything Bento knows about the current machine.
type System struct {
	// OS is the Go-style operating system name: windows, linux or darwin.
	OS string `json:"os"`
	// Arch is the Go-style architecture: amd64 or arm64.
	Arch string `json:"architecture"`
	// OSName is the human-readable name, such as "Windows 11" or
	// "Ubuntu 24.04.1 LTS".
	OSName string `json:"os_name"`
	// OSVersion is the raw version string reported by the system.
	OSVersion string `json:"os_version,omitempty"`
	// Distro is populated on Linux only.
	Distro Distro `json:"distro,omitzero"`
	// Shell is the user's login shell and the file Bento would configure.
	Shell Shell `json:"shell,omitzero"`
	// PackageManagers lists the managers found on this machine, in the order
	// Bento prefers them.
	PackageManagers []string `json:"package_managers"`
	// Elevated reports whether Bento is running with administrator or root
	// privileges.
	Elevated bool `json:"elevated"`
	// NvidiaGPU reports whether an NVIDIA GPU was actually observed.
	NvidiaGPU bool   `json:"nvidia_gpu"`
	GPUName   string `json:"gpu_name,omitempty"`

	Home     string `json:"home"`
	User     string `json:"user,omitempty"`
	Hostname string `json:"hostname,omitempty"`
}

// Distro describes a Linux distribution as reported by /etc/os-release.
type Distro struct {
	// ID is the machine-readable identifier: ubuntu, fedora, arch, opensuse.
	ID string `json:"id,omitempty"`
	// Name is the pretty name.
	Name string `json:"name,omitempty"`
	// Version is VERSION_ID.
	Version string `json:"version,omitempty"`
	// Like holds ID_LIKE, which is how derivatives (Linux Mint, Pop!_OS)
	// declare the family whose package manager they use.
	Like string `json:"like,omitempty"`
}

// Shell describes the user's shell and where its configuration lives.
type Shell struct {
	// Name is bash, zsh, fish, powershell or cmd.
	Name string `json:"name,omitempty"`
	Path string `json:"path,omitempty"`
	// ConfigFile is the file Bento would append its managed block to.
	ConfigFile string `json:"config_file,omitempty"`
}

// Display renders the system as shown on the Bento start screen.
func (s System) Display() string {
	if s.OSName != "" {
		return s.OSName
	}
	return s.OS
}

// PreferredManager returns the package manager Bento will reach for first, or
// an empty string when the machine has none.
func (s System) PreferredManager() string {
	if len(s.PackageManagers) == 0 {
		return ""
	}
	return s.PackageManagers[0]
}

// HasManager reports whether a named package manager is available.
func (s System) HasManager(name string) bool {
	for _, candidate := range s.PackageManagers {
		if candidate == name {
			return true
		}
	}
	return false
}

// Detector gathers system facts. Every external interaction is behind an
// injectable field so the whole detector can be tested without a real machine.
type Detector struct {
	// Runner executes probe commands.
	Runner command.Runner
	// GOOS and GOARCH default to the running binary's values.
	GOOS   string
	GOARCH string
	// RootFS is the filesystem used to read /etc/os-release. It is rooted at
	// the filesystem root, so the path read is "etc/os-release".
	RootFS fs.FS
	// Getenv reads environment variables.
	Getenv func(string) string
	// Geteuid returns the effective user id on Unix.
	Geteuid func() int
	// HomeDir returns the user's home directory.
	HomeDir func() (string, error)
}

// New returns a Detector wired to the real machine.
func New(runner command.Runner) *Detector {
	return &Detector{
		Runner:  runner,
		GOOS:    runtime.GOOS,
		GOARCH:  runtime.GOARCH,
		RootFS:  os.DirFS(rootPath()),
		Getenv:  os.Getenv,
		Geteuid: os.Geteuid,
		HomeDir: os.UserHomeDir,
	}
}

func rootPath() string {
	if runtime.GOOS == "windows" {
		// Reading /etc/os-release is meaningless on Windows; a root of "."
		// keeps os.DirFS valid without pointing at anything sensitive.
		return "."
	}
	return "/"
}

// Detect gathers everything about the current machine. It never returns an
// error for a fact it merely could not observe: unknown values are left empty
// so the report can say "unknown" instead of inventing something.
func (d *Detector) Detect(ctx context.Context) System {
	d.applyDefaults()

	system := System{OS: d.GOOS, Arch: d.GOARCH}
	system.Home, _ = d.HomeDir()
	system.Hostname, _ = os.Hostname()
	if account, err := user.Current(); err == nil {
		system.User = account.Username
	}

	switch d.GOOS {
	case "windows":
		system.OSName, system.OSVersion = d.detectWindows(ctx)
	case "darwin":
		system.OSName, system.OSVersion = d.detectDarwin(ctx)
	case "linux":
		system.Distro = d.detectDistro()
		system.OSName = system.Distro.Name
		system.OSVersion = system.Distro.Version
		if system.OSName == "" {
			system.OSName = "Linux"
		}
	default:
		system.OSName = d.GOOS
	}

	system.Shell = d.detectShell(system.Home)
	system.PackageManagers = d.detectPackageManagers(system.Distro)
	system.Elevated = d.detectElevation(ctx)
	system.NvidiaGPU, system.GPUName = d.detectNvidiaGPU(ctx)
	return system
}

func (d *Detector) applyDefaults() {
	if d.GOOS == "" {
		d.GOOS = runtime.GOOS
	}
	if d.GOARCH == "" {
		d.GOARCH = runtime.GOARCH
	}
	if d.Getenv == nil {
		d.Getenv = os.Getenv
	}
	if d.Geteuid == nil {
		d.Geteuid = os.Geteuid
	}
	if d.HomeDir == nil {
		d.HomeDir = os.UserHomeDir
	}
	if d.RootFS == nil {
		d.RootFS = os.DirFS(rootPath())
	}
}

// detectWindows reads the version from `cmd /c ver`, which reports the true
// build number. The marketing name is derived from that build, because
// Windows 11 still identifies itself as version 10.0.
func (d *Detector) detectWindows(ctx context.Context) (name, version string) {
	result, err := d.Runner.Run(ctx, command.Spec{
		Name: "cmd", Args: []string{"/c", "ver"}, AllowFailure: true,
	})
	if err != nil || !result.Success() {
		return "Windows", ""
	}
	version = strings.TrimSpace(result.Output())
	return WindowsName(version), extractWindowsVersion(version)
}

// WindowsName maps the output of `ver` to a product name. Windows 11 is
// Windows 10.0 with a build number of 22000 or higher, which is the only
// reliable way to tell them apart from a version string.
func WindowsName(verOutput string) string {
	build := windowsBuild(verOutput)
	switch {
	case build == 0:
		return "Windows"
	case build >= 22000:
		return "Windows 11"
	case build >= 10240:
		return "Windows 10"
	default:
		return "Windows"
	}
}

func windowsBuild(verOutput string) int {
	version := extractWindowsVersion(verOutput)
	parts := strings.Split(version, ".")
	if len(parts) < 3 {
		return 0
	}
	build, err := strconv.Atoi(parts[2])
	if err != nil {
		return 0
	}
	return build
}

func extractWindowsVersion(verOutput string) string {
	start := strings.LastIndex(verOutput, "[")
	end := strings.LastIndex(verOutput, "]")
	segment := verOutput
	if start >= 0 && end > start {
		segment = verOutput[start+1 : end]
	}
	fields := strings.Fields(segment)
	if len(fields) == 0 {
		return ""
	}
	return fields[len(fields)-1]
}

func (d *Detector) detectDarwin(ctx context.Context) (name, version string) {
	result, err := d.Runner.Run(ctx, command.Spec{
		Name: "sw_vers", Args: []string{"-productVersion"}, AllowFailure: true,
	})
	if err != nil || !result.Success() {
		return "macOS", ""
	}
	version = strings.TrimSpace(result.Output())
	if version == "" {
		return "macOS", ""
	}
	return "macOS " + version, version
}

func (d *Detector) detectDistro() Distro {
	for _, path := range []string{"etc/os-release", "usr/lib/os-release"} {
		data, err := fs.ReadFile(d.RootFS, path)
		if err != nil {
			continue
		}
		return ParseOSRelease(string(data))
	}
	return Distro{}
}

// ParseOSRelease reads the freedesktop os-release format. Values may be
// quoted, and comments and blank lines are ignored.
func ParseOSRelease(content string) Distro {
	fields := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		fields[strings.TrimSpace(key)] = value
	}

	distro := Distro{
		ID:      fields["ID"],
		Version: fields["VERSION_ID"],
		Like:    fields["ID_LIKE"],
	}
	if pretty := fields["PRETTY_NAME"]; pretty != "" {
		distro.Name = pretty
	} else if name := fields["NAME"]; name != "" {
		distro.Name = strings.TrimSpace(name + " " + distro.Version)
	}
	return distro
}

// managerProbes maps a package manager to the executable that proves it is
// installed. Detection is by presence, never by guessing from the distro name,
// because derivatives and minimal images routinely surprise you.
var managerProbes = []struct{ manager, executable string }{
	{"winget", "winget"},
	{"apt", "apt-get"},
	{"dnf", "dnf"},
	{"pacman", "pacman"},
	{"zypper", "zypper"},
	{"brew", "brew"},
	{"snap", "snap"},
}

func (d *Detector) detectPackageManagers(distro Distro) []string {
	var found []string
	for _, probe := range managerProbes {
		if command.Available(d.Runner, probe.executable) {
			found = append(found, probe.manager)
		}
	}
	return prioritise(found, distro)
}

// prioritise orders detected managers so the distribution's native manager
// comes first. A Fedora box with Homebrew installed should still install
// system packages with dnf.
func prioritise(found []string, distro Distro) []string {
	if len(found) < 2 {
		return found
	}
	family := strings.ToLower(distro.ID + " " + distro.Like)
	preferred := ""
	switch {
	case strings.Contains(family, "debian"), strings.Contains(family, "ubuntu"):
		preferred = "apt"
	case strings.Contains(family, "fedora"), strings.Contains(family, "rhel"), strings.Contains(family, "centos"):
		preferred = "dnf"
	case strings.Contains(family, "arch"):
		preferred = "pacman"
	case strings.Contains(family, "suse"):
		preferred = "zypper"
	}
	if preferred == "" {
		return found
	}

	out := make([]string, 0, len(found))
	for _, manager := range found {
		if manager == preferred {
			out = append(out, manager)
		}
	}
	for _, manager := range found {
		if manager != preferred {
			out = append(out, manager)
		}
	}
	return out
}

func (d *Detector) detectShell(home string) Shell {
	if d.GOOS == "windows" {
		// PowerShell always exports PSModulePath; its absence means Bento was
		// started from cmd. This only affects the advice Bento prints, since
		// Windows environment variables are written to the registry either way.
		if d.Getenv("PSModulePath") != "" {
			return Shell{Name: "powershell"}
		}
		return Shell{Name: "cmd"}
	}

	path := d.Getenv("SHELL")
	name := filepath.Base(path)
	if name == "." || name == "/" || name == "" {
		name = ""
	}
	return Shell{Name: name, Path: path, ConfigFile: ShellConfigFile(name, home)}
}

// ShellConfigFile returns the file Bento appends its managed environment block
// to for a given shell. An unrecognised shell falls back to ~/.profile, which
// almost every POSIX shell reads at login.
func ShellConfigFile(shell, home string) string {
	if home == "" {
		return ""
	}
	switch shell {
	case "zsh":
		return filepath.Join(home, ".zshrc")
	case "bash":
		return filepath.Join(home, ".bashrc")
	case "fish":
		return filepath.Join(home, ".config", "fish", "config.fish")
	case "":
		return filepath.Join(home, ".profile")
	default:
		return filepath.Join(home, ".profile")
	}
}

func (d *Detector) detectElevation(ctx context.Context) bool {
	if d.GOOS == "windows" {
		// The High Mandatory Level SID is present in an elevated token's
		// group list and absent otherwise.
		result, err := d.Runner.Run(ctx, command.Spec{
			Name: "whoami", Args: []string{"/groups"}, AllowFailure: true,
		})
		if err != nil || !result.Success() {
			return false
		}
		return strings.Contains(result.Stdout, "S-1-16-12288")
	}
	return d.Geteuid() == 0
}

func (d *Detector) detectNvidiaGPU(ctx context.Context) (bool, string) {
	if command.Available(d.Runner, "nvidia-smi") {
		result, err := d.Runner.Run(ctx, command.Spec{
			Name: "nvidia-smi", Args: []string{"--query-gpu=name", "--format=csv,noheader"},
			AllowFailure: true,
		})
		if err == nil && result.Success() {
			if name := firstLine(result.Output()); name != "" {
				return true, name
			}
		}
	}

	// On Linux a GPU can be present without the driver tooling installed, in
	// which case lspci still sees the hardware.
	if d.GOOS == "linux" && command.Available(d.Runner, "lspci") {
		result, err := d.Runner.Run(ctx, command.Spec{Name: "lspci", AllowFailure: true})
		if err == nil && result.Success() {
			for _, line := range strings.Split(result.Stdout, "\n") {
				if strings.Contains(strings.ToUpper(line), "NVIDIA") &&
					(strings.Contains(line, "VGA") || strings.Contains(line, "3D controller")) {
					return true, strings.TrimSpace(line)
				}
			}
		}
	}
	return false, ""
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return strings.TrimSpace(line)
}

// Summary renders a short multi-line description used on the start screen.
func (s System) Summary() string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "OS:           %s\n", s.Display())
	fmt.Fprintf(&builder, "Architecture: %s\n", s.Arch)
	if len(s.PackageManagers) > 0 {
		fmt.Fprintf(&builder, "Packages:     %s\n", strings.Join(s.PackageManagers, ", "))
	} else {
		builder.WriteString("Packages:     none detected\n")
	}
	if s.NvidiaGPU {
		fmt.Fprintf(&builder, "GPU:          %s\n", s.GPUName)
	}
	return builder.String()
}
