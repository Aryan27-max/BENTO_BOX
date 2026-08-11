package installer

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Aryan27-max/bento-box/internal/catalog"
	"github.com/Aryan27-max/bento-box/internal/command"
	"github.com/Aryan27-max/bento-box/internal/dependency"
	"github.com/Aryan27-max/bento-box/internal/detector"
	"github.com/Aryan27-max/bento-box/internal/environment"
	"github.com/Aryan27-max/bento-box/internal/pkgmanager"
	"github.com/Aryan27-max/bento-box/internal/resolver"
	"github.com/Aryan27-max/bento-box/internal/services"
	"github.com/Aryan27-max/bento-box/internal/verifier"
)

// fakeManager is a package manager that records what it was asked to do and
// can be told to fail, so the pipeline's decisions can be tested without any
// software being installed.
type fakeManager struct {
	name      string
	installed map[string]bool
	fail      bool
	calls     []string
	refreshes int
}

func newFakeManager(name string) *fakeManager {
	return &fakeManager{name: name, installed: map[string]bool{}}
}

func (f *fakeManager) Name() string      { return f.name }
func (f *fakeManager) IsAvailable() bool { return true }

func (f *fakeManager) Refresh(context.Context) error {
	f.refreshes++
	return nil
}

func (f *fakeManager) Install(_ context.Context, request pkgmanager.Request) (pkgmanager.Result, error) {
	f.calls = append(f.calls, f.name+" install "+strings.Join(request.Packages, " "))
	if f.fail {
		return pkgmanager.Result{Message: "package not found"}, errInstall
	}
	for _, pkg := range request.Packages {
		f.installed[pkg] = true
	}
	return pkgmanager.Result{Success: true}, nil
}

func (f *fakeManager) Upgrade(ctx context.Context, request pkgmanager.Request) (pkgmanager.Result, error) {
	return f.Install(ctx, request)
}

func (f *fakeManager) IsInstalled(_ context.Context, pkg string) bool { return f.installed[pkg] }
func (f *fakeManager) Version(context.Context, string) string         { return "" }

func (f *fakeManager) InstallLocal(_ context.Context, path string, _ bool) (pkgmanager.Result, error) {
	f.calls = append(f.calls, f.name+" install-local "+path)
	if f.fail {
		return pkgmanager.Result{Message: "could not install"}, errInstall
	}
	return pkgmanager.Result{Success: true}, nil
}

type installError struct{}

func (installError) Error() string { return "install failed" }

var errInstall = installError{}

// harness wires a full installer against mocks and a temporary home.
type harness struct {
	installer *Installer
	runner    *command.Mock
	managers  map[string]*fakeManager
	catalog   *catalog.Catalog
	system    detector.System
	home      string
}

func newHarness(t *testing.T, deps string, managerNames ...string) *harness {
	t.Helper()

	loaded, err := catalog.LoadFS(fstest.MapFS{
		"profiles.json":          {Data: []byte(`[{"id":"core","name":"Core","order":0},{"id":"web","name":"Web","order":1}]`)},
		"dependencies/test.json": {Data: []byte(deps)},
	})
	if err != nil {
		t.Fatalf("LoadFS: %v", err)
	}

	home := t.TempDir()
	runner := command.NewMock()
	system := detector.System{OS: "linux", Arch: "amd64", Home: home, OSName: "Test Linux"}

	registry := pkgmanager.NewEmptyRegistry()
	managers := map[string]*fakeManager{}
	for _, name := range managerNames {
		manager := newFakeManager(name)
		managers[name] = manager
		registry.Register(manager)
	}

	check := verifier.New(runner, "linux", home)
	check.Getenv = func(string) string { return "" }

	installer := New(Options{
		Runner:      runner,
		Registry:    registry,
		Verifier:    check,
		Environment: environment.New("linux", home, filepath.Join(home, ".bashrc"), "bash", false),
		Services:    services.New(runner, "linux", false),
		System:      system,
	})

	return &harness{installer: installer, runner: runner, managers: managers, catalog: loaded, system: system, home: home}
}

func (h *harness) run(t *testing.T) []dependency.Outcome {
	t.Helper()
	resolution, err := resolver.Resolve(resolver.Request{
		Catalog: h.catalog, Profile: "web", OS: "linux", Arch: "amd64",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	plan := h.installer.BuildPlan(context.Background(), resolution, h.system)
	return h.installer.Execute(context.Background(), plan)
}

// appearsAfterInstall models a real machine: the tool is nowhere to be found
// until the package manager has actually installed it, and only then does
// verification start succeeding.
func (h *harness) appearsAfterInstall(managerName, executable, path, commandPrefix, output string) {
	base := h.managers[managerName]
	h.installer.Registry.Register(&switchingManager{fakeManager: base, onInstall: func() {
		h.runner.AddPath(executable, path)
		h.runner.RespondOK(commandPrefix, output)
	}})
}

func (h *harness) plan(t *testing.T) Plan {
	t.Helper()
	resolution, err := resolver.Resolve(resolver.Request{
		Catalog: h.catalog, Profile: "web", OS: "linux", Arch: "amd64",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return h.installer.BuildPlan(context.Background(), resolution, h.system)
}

func outcomeFor(outcomes []dependency.Outcome, name string) (dependency.Outcome, bool) {
	for _, outcome := range outcomes {
		if outcome.Name == name {
			return outcome, true
		}
	}
	return dependency.Outcome{}, false
}

const simpleTool = `[{
	"name":"widget","display_name":"Widget","category":"CLI_TOOL","profiles":["web"],
	"verify":{"command":"widget","args":["--version"]},
	"platforms":{"linux":{"install":[{"method":"package_manager","manager":"apt","packages":["widget"]}]}}
}]`

// TestAlreadyInstalledIsLeftAlone is the idempotence guarantee: running Bento
// on a machine that already has everything must change nothing.
func TestAlreadyInstalledIsLeftAlone(t *testing.T) {
	h := newHarness(t, simpleTool, "apt")
	h.runner.AddPath("widget", "/usr/bin/widget")
	h.runner.RespondOK("/usr/bin/widget --version", "widget 3.2.1")

	plan := h.plan(t)
	if len(plan.ToInstall()) != 0 {
		t.Errorf("plan wants to install %d things on an already-configured machine", len(plan.ToInstall()))
	}
	if !plan.ChangesNothing() {
		t.Error("ChangesNothing should be true")
	}

	outcomes := h.installer.Execute(context.Background(), plan)
	outcome, _ := outcomeFor(outcomes, "widget")
	if outcome.Status != dependency.StatusAlreadyInstalled {
		t.Errorf("status = %s, want ALREADY_INSTALLED", outcome.Status)
	}
	if outcome.Version != "3.2.1" {
		t.Errorf("version = %q, want the observed 3.2.1", outcome.Version)
	}
	if len(h.managers["apt"].calls) != 0 {
		t.Errorf("nothing should have been installed, but apt was called: %v", h.managers["apt"].calls)
	}
}

func TestMissingDependencyIsInstalledAndVerified(t *testing.T) {
	h := newHarness(t, simpleTool, "apt")

	plan := h.plan(t)
	if len(plan.ToInstall()) != 1 {
		t.Fatalf("plan should install exactly one thing, got %d", len(plan.ToInstall()))
	}

	// The tool appears once the package manager has installed it.
	h.runner.AddPath("widget", "/usr/bin/widget")
	h.runner.RespondOK("/usr/bin/widget --version", "widget 3.2.1")

	outcomes := h.installer.Execute(context.Background(), plan)
	outcome, _ := outcomeFor(outcomes, "widget")
	if outcome.Status != dependency.StatusInstalled {
		t.Errorf("status = %s (%v), want INSTALLED", outcome.Status, outcome.Errors)
	}
	if outcome.Version != "3.2.1" {
		t.Errorf("version = %q", outcome.Version)
	}
	if !h.managers["apt"].installed["widget"] {
		t.Error("apt was never asked to install widget")
	}
}

// TestInstallThatCannotBeVerifiedIsAFailure: a package manager reporting
// success is not proof. Bento only calls something installed once it has seen
// the tool run.
func TestInstallThatCannotBeVerifiedIsAFailure(t *testing.T) {
	h := newHarness(t, simpleTool, "apt")

	outcomes := h.run(t)
	outcome, _ := outcomeFor(outcomes, "widget")
	if outcome.Status != dependency.StatusFailed {
		t.Errorf("status = %s, want FAILED when verification cannot find the tool", outcome.Status)
	}
	if len(outcome.Errors) == 0 {
		t.Error("a failure should explain itself")
	}
}

const versionedTool = `[{
	"name":"node","display_name":"Node.js","category":"RUNTIME","profiles":["web"],
	"minimum_version":"20.0.0",
	"verify":{"command":"node","args":["--version"]},
	"platforms":{"linux":{"install":[
		{"method":"package_manager","manager":"apt","packages":["nodejs"]},
		{"method":"package_manager","manager":"snap","packages":["node"]}
	]}}
}]`

func TestOldVersionIsUpgraded(t *testing.T) {
	h := newHarness(t, versionedTool, "apt", "snap")
	h.runner.AddPath("node", "/usr/bin/node")
	h.runner.RespondOK("/usr/bin/node --version", "v18.20.5")

	plan := h.plan(t)
	item := plan.ToInstall()
	if len(item) != 1 || item[0].Action != dependency.ActionUpgrade {
		t.Fatalf("plan = %+v, want a single upgrade", item)
	}
	if !strings.Contains(item[0].Reason, "older than") {
		t.Errorf("Reason = %q, want an explanation of the version gap", item[0].Reason)
	}

	// After the upgrade the machine reports a satisfactory version.
	h.runner.RespondOK("/usr/bin/node --version", "v22.14.0")

	outcomes := h.installer.Execute(context.Background(), plan)
	outcome, _ := outcomeFor(outcomes, "node")
	if outcome.Status != dependency.StatusUpdated {
		t.Errorf("status = %s, want UPDATED", outcome.Status)
	}
	if outcome.PreviousVersion != "18.20.5" {
		t.Errorf("PreviousVersion = %q, want 18.20.5", outcome.PreviousVersion)
	}
	if outcome.Version != "22.14.0" {
		t.Errorf("Version = %q, want 22.14.0", outcome.Version)
	}
}

// TestSourceBelowMinimumFallsThroughToTheNextOne is the Node-on-Debian case:
// apt installs a release that is years old, so Bento moves on to the next
// source rather than declaring victory.
func TestSourceBelowMinimumFallsThroughToTheNextOne(t *testing.T) {
	h := newHarness(t, versionedTool, "apt", "snap")
	h.runner.AddPath("node", "/usr/bin/node")

	// apt delivers 18.x, which does not satisfy the minimum. snap delivers a
	// current release. The mock switches its answer once snap has run.
	h.runner.RespondOK("/usr/bin/node --version", "v18.20.5")

	plan := h.plan(t)
	snap := h.managers["snap"]
	apt := h.managers["apt"]

	// Installing through snap makes the machine report a current release,
	// exactly as a real install would.
	h.appearsAfterInstall("snap", "node", "/usr/bin/node", "/usr/bin/node --version", "v22.14.0")

	outcomes := h.installer.Execute(context.Background(), plan)
	outcome, _ := outcomeFor(outcomes, "node")

	// Node was already present at 18.x, so a successful run is an upgrade.
	if outcome.Status != dependency.StatusUpdated {
		t.Fatalf("status = %s (%v), want UPDATED via the fallback source", outcome.Status, outcome.Errors)
	}
	if outcome.Version != "22.14.0" {
		t.Errorf("version = %q, want the version from the fallback source", outcome.Version)
	}
	if len(apt.calls) == 0 {
		t.Error("the first source should have been tried first")
	}
	if len(snap.calls) == 0 {
		t.Error("the fallback source should have been used")
	}
}

// switchingManager runs a callback when an install succeeds, letting a test
// model the machine actually changing.
type switchingManager struct {
	*fakeManager
	onInstall func()
}

func (s *switchingManager) Install(ctx context.Context, request pkgmanager.Request) (pkgmanager.Result, error) {
	result, err := s.fakeManager.Install(ctx, request)
	if err == nil && s.onInstall != nil {
		s.onInstall()
	}
	return result, err
}

func TestFirstSourceFailureFallsThrough(t *testing.T) {
	h := newHarness(t, versionedTool, "apt", "snap")
	h.managers["apt"].fail = true
	h.appearsAfterInstall("snap", "node", "/usr/bin/node", "/usr/bin/node --version", "v22.14.0")

	outcomes := h.run(t)
	outcome, _ := outcomeFor(outcomes, "node")
	if outcome.Status != dependency.StatusInstalled {
		t.Errorf("status = %s (%v), want INSTALLED after falling back", outcome.Status, outcome.Errors)
	}
	if len(h.managers["snap"].calls) == 0 {
		t.Error("the second source was never tried")
	}
}

const dependentTools = `[
	{"name":"node","display_name":"Node.js","category":"RUNTIME","profiles":["web"],
	 "verify":{"command":"node","args":["--version"]},
	 "platforms":{"linux":{"install":[{"method":"package_manager","manager":"apt","packages":["nodejs"]}]}}},
	{"name":"pnpm","display_name":"pnpm","category":"PACKAGE_MANAGER","profiles":["web"],"requires":["node"],
	 "verify":{"command":"pnpm","args":["--version"]},
	 "platforms":{"linux":{"install":[{"method":"package_manager","manager":"apt","packages":["pnpm"]}]}}}
]`

// TestDependentsOfAFailureAreSkippedNotFailed: one broken dependency must not
// produce a cascade of confusing failures.
func TestDependentsOfAFailureAreSkippedNotFailed(t *testing.T) {
	h := newHarness(t, dependentTools, "apt")
	h.managers["apt"].fail = true

	outcomes := h.run(t)

	node, _ := outcomeFor(outcomes, "node")
	if node.Status != dependency.StatusFailed {
		t.Errorf("node status = %s, want FAILED", node.Status)
	}

	pnpm, _ := outcomeFor(outcomes, "pnpm")
	if pnpm.Status != dependency.StatusSkipped {
		t.Errorf("pnpm status = %s, want SKIPPED", pnpm.Status)
	}
	if !strings.Contains(pnpm.Reason, "node") {
		t.Errorf("pnpm reason = %q, want it to name the failed prerequisite", pnpm.Reason)
	}
}

// TestOneFailureDoesNotStopIndependentWork: the run continues past a failure.
func TestOneFailureDoesNotStopIndependentWork(t *testing.T) {
	deps := `[
		{"name":"broken","category":"CLI_TOOL","profiles":["web"],
		 "verify":{"command":"broken"},
		 "platforms":{"linux":{"install":[{"method":"package_manager","manager":"apt","packages":["broken"]}]}}},
		{"name":"working","category":"CLI_TOOL","profiles":["web"],
		 "verify":{"command":"working","args":["--version"]},
		 "platforms":{"linux":{"install":[{"method":"package_manager","manager":"snap","packages":["working"]}]}}}
	]`

	h := newHarness(t, deps, "apt", "snap")
	h.managers["apt"].fail = true
	h.appearsAfterInstall("snap", "working", "/usr/bin/working", "/usr/bin/working --version", "working 1.0.0")

	outcomes := h.run(t)

	broken, _ := outcomeFor(outcomes, "broken")
	if broken.Status != dependency.StatusFailed {
		t.Errorf("broken status = %s, want FAILED", broken.Status)
	}
	working, _ := outcomeFor(outcomes, "working")
	if working.Status != dependency.StatusInstalled {
		t.Errorf("working status = %s (%v), want INSTALLED despite the other failure", working.Status, working.Errors)
	}
}

func TestUnsupportedIsReportedNotAttempted(t *testing.T) {
	deps := `[{
		"name":"redis","display_name":"Redis","category":"DATABASE","profiles":["web"],
		"verify":{"command":"redis-cli"},
		"unsupported":{"linux":"Redis is unavailable in this fictional test universe."},
		"platforms":{"windows":{"install":[{"method":"package_manager","manager":"winget","packages":["redis"]}]}}
	}]`

	h := newHarness(t, deps, "apt")
	outcomes := h.run(t)

	outcome, _ := outcomeFor(outcomes, "redis")
	if outcome.Status != dependency.StatusUnsupported {
		t.Errorf("status = %s, want UNSUPPORTED", outcome.Status)
	}
	if !strings.Contains(outcome.Reason, "fictional test universe") {
		t.Errorf("reason = %q, want the catalog's explanation", outcome.Reason)
	}
	if len(h.managers["apt"].calls) != 0 {
		t.Error("an unsupported dependency must not be installed")
	}
}

func TestManualDependencyIsSkippedWithInstructions(t *testing.T) {
	deps := `[{
		"name":"compass","display_name":"Compass","category":"GUI_APPLICATION","profiles":["web"],
		"verify":{"skip_version":true,"paths":{"linux":["/opt/compass"]}},
		"platforms":{"linux":{"install":[{"method":"manual","reason":"Compass has no stable download URL.","url":"https://example.com/compass"}]}}
	}]`

	h := newHarness(t, deps, "apt")
	outcomes := h.run(t)

	outcome, _ := outcomeFor(outcomes, "compass")
	if outcome.Status != dependency.StatusSkipped {
		t.Errorf("status = %s, want SKIPPED", outcome.Status)
	}
	if outcome.Reason != "Compass has no stable download URL." {
		t.Errorf("reason = %q", outcome.Reason)
	}
	if outcome.URL != "https://example.com/compass" {
		t.Errorf("url = %q, want the official instructions", outcome.URL)
	}
}

func TestDependencyWithNoUsableSourceIsSkippedWithAReason(t *testing.T) {
	// The catalog only offers dnf, and this machine has apt.
	deps := `[{
		"name":"tool","category":"CLI_TOOL","profiles":["web"],
		"verify":{"command":"tool"},
		"platforms":{"linux":{"install":[{"method":"package_manager","manager":"dnf","packages":["tool"]}]}}
	}]`

	h := newHarness(t, deps, "apt")
	outcomes := h.run(t)

	outcome, _ := outcomeFor(outcomes, "tool")
	if outcome.Status != dependency.StatusSkipped {
		t.Errorf("status = %s, want SKIPPED", outcome.Status)
	}
	if !strings.Contains(outcome.Reason, "dnf") {
		t.Errorf("reason = %q, want it to name the missing package manager", outcome.Reason)
	}
}

// TestDryRunChangesNothing is the safety promise of --dry-run.
func TestDryRunChangesNothing(t *testing.T) {
	h := newHarness(t, simpleTool, "apt")
	h.installer.DryRun = true

	outcomes := h.run(t)
	outcome, _ := outcomeFor(outcomes, "widget")
	if outcome.Status != dependency.StatusPlanned {
		t.Errorf("status = %s, want PLANNED in a dry run", outcome.Status)
	}
	if len(h.managers["apt"].calls) != 0 {
		t.Errorf("dry run executed installs: %v", h.managers["apt"].calls)
	}
}

// TestServiceStateIsSeparateFromInstallation: a database that is installed but
// not running is still a successful installation.
func TestServiceStateIsSeparateFromInstallation(t *testing.T) {
	deps := `[{
		"name":"postgresql","display_name":"PostgreSQL","category":"DATABASE","profiles":["web"],
		"verify":{"command":"psql","args":["--version"]},
		"service":{"names":{"linux":"postgresql"}},
		"platforms":{"linux":{"install":[{"method":"package_manager","manager":"apt","packages":["postgresql"]}]}}
	}]`

	h := newHarness(t, deps, "apt")
	h.appearsAfterInstall("apt", "psql", "/usr/bin/psql", "/usr/bin/psql --version", "psql (PostgreSQL) 17.4")
	h.runner.AddPath("systemctl", "/usr/bin/systemctl")
	h.runner.Respond("systemctl is-active postgresql", command.Result{Stdout: "inactive", ExitCode: 3})

	outcomes := h.run(t)
	outcome, _ := outcomeFor(outcomes, "postgresql")

	if outcome.Status != dependency.StatusInstalled {
		t.Errorf("status = %s, want INSTALLED even though the service is stopped", outcome.Status)
	}
	if outcome.Service == nil {
		t.Fatal("the service state should be reported")
	}
	if outcome.Service.State != dependency.ServiceStopped {
		t.Errorf("service state = %s, want STOPPED", outcome.Service.State)
	}
	if h.runner.Ran("systemctl start") {
		t.Error("Bento must not start a service that no profile requires")
	}
}

func TestRequiredServiceIsStarted(t *testing.T) {
	deps := `[{
		"name":"postgresql","display_name":"PostgreSQL","category":"DATABASE","profiles":["web"],
		"verify":{"command":"psql","args":["--version"]},
		"service":{"names":{"linux":"postgresql"},"required":true},
		"platforms":{"linux":{"install":[{"method":"package_manager","manager":"apt","packages":["postgresql"]}]}}
	}]`

	h := newHarness(t, deps, "apt")
	h.appearsAfterInstall("apt", "psql", "/usr/bin/psql", "/usr/bin/psql --version", "psql (PostgreSQL) 17.4")
	h.runner.AddPath("systemctl", "/usr/bin/systemctl")
	h.runner.AddPath("sudo", "/usr/bin/sudo")
	h.runner.Respond("systemctl is-active postgresql", command.Result{Stdout: "inactive", ExitCode: 3})
	h.runner.RespondOK("sudo -n systemctl start postgresql", "")

	h.run(t)
	if !h.runner.Ran("sudo -n systemctl start postgresql") {
		t.Errorf("a required service should be started, calls: %v", h.runner.Invocations())
	}
}

// TestPlanIsBuiltBeforeAnythingIsTouched: building a plan must be read-only.
func TestPlanIsBuiltBeforeAnythingIsTouched(t *testing.T) {
	h := newHarness(t, simpleTool, "apt")
	h.plan(t)

	if len(h.managers["apt"].calls) != 0 {
		t.Errorf("planning executed installs: %v", h.managers["apt"].calls)
	}
	// Planning only ever runs verification probes, never a mutating command.
	for _, invocation := range h.runner.Invocations() {
		if strings.Contains(invocation, "install") {
			t.Errorf("planning ran a mutating command: %q", invocation)
		}
	}
}

func TestElevationIsDeclaredInThePlan(t *testing.T) {
	deps := `[{
		"name":"tool","category":"CLI_TOOL","profiles":["web"],
		"verify":{"command":"tool"},
		"platforms":{"linux":{"elevate":true,"install":[{"method":"package_manager","manager":"apt","packages":["tool"],"elevate":true}]}}
	}]`

	h := newHarness(t, deps, "apt")
	plan := h.plan(t)
	if !plan.NeedsElevation() {
		t.Error("a plan containing a privileged install should say so up front")
	}
}
