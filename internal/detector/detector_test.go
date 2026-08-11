package detector

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Aryan27-max/bento-box/internal/command"
)

func newTestDetector(goos, goarch string, runner *command.Mock) *Detector {
	return &Detector{
		Runner:  runner,
		GOOS:    goos,
		GOARCH:  goarch,
		RootFS:  fstest.MapFS{},
		Getenv:  func(string) string { return "" },
		Geteuid: func() int { return 1000 },
		HomeDir: func() (string, error) { return "/home/dev", nil },
	}
}

func TestDetectWindows(t *testing.T) {
	runner := command.NewMock()
	runner.RespondOK("cmd /c ver", "\nMicrosoft Windows [Version 10.0.26200.1234]\n")
	runner.RespondOK("whoami /groups", "Mandatory Label\\High Mandatory Level  Label  S-1-16-12288")
	runner.AddPath("winget", `C:\winget.exe`)

	detector := newTestDetector("windows", "amd64", runner)
	detector.Getenv = func(key string) string {
		if key == "PSModulePath" {
			return `C:\Program Files\PowerShell\Modules`
		}
		return ""
	}

	system := detector.Detect(context.Background())

	if system.OSName != "Windows 11" {
		t.Errorf("OSName = %q, want Windows 11", system.OSName)
	}
	if system.OSVersion != "10.0.26200.1234" {
		t.Errorf("OSVersion = %q, want 10.0.26200.1234", system.OSVersion)
	}
	if system.Arch != "amd64" {
		t.Errorf("Arch = %q, want amd64", system.Arch)
	}
	if !system.HasManager("winget") {
		t.Errorf("PackageManagers = %v, want winget", system.PackageManagers)
	}
	if !system.Elevated {
		t.Error("expected the High Mandatory Level SID to be read as elevated")
	}
	if system.Shell.Name != "powershell" {
		t.Errorf("Shell = %q, want powershell", system.Shell.Name)
	}
}

func TestWindowsNameFromBuildNumber(t *testing.T) {
	// Windows 11 reports itself as version 10.0; only the build tells them
	// apart, which is exactly the trap this mapping exists to avoid.
	cases := map[string]string{
		"Microsoft Windows [Version 10.0.26200.1234]": "Windows 11",
		"Microsoft Windows [Version 10.0.22000.194]":  "Windows 11",
		"Microsoft Windows [Version 10.0.19045.5247]": "Windows 10",
		"Microsoft Windows [Version 10.0.10240.0]":    "Windows 10",
		"garbage": "Windows",
	}
	for input, want := range cases {
		if got := WindowsName(input); got != want {
			t.Errorf("WindowsName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestWindowsNotElevated(t *testing.T) {
	runner := command.NewMock()
	runner.RespondOK("cmd /c ver", "Microsoft Windows [Version 10.0.26200.1234]")
	runner.RespondOK("whoami /groups", "Mandatory Label\\Medium Mandatory Level  Label  S-1-16-8192")

	system := newTestDetector("windows", "amd64", runner).Detect(context.Background())
	if system.Elevated {
		t.Error("a medium integrity token must not be reported as elevated")
	}
}

func TestDetectMacOS(t *testing.T) {
	runner := command.NewMock()
	runner.RespondOK("sw_vers -productVersion", "15.2\n")
	runner.AddPath("brew", "/opt/homebrew/bin/brew")

	detector := newTestDetector("darwin", "arm64", runner)
	detector.Getenv = func(key string) string {
		if key == "SHELL" {
			return "/bin/zsh"
		}
		return ""
	}

	system := detector.Detect(context.Background())

	if system.OSName != "macOS 15.2" {
		t.Errorf("OSName = %q, want macOS 15.2", system.OSName)
	}
	if system.Arch != "arm64" {
		t.Errorf("Arch = %q, want arm64", system.Arch)
	}
	if !system.HasManager("brew") {
		t.Errorf("PackageManagers = %v, want brew", system.PackageManagers)
	}
	if system.Shell.Name != "zsh" {
		t.Errorf("Shell = %q, want zsh", system.Shell.Name)
	}
	if want := filepath.Join("/home/dev", ".zshrc"); system.Shell.ConfigFile != want {
		t.Errorf("ConfigFile = %q, want %q", system.Shell.ConfigFile, want)
	}
}

func TestDetectLinuxDistributions(t *testing.T) {
	cases := []struct {
		name        string
		osRelease   string
		executables []string
		wantID      string
		wantName    string
		wantManager string
	}{
		{
			name: "ubuntu",
			osRelease: `NAME="Ubuntu"
VERSION="24.04.1 LTS (Noble Numbat)"
ID=ubuntu
ID_LIKE=debian
PRETTY_NAME="Ubuntu 24.04.1 LTS"
VERSION_ID="24.04"`,
			executables: []string{"apt-get", "snap"},
			wantID:      "ubuntu",
			wantName:    "Ubuntu 24.04.1 LTS",
			wantManager: "apt",
		},
		{
			name: "fedora",
			osRelease: `NAME="Fedora Linux"
VERSION="41 (Workstation Edition)"
ID=fedora
VERSION_ID=41
PRETTY_NAME="Fedora Linux 41 (Workstation Edition)"`,
			executables: []string{"dnf"},
			wantID:      "fedora",
			wantName:    "Fedora Linux 41 (Workstation Edition)",
			wantManager: "dnf",
		},
		{
			name: "arch",
			osRelease: `NAME="Arch Linux"
PRETTY_NAME="Arch Linux"
ID=arch
BUILD_ID=rolling`,
			executables: []string{"pacman"},
			wantID:      "arch",
			wantName:    "Arch Linux",
			wantManager: "pacman",
		},
		{
			name: "opensuse",
			osRelease: `NAME="openSUSE Tumbleweed"
ID="opensuse-tumbleweed"
ID_LIKE="opensuse suse"
VERSION_ID="20250210"
PRETTY_NAME="openSUSE Tumbleweed"`,
			executables: []string{"zypper"},
			wantID:      "opensuse-tumbleweed",
			wantName:    "openSUSE Tumbleweed",
			wantManager: "zypper",
		},
		{
			name: "pop-os derivative",
			osRelease: `NAME="Pop!_OS"
VERSION="22.04 LTS"
ID=pop
ID_LIKE="ubuntu debian"
PRETTY_NAME="Pop!_OS 22.04 LTS"
VERSION_ID="22.04"`,
			executables: []string{"apt-get"},
			wantID:      "pop",
			wantName:    "Pop!_OS 22.04 LTS",
			wantManager: "apt",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := command.NewMock()
			for _, executable := range tc.executables {
				runner.AddPath(executable, "/usr/bin/"+executable)
			}

			detector := newTestDetector("linux", "amd64", runner)
			detector.RootFS = fstest.MapFS{"etc/os-release": {Data: []byte(tc.osRelease)}}
			detector.Getenv = func(key string) string {
				if key == "SHELL" {
					return "/bin/bash"
				}
				return ""
			}

			system := detector.Detect(context.Background())

			if system.Distro.ID != tc.wantID {
				t.Errorf("Distro.ID = %q, want %q", system.Distro.ID, tc.wantID)
			}
			if system.OSName != tc.wantName {
				t.Errorf("OSName = %q, want %q", system.OSName, tc.wantName)
			}
			if system.PreferredManager() != tc.wantManager {
				t.Errorf("PreferredManager = %q, want %q (detected %v)",
					system.PreferredManager(), tc.wantManager, system.PackageManagers)
			}
			if system.Shell.Name != "bash" {
				t.Errorf("Shell = %q, want bash", system.Shell.Name)
			}
		})
	}
}

// TestDistroManagerWinsOverHomebrew covers the machine that has both: a Fedora
// box with Homebrew installed should still install system packages with dnf.
func TestDistroManagerWinsOverHomebrew(t *testing.T) {
	runner := command.NewMock()
	runner.AddPath("brew", "/home/linuxbrew/.linuxbrew/bin/brew")
	runner.AddPath("dnf", "/usr/bin/dnf")

	detector := newTestDetector("linux", "amd64", runner)
	detector.RootFS = fstest.MapFS{"etc/os-release": {Data: []byte("ID=fedora\nPRETTY_NAME=\"Fedora Linux 41\"")}}

	system := detector.Detect(context.Background())
	if system.PreferredManager() != "dnf" {
		t.Errorf("PreferredManager = %q, want dnf (got order %v)", system.PreferredManager(), system.PackageManagers)
	}
	if !system.HasManager("brew") {
		t.Error("brew should still be listed as available")
	}
}

func TestParseOSReleaseHandlesMessyFiles(t *testing.T) {
	distro := ParseOSRelease(`# a comment

NAME='Debian GNU/Linux'
ID=debian
VERSION_ID="12"
PRETTY_NAME="Debian GNU/Linux 12 (bookworm)"
MALFORMED_LINE
`)
	if distro.ID != "debian" {
		t.Errorf("ID = %q, want debian", distro.ID)
	}
	if distro.Version != "12" {
		t.Errorf("Version = %q, want 12", distro.Version)
	}
	if distro.Name != "Debian GNU/Linux 12 (bookworm)" {
		t.Errorf("Name = %q", distro.Name)
	}
}

func TestParseOSReleaseFallsBackToName(t *testing.T) {
	distro := ParseOSRelease("NAME=\"Alpine Linux\"\nID=alpine\nVERSION_ID=3.21.0\n")
	if distro.Name != "Alpine Linux 3.21.0" {
		t.Errorf("Name = %q, want the NAME and VERSION_ID combined", distro.Name)
	}
}

func TestLinuxWithoutOSReleaseDegradesGracefully(t *testing.T) {
	detector := newTestDetector("linux", "arm64", command.NewMock())
	system := detector.Detect(context.Background())

	if system.OSName != "Linux" {
		t.Errorf("OSName = %q, want Linux", system.OSName)
	}
	if len(system.PackageManagers) != 0 {
		t.Errorf("PackageManagers = %v, want none", system.PackageManagers)
	}
}

func TestRootIsDetectedAsElevated(t *testing.T) {
	detector := newTestDetector("linux", "amd64", command.NewMock())
	detector.Geteuid = func() int { return 0 }

	if !detector.Detect(context.Background()).Elevated {
		t.Error("uid 0 must be reported as elevated")
	}
}

func TestNvidiaGPUDetection(t *testing.T) {
	runner := command.NewMock()
	runner.RespondOK("nvidia-smi --query-gpu=name --format=csv,noheader", "NVIDIA GeForce RTX 4070\n")

	system := newTestDetector("linux", "amd64", runner).Detect(context.Background())
	if !system.NvidiaGPU {
		t.Fatal("expected an NVIDIA GPU to be detected")
	}
	if system.GPUName != "NVIDIA GeForce RTX 4070" {
		t.Errorf("GPUName = %q", system.GPUName)
	}
}

func TestNvidiaGPUDetectionViaLspci(t *testing.T) {
	runner := command.NewMock()
	runner.RespondOK("lspci", "00:02.0 VGA compatible controller: Intel Corporation UHD Graphics\n"+
		"01:00.0 VGA compatible controller: NVIDIA Corporation GA106 [GeForce RTX 3060]\n")

	system := newTestDetector("linux", "amd64", runner).Detect(context.Background())
	if !system.NvidiaGPU {
		t.Error("lspci output naming an NVIDIA VGA controller should count as a GPU")
	}
}

func TestNoGPUIsReportedHonestly(t *testing.T) {
	runner := command.NewMock()
	runner.RespondOK("lspci", "00:02.0 VGA compatible controller: Intel Corporation UHD Graphics\n")

	system := newTestDetector("linux", "amd64", runner).Detect(context.Background())
	if system.NvidiaGPU {
		t.Error("an Intel-only machine must not be reported as having an NVIDIA GPU")
	}
	if system.GPUName != "" {
		t.Errorf("GPUName = %q, want empty", system.GPUName)
	}
}

func TestShellConfigFileMapping(t *testing.T) {
	home := "/home/dev"
	cases := map[string]string{
		"zsh":     filepath.Join(home, ".zshrc"),
		"bash":    filepath.Join(home, ".bashrc"),
		"fish":    filepath.Join(home, ".config", "fish", "config.fish"),
		"":        filepath.Join(home, ".profile"),
		"unknown": filepath.Join(home, ".profile"),
	}
	for shell, want := range cases {
		if got := ShellConfigFile(shell, home); got != want {
			t.Errorf("ShellConfigFile(%q) = %q, want %q", shell, got, want)
		}
	}
	if got := ShellConfigFile("bash", ""); got != "" {
		t.Errorf("ShellConfigFile with no home = %q, want empty", got)
	}
}

func TestSummaryMentionsWhatWasDetected(t *testing.T) {
	system := System{
		OS: "linux", Arch: "amd64", OSName: "Ubuntu 24.04.1 LTS",
		PackageManagers: []string{"apt", "snap"},
		NvidiaGPU:       true, GPUName: "NVIDIA GeForce RTX 4070",
	}
	summary := system.Summary()
	for _, want := range []string{"Ubuntu 24.04.1 LTS", "amd64", "apt, snap", "RTX 4070"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary is missing %q:\n%s", want, summary)
		}
	}
}
