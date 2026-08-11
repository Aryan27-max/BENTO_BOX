package pkgmanager

import (
	"context"
	"strings"
	"testing"

	"github.com/Aryan27-max/bento-box/internal/command"
)

func newMock(executables ...string) *command.Mock {
	mock := command.NewMock()
	for _, executable := range executables {
		mock.AddPath(executable, "/usr/bin/"+executable)
	}
	return mock
}

func TestRegistryOnlyKeepsManagersThatExist(t *testing.T) {
	mock := newMock("apt-get", "snap")
	registry := NewRegistry(Options{Runner: mock})

	if got := registry.Available(); strings.Join(got, ",") != "apt,snap" {
		t.Errorf("Available() = %v, want [apt snap]", got)
	}
	if registry.Has("winget") {
		t.Error("winget must not be reported on a machine without it")
	}
	if _, ok := registry.Get("apt"); !ok {
		t.Error("apt should be retrievable")
	}
}

func TestWingetInstallIsNonInteractive(t *testing.T) {
	mock := newMock("winget")
	mock.RespondOK("winget install", "Successfully installed")

	winget := NewWinget(Options{Runner: mock})
	if _, err := winget.Install(context.Background(), Request{Packages: []string{"Git.Git"}}); err != nil {
		t.Fatalf("Install: %v", err)
	}

	invocation := mock.Invocations()[0]
	// Without these flags winget stops and waits for a keypress, which would
	// hang an unattended run forever.
	for _, flag := range []string{"--exact", "--silent", "--accept-package-agreements", "--accept-source-agreements", "--disable-interactivity"} {
		if !strings.Contains(invocation, flag) {
			t.Errorf("winget invocation %q is missing %s", invocation, flag)
		}
	}
	if !strings.Contains(invocation, "--id Git.Git") {
		t.Errorf("winget invocation %q does not install the requested package", invocation)
	}
}

func TestWingetInstallsPackagesIndividually(t *testing.T) {
	mock := newMock("winget")
	mock.RespondOK("winget install", "ok")

	winget := NewWinget(Options{Runner: mock})
	if _, err := winget.Install(context.Background(), Request{Packages: []string{"A.One", "B.Two"}}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(mock.Calls) != 2 {
		t.Fatalf("expected one invocation per package, got %d", len(mock.Calls))
	}
}

func TestWingetReportsFailure(t *testing.T) {
	mock := newMock("winget")
	mock.RespondFail("winget install", 1, "No package found matching input criteria.")

	winget := NewWinget(Options{Runner: mock})
	result, err := winget.Install(context.Background(), Request{Packages: []string{"Nope.Nope"}})
	if err == nil {
		t.Fatal("expected an error when winget fails")
	}
	if !strings.Contains(result.Message, "No package found") {
		t.Errorf("Message = %q, want winget's explanation", result.Message)
	}
}

func TestWingetPresenceAndVersion(t *testing.T) {
	mock := newMock("winget")
	mock.RespondOK("winget list --id Git.Git",
		"Name  Id       Version\n-------------------------\nGit   Git.Git  2.47.1")

	winget := NewWinget(Options{Runner: mock})
	if !winget.IsInstalled(context.Background(), "Git.Git") {
		t.Error("Git.Git should be reported as installed")
	}
	if got := winget.Version(context.Background(), "Git.Git"); got != "2.47.1" {
		t.Errorf("Version = %q, want 2.47.1", got)
	}
}

// TestWingetZeroExitWithNoResultsIsNotInstalled covers winget's habit of
// exiting cleanly while reporting that it found nothing.
func TestWingetZeroExitWithNoResultsIsNotInstalled(t *testing.T) {
	mock := newMock("winget")
	mock.RespondOK("winget list --id Ghost.Ghost", "No installed package found matching input criteria.")

	winget := NewWinget(Options{Runner: mock})
	if winget.IsInstalled(context.Background(), "Ghost.Ghost") {
		t.Error("a 'no package found' message must not be read as installed")
	}
}

func TestAptInstallIsNonInteractiveAndElevates(t *testing.T) {
	mock := newMock("apt-get", "sudo", "dpkg-query")
	mock.RespondOK("sudo -n env DEBIAN_FRONTEND=noninteractive apt-get install", "done")

	apt := NewApt(Options{Runner: mock, Elevated: false})
	if _, err := apt.Install(context.Background(), Request{Packages: []string{"git"}, Elevate: true}); err != nil {
		t.Fatalf("Install: %v", err)
	}

	invocation := mock.Invocations()[0]
	if !strings.HasPrefix(invocation, "sudo -n env DEBIAN_FRONTEND=noninteractive apt-get install -y") {
		t.Errorf("apt invocation = %q, want a non-interactive sudo call", invocation)
	}
	if !strings.HasSuffix(invocation, "git") {
		t.Errorf("apt invocation = %q, want it to install git", invocation)
	}
}

func TestAptDoesNotSudoWhenAlreadyRoot(t *testing.T) {
	mock := newMock("apt-get", "sudo")
	mock.RespondOK("apt-get install", "done")

	apt := NewApt(Options{Runner: mock, Elevated: true})
	if _, err := apt.Install(context.Background(), Request{Packages: []string{"git"}, Elevate: true}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if invocation := mock.Invocations()[0]; strings.HasPrefix(invocation, "sudo") {
		t.Errorf("running as root should not shell out to sudo: %q", invocation)
	}
}

func TestElevationFailsClearlyWithoutSudo(t *testing.T) {
	mock := newMock("dnf") // no sudo on PATH
	dnf := NewDnf(Options{Runner: mock, Elevated: false})

	_, err := dnf.Install(context.Background(), Request{Packages: []string{"git"}, Elevate: true})
	if err == nil {
		t.Fatal("expected an error when elevation is impossible")
	}
	if !strings.Contains(err.Error(), "root") {
		t.Errorf("error = %q, want it to explain that root is needed", err)
	}
	if len(mock.Calls) != 0 {
		t.Error("nothing should be executed when elevation is impossible")
	}
}

func TestAptRefreshRunsOnlyOnce(t *testing.T) {
	mock := newMock("apt-get", "sudo")
	mock.RespondOK("sudo -n apt-get update", "done")

	apt := NewApt(Options{Runner: mock})
	for i := 0; i < 3; i++ {
		if err := apt.Refresh(context.Background()); err != nil {
			t.Fatalf("Refresh: %v", err)
		}
	}
	if len(mock.Calls) != 1 {
		t.Errorf("apt-get update ran %d times, want 1", len(mock.Calls))
	}
}

func TestAptPresenceAndVersion(t *testing.T) {
	mock := newMock("apt-get", "dpkg-query")
	mock.RespondOK("dpkg-query -W -f=${Status} git", "install ok installed")
	mock.RespondOK("dpkg-query -W -f=${Version} git", "1:2.43.0-1ubuntu7.1")

	apt := NewApt(Options{Runner: mock})
	if !apt.IsInstalled(context.Background(), "git") {
		t.Error("git should be reported as installed")
	}
	if got := apt.Version(context.Background(), "git"); got != "1:2.43.0-1ubuntu7.1" {
		t.Errorf("Version = %q", got)
	}
}

func TestAptDeinstalledPackageIsNotInstalled(t *testing.T) {
	mock := newMock("apt-get", "dpkg-query")
	// A removed-but-not-purged package still has a dpkg entry; only the full
	// "install ok installed" status means it is really there.
	mock.RespondOK("dpkg-query -W -f=${Status} git", "deinstall ok config-files")

	apt := NewApt(Options{Runner: mock})
	if apt.IsInstalled(context.Background(), "git") {
		t.Error("a config-files-only package must not count as installed")
	}
}

func TestLocalPackageInstallUsesAnExplicitPath(t *testing.T) {
	mock := newMock("apt-get", "sudo")
	mock.RespondOK("sudo -n env DEBIAN_FRONTEND=noninteractive apt-get install -y ./code.deb", "done")

	apt := NewApt(Options{Runner: mock})
	if _, err := apt.InstallLocal(context.Background(), "code.deb", true); err != nil {
		t.Fatalf("InstallLocal: %v", err)
	}
	// A bare "code.deb" would be treated as a package name to look up in the
	// archive rather than a file to install.
	if !strings.Contains(mock.Invocations()[0], "./code.deb") {
		t.Errorf("invocation = %q, want an explicitly relative path", mock.Invocations()[0])
	}
}

func TestManagersWithoutLocalInstallSaySo(t *testing.T) {
	mock := newMock("winget", "brew", "snap")
	for _, manager := range []Manager{
		NewWinget(Options{Runner: mock}),
		NewBrew(Options{Runner: mock}),
		NewSnap(Options{Runner: mock}),
	} {
		if _, err := manager.InstallLocal(context.Background(), "/tmp/thing.deb", false); err == nil {
			t.Errorf("%s should report that it cannot install a package file", manager.Name())
		}
	}
}

func TestPacmanInstallIsIdempotent(t *testing.T) {
	mock := newMock("pacman", "sudo")
	mock.RespondOK("sudo -n pacman -S", "done")

	pacman := NewPacman(Options{Runner: mock})
	if _, err := pacman.Install(context.Background(), Request{Packages: []string{"git"}, Elevate: true}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	// --needed is what makes a second Bento run a no-op rather than a
	// reinstall.
	if !strings.Contains(mock.Invocations()[0], "--needed") {
		t.Errorf("pacman invocation %q should use --needed", mock.Invocations()[0])
	}
}

func TestPacmanVersion(t *testing.T) {
	mock := newMock("pacman")
	mock.RespondOK("pacman -Q git", "git 2.47.1-1")

	pacman := NewPacman(Options{Runner: mock})
	if got := pacman.Version(context.Background(), "git"); got != "2.47.1-1" {
		t.Errorf("Version = %q, want 2.47.1-1", got)
	}
}

func TestZypperAndDnfAreNonInteractive(t *testing.T) {
	mock := newMock("zypper", "dnf", "sudo")
	mock.RespondOK("sudo -n zypper", "done")
	mock.RespondOK("sudo -n dnf", "done")

	zypper := NewZypper(Options{Runner: mock})
	if _, err := zypper.Install(context.Background(), Request{Packages: []string{"git"}, Elevate: true}); err != nil {
		t.Fatalf("zypper Install: %v", err)
	}
	if !strings.Contains(mock.Invocations()[0], "--non-interactive") {
		t.Errorf("zypper invocation %q should be non-interactive", mock.Invocations()[0])
	}

	mock.Reset()
	dnf := NewDnf(Options{Runner: mock})
	if _, err := dnf.Install(context.Background(), Request{Packages: []string{"git"}, Elevate: true}); err != nil {
		t.Fatalf("dnf Install: %v", err)
	}
	if !strings.Contains(mock.Invocations()[0], "-y") {
		t.Errorf("dnf invocation %q should assume yes", mock.Invocations()[0])
	}
}

func TestBrewNeverUsesSudo(t *testing.T) {
	mock := newMock("brew", "sudo")
	mock.RespondOK("brew install", "done")

	brew := NewBrew(Options{Runner: mock, Elevated: false})
	// Homebrew refuses to run under sudo, so even an elevate request must not
	// produce one.
	if _, err := brew.Install(context.Background(), Request{Packages: []string{"git"}, Elevate: true}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if strings.HasPrefix(mock.Invocations()[0], "sudo") {
		t.Errorf("brew must never be run under sudo, got %q", mock.Invocations()[0])
	}
}

func TestBrewCask(t *testing.T) {
	mock := newMock("brew")
	mock.RespondOK("brew install --cask", "done")

	brew := NewBrew(Options{Runner: mock})
	if _, err := brew.Install(context.Background(), Request{Packages: []string{"visual-studio-code"}, Cask: true}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !strings.Contains(mock.Invocations()[0], "--cask") {
		t.Errorf("invocation = %q, want --cask", mock.Invocations()[0])
	}
}

func TestBrewFindsCaskOnlyPackages(t *testing.T) {
	mock := newMock("brew")
	mock.RespondFail("brew list --versions postman", 1, "Error: No such keg")
	mock.RespondOK("brew list --cask --versions postman", "postman 11.20.0")

	brew := NewBrew(Options{Runner: mock})
	if !brew.IsInstalled(context.Background(), "postman") {
		t.Error("a cask-only package should still be found")
	}
	if got := brew.Version(context.Background(), "postman"); got != "11.20.0" {
		t.Errorf("Version = %q, want 11.20.0", got)
	}
}

func TestSnapClassicConfinement(t *testing.T) {
	mock := newMock("snap", "sudo")
	mock.RespondOK("sudo -n snap install", "done")

	snap := NewSnap(Options{Runner: mock})
	if _, err := snap.Install(context.Background(), Request{Packages: []string{"code"}, Classic: true, Elevate: true}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !strings.Contains(mock.Invocations()[0], "--classic") {
		t.Errorf("invocation = %q, want --classic", mock.Invocations()[0])
	}
}

func TestSnapVersionSkipsTheHeaderRow(t *testing.T) {
	mock := newMock("snap")
	mock.RespondOK("snap list code",
		"Name  Version   Rev  Tracking       Publisher  Notes\ncode  1.97.2    180  latest/stable  vscode✓    classic")

	snap := NewSnap(Options{Runner: mock})
	if got := snap.Version(context.Background(), "code"); got != "1.97.2" {
		t.Errorf("Version = %q, want 1.97.2", got)
	}
}

// TestDryRunTouchesNothing is the safety guarantee behind --dry-run: no
// mutating command may reach the machine.
func TestDryRunTouchesNothing(t *testing.T) {
	mock := newMock("apt-get", "winget", "brew", "pacman", "zypper", "dnf", "snap", "sudo")
	options := Options{Runner: mock, DryRun: true}

	managers := []Manager{
		NewWinget(options), NewApt(options), NewDnf(options),
		NewPacman(options), NewZypper(options), NewBrew(options), NewSnap(options),
	}
	for _, manager := range managers {
		if _, err := manager.Install(context.Background(), Request{Packages: []string{"git"}, Elevate: true}); err != nil {
			t.Fatalf("%s Install in dry run: %v", manager.Name(), err)
		}
		if err := manager.Refresh(context.Background()); err != nil {
			t.Fatalf("%s Refresh in dry run: %v", manager.Name(), err)
		}
	}
	if len(mock.Calls) != 0 {
		t.Errorf("dry run executed %d commands, want 0: %v", len(mock.Calls), mock.Invocations())
	}
}

// TestDryRunStillAnswersQueries: the plan shown to the user has to be
// accurate, so read-only checks must still run.
func TestDryRunStillAnswersQueries(t *testing.T) {
	mock := newMock("apt-get", "dpkg-query")
	mock.RespondOK("dpkg-query -W -f=${Status} git", "install ok installed")

	apt := NewApt(Options{Runner: mock, DryRun: true})
	if !apt.IsInstalled(context.Background(), "git") {
		t.Error("presence checks must still run in dry-run mode")
	}
}

func TestSummariseFailureUsesTheLastMeaningfulLine(t *testing.T) {
	result := command.Result{
		ExitCode: 100,
		Stderr:   "Reading package lists...\nE: Unable to locate package ghost\n\n",
	}
	if got := summariseFailure(result); got != "E: Unable to locate package ghost" {
		t.Errorf("summariseFailure = %q", got)
	}
}
