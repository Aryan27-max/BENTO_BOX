package installer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Aryan27-max/bento-box/internal/command"
	"github.com/Aryan27-max/bento-box/internal/dependency"
	"github.com/Aryan27-max/bento-box/internal/environment"
	"github.com/Aryan27-max/bento-box/internal/paths"
	"github.com/Aryan27-max/bento-box/internal/pkgmanager"
)

// Execute carries out a plan and returns one outcome per dependency.
//
// A failure never takes the whole run down. An independent dependency that
// fails is recorded and the run continues; a dependency whose prerequisite
// failed is skipped with that reason, because attempting it would only produce
// a second, more confusing failure.
func (i *Installer) Execute(ctx context.Context, plan Plan) []dependency.Outcome {
	outcomes := make([]dependency.Outcome, 0, len(plan.Items))
	total := len(plan.Items)

	for index, item := range plan.Items {
		i.Progress.Start(index+1, total, item)
		i.log.Section(item.Spec.Name)

		started := time.Now()
		outcome := i.executeOne(ctx, item)
		outcome.SetDuration(time.Since(started))

		i.log.Printf("%s → %s (%s)", item.Spec.Name, outcome.Status, outcome.Version)
		i.Progress.Finish(outcome)
		outcomes = append(outcomes, outcome)
	}

	if err := i.Environment.Flush(); err != nil {
		i.log.Printf("failed to persist environment changes: %v", err)
	}
	return outcomes
}

func (i *Installer) executeOne(ctx context.Context, item Item) dependency.Outcome {
	outcome := dependency.Outcome{
		Name:            item.Spec.Name,
		DisplayName:     item.Spec.Label(),
		Category:        item.Spec.Category,
		Action:          item.Action,
		Reason:          item.Reason,
		Source:          item.Source,
		RestartRequired: item.RestartRequired,
	}
	if item.Step != nil {
		outcome.Method = string(item.Step.Method)
		outcome.URL = item.Step.URL
	}

	switch item.Action {
	case dependency.ActionUnsupported:
		outcome.Status = dependency.StatusUnsupported
		return outcome

	case dependency.ActionSkip:
		outcome.Status = dependency.StatusSkipped
		if item.Step != nil && item.Step.Method == dependency.MethodManual {
			outcome.URL = item.Step.URL
		}
		return outcome

	case dependency.ActionKeep:
		outcome.Status = dependency.StatusAlreadyInstalled
		outcome.Version = item.Current.VersionString()
		i.configure(ctx, item.Spec, &outcome)
		i.checkService(ctx, item.Spec, &outcome)
		return outcome
	}

	// A prerequisite that failed makes this dependency unattemptable.
	if blocker, reason := i.blockedBy(item.Spec); blocker != "" {
		outcome.Status = dependency.StatusSkipped
		outcome.Reason = fmt.Sprintf("skipped because %s could not be installed (%s)", blocker, reason)
		i.failed[item.Spec.Name] = "a prerequisite failed"
		return outcome
	}

	if i.DryRun {
		outcome.Status = dependency.StatusPlanned
		outcome.Reason = "dry run: nothing was installed"
		return outcome
	}

	i.install(ctx, item, &outcome)
	return outcome
}

func (i *Installer) blockedBy(spec dependency.Spec) (string, string) {
	for _, required := range spec.Requires {
		if reason, failed := i.failed[required]; failed {
			return required, reason
		}
	}
	return "", ""
}

// install runs the candidate steps in order until one produces a verified
// installation. Trying the next candidate when the first fails is what makes
// "snap, otherwise the vendor package, otherwise the distro package" work
// without any of that logic living in Go.
func (i *Installer) install(ctx context.Context, item Item, outcome *dependency.Outcome) {
	platform := item.Spec.Platforms[i.OS]
	previous := item.Current.VersionString()

	var attempts []string
	for index := range platform.Install {
		step := platform.Install[index]

		if !i.stepUsable(step) {
			continue
		}
		if step.Method == dependency.MethodManual {
			outcome.Status = dependency.StatusSkipped
			outcome.Reason = step.Reason
			outcome.URL = step.URL
			return
		}

		outcome.Method = string(step.Method)
		outcome.Source = step.Describe()

		i.Progress.Step("installing via " + step.Describe())
		err := i.runStep(ctx, item.Spec, step, outcome)
		if err != nil {
			attempts = append(attempts, fmt.Sprintf("%s: %v", step.Describe(), err))
			i.log.Printf("%s: %s failed: %v", item.Spec.Name, step.Describe(), err)
			continue
		}

		// Configure the environment before verifying: a tool installed into
		// Bento's own directory is only findable once its PATH entry exists.
		i.configure(ctx, item.Spec, outcome)

		i.Progress.Step("verifying")
		result := i.Verifier.Verify(ctx, item.Spec)
		if !result.Present {
			attempts = append(attempts, fmt.Sprintf("%s: installed but %s", step.Describe(), result.Problem))
			i.log.Printf("%s: %s reported success but verification failed: %s", item.Spec.Name, step.Describe(), result.Problem)
			continue
		}

		// An install that succeeds but delivers a version below the minimum
		// is not a success. Distribution packages of Node.js are the reason
		// this check exists: apt happily installs a release that is years old.
		if item.Spec.MinimumVersion != "" && result.VersionKnown && !result.Version.Satisfies(item.Spec.MinimumVersion) {
			attempts = append(attempts, fmt.Sprintf("%s: provided %s, which is older than the required %s",
				step.Describe(), result.Version, item.Spec.MinimumVersion))
			i.log.Printf("%s: %s gave %s, below the minimum %s; trying the next source",
				item.Spec.Name, step.Describe(), result.Version, item.Spec.MinimumVersion)
			continue
		}

		outcome.Version = result.VersionString()
		outcome.Components = result.Components
		switch {
		case item.Action == dependency.ActionUpgrade:
			outcome.Status = dependency.StatusUpdated
			outcome.PreviousVersion = previous
		default:
			outcome.Status = dependency.StatusInstalled
		}

		if missing := result.MissingComponents(); len(missing) > 0 {
			outcome.Status = dependency.StatusWarning
			outcome.AddWarning("these components are missing: " + strings.Join(missing, ", "))
		}
		if !result.VersionKnown && result.Problem != "" {
			outcome.AddWarning(result.Problem)
		}

		i.checkService(ctx, item.Spec, outcome)
		return
	}

	outcome.Status = dependency.StatusFailed
	for _, attempt := range attempts {
		outcome.AddError(attempt)
	}
	if len(attempts) == 0 {
		outcome.AddError("no installation method could be used on this machine")
	}
	i.failed[item.Spec.Name] = strings.Join(attempts, "; ")
	if !item.Spec.Required {
		// An optional dependency that failed is a warning about the machine,
		// not a broken environment.
		outcome.Status = dependency.StatusFailed
		outcome.Reason = "this dependency is optional"
	}
}

func (i *Installer) stepUsable(step dependency.Step) bool {
	switch step.Method {
	case dependency.MethodPackageManager, dependency.MethodLocalPackage:
		return i.Registry.Has(step.Manager)
	case dependency.MethodLanguagePackage:
		return i.languageToolAvailable(step.Via)
	case dependency.MethodCommand:
		return len(step.Command) > 0 && command.Available(i.Runner, step.Command[0])
	case dependency.MethodArchive, dependency.MethodInstaller:
		return step.Resolver != "" || step.URLFor(i.Arch) != ""
	default:
		return true
	}
}

func (i *Installer) runStep(ctx context.Context, spec dependency.Spec, step dependency.Step, outcome *dependency.Outcome) error {
	if err := i.runHooks(ctx, step.PreInstall, "pre-install"); err != nil {
		return err
	}

	var err error
	switch step.Method {
	case dependency.MethodPackageManager:
		err = i.installWithManager(ctx, step, outcome)
	case dependency.MethodLocalPackage:
		err = i.installLocalPackage(ctx, step)
	case dependency.MethodLanguagePackage:
		err = i.installLanguagePackage(ctx, step, outcome)
	case dependency.MethodArchive:
		err = i.installArchive(ctx, spec, step, outcome)
	case dependency.MethodInstaller:
		err = i.installVendorInstaller(ctx, step, outcome)
	case dependency.MethodCommand:
		err = i.runCommandStep(ctx, step)
	case dependency.MethodBundled:
		err = nil
	default:
		err = fmt.Errorf("unsupported installation method %q", step.Method)
	}
	if err != nil {
		return err
	}

	return i.runHooks(ctx, step.PostInstall, "post-install")
}

// runHooks executes the commands a dependency declares around its install,
// such as tapping a Homebrew repository or adding Rust components.
func (i *Installer) runHooks(ctx context.Context, hooks [][]string, phase string) error {
	for _, hook := range hooks {
		if len(hook) == 0 {
			continue
		}
		name := hook[0]
		resolved, err := i.Runner.LookPath(name)
		if err != nil {
			if found, ok := i.findOnBentoPath(name); ok {
				resolved = found
			} else {
				return fmt.Errorf("%s command %q is not available", phase, name)
			}
		}

		i.Progress.Step(phase + ": " + strings.Join(hook, " "))
		result, err := i.Runner.Run(ctx, command.Spec{
			Name: resolved, Args: hook[1:], Env: i.commandEnv(),
		})
		if err != nil {
			return fmt.Errorf("%s %q: %w", phase, strings.Join(hook, " "), err)
		}
		if !result.Success() {
			return fmt.Errorf("%s %q exited with code %d: %s",
				phase, strings.Join(hook, " "), result.ExitCode, lastLine(result.Stderr))
		}
	}
	return nil
}

func (i *Installer) installWithManager(ctx context.Context, step dependency.Step, outcome *dependency.Outcome) error {
	manager, ok := i.Registry.Get(step.Manager)
	if !ok {
		return fmt.Errorf("%s is not available", step.Manager)
	}
	if err := manager.Refresh(ctx); err != nil {
		// A failed index refresh is worth recording but not fatal: the
		// install may still succeed from a cached index.
		i.log.Printf("%s refresh failed: %v", step.Manager, err)
		outcome.AddWarning(fmt.Sprintf("%s could not refresh its package index", step.Manager))
	}

	request := pkgmanager.Request{
		Packages: step.Packages, Cask: step.Cask, Classic: step.Classic,
		ExtraArgs: step.Args, Elevate: step.Elevate,
	}
	result, err := manager.Install(ctx, request)
	if result.Command != "" {
		i.log.Printf("%s", result.Command)
	}
	if err != nil {
		if result.Message != "" {
			return fmt.Errorf("%s", result.Message)
		}
		return err
	}
	return nil
}

func (i *Installer) installLocalPackage(ctx context.Context, step dependency.Step) error {
	manager, ok := i.Registry.Get(step.Manager)
	if !ok {
		return fmt.Errorf("%s is not available", step.Manager)
	}

	url := step.URLFor(i.Arch)
	if url == "" {
		return fmt.Errorf("no download is published for %s", i.Arch)
	}

	i.Progress.Step("downloading the official package")
	path, err := i.fetch(ctx, Download{URL: url, SHA256: step.Checksum}, i.downloadDir())
	if err != nil {
		return err
	}
	defer os.Remove(path)

	result, err := manager.InstallLocal(ctx, path, step.Elevate)
	if err != nil {
		if result.Message != "" {
			return fmt.Errorf("%s", result.Message)
		}
		return err
	}
	return nil
}

// installArchive downloads an official archive, verifies its checksum, unpacks
// it under Bento's home and puts its bin directory on PATH.
func (i *Installer) installArchive(ctx context.Context, spec dependency.Spec, step dependency.Step, outcome *dependency.Outcome) error {
	download, err := i.resolveDownload(ctx, step)
	if err != nil {
		return err
	}

	i.Progress.Step("downloading " + download.URL)
	archive, err := i.fetch(ctx, download, i.downloadDir())
	if err != nil {
		return err
	}
	defer os.Remove(archive)

	name := step.InstallDir
	if name == "" {
		name = spec.Name
	}
	target := filepath.Join(paths.OptDir(i.Home), name)

	// Replace any previous unpack so an upgrade does not merge two versions.
	if err := os.RemoveAll(target); err != nil {
		return fmt.Errorf("clearing %s: %w", target, err)
	}

	i.Progress.Step("unpacking")
	root, err := i.extract(ctx, archive, target)
	if err != nil {
		return err
	}

	binDir := filepath.Join(root, "bin")
	if _, err := os.Stat(binDir); err != nil {
		// Some archives put their executables at the top level.
		binDir = root
	}
	change := i.Environment.AddPath(binDir)
	if change.Applied || change.Reason == "already on PATH" {
		outcome.Environment = append(outcome.Environment, change.String())
		i.refreshVerifierPath()
	}
	if download.Version != "" {
		i.log.Printf("%s: unpacked version %s from %s", spec.Name, download.Version, download.URL)
	}
	return nil
}

// installVendorInstaller downloads an official installer binary and runs it
// unattended. rustup is the reason this exists: it has no good distribution
// package, and piping its shell script into a shell is exactly the
// supply-chain shortcut Bento refuses to take.
func (i *Installer) installVendorInstaller(ctx context.Context, step dependency.Step, outcome *dependency.Outcome) error {
	download, err := i.resolveDownload(ctx, step)
	if err != nil {
		return err
	}

	i.Progress.Step("downloading " + download.URL)
	path, err := i.fetch(ctx, download, i.downloadDir())
	if err != nil {
		return err
	}
	defer os.Remove(path)

	if i.OS != "windows" {
		if err := os.Chmod(path, 0o755); err != nil {
			return fmt.Errorf("making the installer executable: %w", err)
		}
	}

	i.Progress.Step("running the official installer")
	result, err := i.Runner.Run(ctx, command.Spec{
		Name: path, Args: step.InstallerArgs, Env: i.commandEnv(),
	})
	if err != nil {
		return err
	}
	if !result.Success() {
		return fmt.Errorf("the installer exited with code %d: %s", result.ExitCode, lastLine(result.Stderr))
	}
	_ = outcome
	return nil
}

func (i *Installer) resolveDownload(ctx context.Context, step dependency.Step) (Download, error) {
	if step.Resolver != "" {
		i.Progress.Step("asking the vendor for the current stable release")
		download, err := i.resolve(ctx, step.Resolver)
		if err != nil {
			return Download{}, err
		}
		return download, nil
	}

	url := step.URLFor(i.Arch)
	if url == "" {
		return Download{}, fmt.Errorf("no download is published for %s", i.Arch)
	}
	return Download{URL: url, SHA256: step.Checksum}, nil
}

func (i *Installer) runCommandStep(ctx context.Context, step dependency.Step) error {
	name, err := i.Runner.LookPath(step.Command[0])
	if err != nil {
		return fmt.Errorf("%s is not available", step.Command[0])
	}
	result, err := i.Runner.Run(ctx, command.Spec{Name: name, Args: step.Command[1:], Env: i.commandEnv()})
	if err != nil {
		return err
	}
	if !result.Success() {
		return fmt.Errorf("%s exited with code %d: %s",
			strings.Join(step.Command, " "), result.ExitCode, lastLine(result.Stderr))
	}
	return nil
}

func (i *Installer) downloadDir() string {
	return filepath.Join(paths.Home(i.Home), "downloads")
}

// configure applies the environment declarations for a dependency and records
// what changed.
func (i *Installer) configure(ctx context.Context, spec dependency.Spec, outcome *dependency.Outcome) {
	applier := &environment.Applier{
		Manager: i.Environment, Runner: i.Runner, OS: i.OS, Home: i.Home,
		ExtraPath: i.Environment.AddedPath(),
	}

	changes := applier.Apply(ctx, spec)
	if len(changes) == 0 {
		return
	}
	i.Progress.Step("configuring the environment")

	for _, change := range changes {
		switch {
		case change.Applied:
			outcome.Environment = append(outcome.Environment, change.String())
		case change.Reason == "already set" || change.Reason == "already on PATH":
			outcome.Environment = append(outcome.Environment, change.String()+" (already configured)")
		case i.DryRun:
			// Not writing anything is the whole point of a dry run, so it is
			// not worth warning about.
		default:
			outcome.AddWarning(fmt.Sprintf("%s was not configured: %s", change.String(), change.Reason))
		}
	}
	i.refreshVerifierPath()
}

// refreshVerifierPath lets verification see directories added during this run.
func (i *Installer) refreshVerifierPath() {
	windows := i.OS == "windows"
	i.Verifier.ExtraPath = mergeUnique(i.Verifier.ExtraPath, i.Environment.AddedPath(), windows)
	if i.npmPrefix != "" {
		i.Verifier.ExtraPath = mergeUnique(i.Verifier.ExtraPath, []string{i.npmBinDir()}, windows)
	}
}

// checkService observes a dependency's service, and starts it only when the
// catalog marks it as genuinely required.
func (i *Installer) checkService(ctx context.Context, spec dependency.Spec, outcome *dependency.Outcome) {
	service := spec.ServiceFor(i.OS)
	if service == nil {
		return
	}

	status := i.Services.Status(ctx, service)
	if status == nil {
		return
	}

	if service.Required && status.State == dependency.ServiceStopped {
		i.Progress.Step("starting " + status.Name)
		state, err := i.Services.Start(ctx, service)
		status.State = state
		if err != nil {
			outcome.AddWarning(fmt.Sprintf("%s could not be started: %v", status.Name, err))
		}
	}
	outcome.Service = status
}

func (i *Installer) commandEnv() []string {
	added := i.Environment.AddedPath()
	if i.npmPrefix != "" {
		added = mergeUnique(added, []string{i.npmBinDir()}, i.OS == "windows")
	}
	if len(added) == 0 {
		return nil
	}
	separator := string(os.PathListSeparator)
	return []string{"PATH=" + strings.Join(added, separator) + separator + os.Getenv("PATH")}
}

func (i *Installer) findOnBentoPath(name string) (string, bool) {
	candidates := []string{name}
	if i.OS == "windows" {
		candidates = []string{name + ".exe", name + ".cmd", name + ".bat", name}
	}
	for _, dir := range i.Environment.AddedPath() {
		for _, candidate := range candidates {
			full := filepath.Join(dir, candidate)
			if _, err := os.Stat(full); err == nil {
				return full, true
			}
		}
	}
	return "", false
}

// mergeUnique appends directories that are not already in the list. It answers
// "is this the same directory?" the same way the environment writer does —
// case-insensitively on Windows — so that a directory recorded as C:\Go\bin is
// not added a second time as c:\go\bin.
func mergeUnique(existing, additions []string, windows bool) []string {
	for _, addition := range additions {
		found := false
		for _, entry := range existing {
			if paths.SameEntry(entry, addition, windows) {
				found = true
				break
			}
		}
		if !found {
			existing = append(existing, addition)
		}
	}
	return existing
}

func lastLine(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		if line := strings.TrimSpace(lines[index]); line != "" {
			if len(line) > 300 {
				return line[:300] + "…"
			}
			return line
		}
	}
	return ""
}
