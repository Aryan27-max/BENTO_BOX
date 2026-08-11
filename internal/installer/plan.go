// Package installer turns a resolved dependency list into a plan, shows that
// plan for confirmation, and then carries it out.
//
// The pipeline for every dependency is the same:
//
//	detect → installed? → version check → satisfies requirement?
//	       → keep or install/upgrade → configure → verify → status
//
// It is idempotent by construction: the detect step runs first, so a second
// Bento run on the same machine keeps everything it finds and changes nothing.
package installer

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/Aryan27-max/bento-box/internal/command"
	"github.com/Aryan27-max/bento-box/internal/dependency"
	"github.com/Aryan27-max/bento-box/internal/detector"
	"github.com/Aryan27-max/bento-box/internal/environment"
	"github.com/Aryan27-max/bento-box/internal/logging"
	"github.com/Aryan27-max/bento-box/internal/pkgmanager"
	"github.com/Aryan27-max/bento-box/internal/profiles"
	"github.com/Aryan27-max/bento-box/internal/resolver"
	"github.com/Aryan27-max/bento-box/internal/services"
	"github.com/Aryan27-max/bento-box/internal/verifier"
)

// Item is one dependency in a plan, with the decision already made.
type Item struct {
	Spec   dependency.Spec
	Action dependency.Action
	// Reason explains skips, unsupported platforms and upgrades.
	Reason string
	// Current is what verification found before anything was changed.
	Current verifier.Result
	// Step is the installation candidate Bento intends to use. It is nil when
	// no action will be taken.
	Step *dependency.Step
	// Source is a human-readable description of where the software will come
	// from, shown in the plan so the user can see it before agreeing.
	Source string
	// Elevate reports that this step needs administrator or root privileges.
	Elevate bool
	// RestartRequired reports that the install only takes effect after a reboot.
	RestartRequired bool
}

// Label returns the dependency's display name.
func (i Item) Label() string { return i.Spec.Label() }

// Plan is everything Bento intends to do, decided before the machine is
// touched.
type Plan struct {
	Profile profiles.Profile
	System  detector.System
	Items   []Item
	DryRun  bool
}

// Select returns the plan items with a given action.
func (p Plan) Select(action dependency.Action) []Item {
	var out []Item
	for _, item := range p.Items {
		if item.Action == action {
			out = append(out, item)
		}
	}
	return out
}

// ToInstall returns everything that will be installed or upgraded.
func (p Plan) ToInstall() []Item {
	return append(p.Select(dependency.ActionInstall), p.Select(dependency.ActionUpgrade)...)
}

// AlreadySatisfied returns dependencies that are present and good enough.
func (p Plan) AlreadySatisfied() []Item { return p.Select(dependency.ActionKeep) }

// Unsupported returns dependencies this platform cannot have.
func (p Plan) Unsupported() []Item { return p.Select(dependency.ActionUnsupported) }

// Skipped returns dependencies Bento will deliberately not touch.
func (p Plan) Skipped() []Item { return p.Select(dependency.ActionSkip) }

// ChangesNothing reports whether the machine is already in the desired state.
func (p Plan) ChangesNothing() bool { return len(p.ToInstall()) == 0 }

// NeedsElevation reports whether any planned step requires administrator or
// root privileges, so the user can be told before they agree rather than
// after a password prompt appears.
func (p Plan) NeedsElevation() bool {
	for _, item := range p.ToInstall() {
		if item.Elevate {
			return true
		}
	}
	return false
}

// Installer carries out plans.
type Installer struct {
	Runner      command.Runner
	Registry    *pkgmanager.Registry
	Verifier    *verifier.Verifier
	Environment *environment.Manager
	Services    *services.Manager

	OS   string
	Arch string
	Home string
	// Elevated reports whether Bento is already running with privileges.
	Elevated bool
	// DryRun plans and reports without changing anything.
	DryRun bool

	// HTTPClient and Endpoints are injectable so downloads and vendor
	// metadata lookups can be tested against a local server.
	HTTPClient *http.Client
	Endpoints  resolverEndpoints
	// Progress receives updates during execution.
	Progress Progress

	log *logging.Logger
	// npmPrefix is Bento's private npm global prefix on Linux, where the
	// system prefix is not writable without root.
	npmPrefix string
	// failed tracks dependencies that did not install, so that anything
	// depending on them is skipped rather than attempted and failed again.
	failed map[string]string
}

// Progress receives updates while a plan is carried out.
type Progress interface {
	// Start announces work on a dependency.
	Start(index, total int, item Item)
	// Step reports a stage within that work.
	Step(message string)
	// Finish reports the outcome.
	Finish(outcome dependency.Outcome)
}

// Options configure a new Installer.
type Options struct {
	Runner      command.Runner
	Registry    *pkgmanager.Registry
	Verifier    *verifier.Verifier
	Environment *environment.Manager
	Services    *services.Manager
	Logger      *logging.Logger
	System      detector.System
	DryRun      bool
	Progress    Progress
}

// New builds an Installer.
func New(options Options) *Installer {
	logger := options.Logger
	if logger == nil {
		logger = logging.NewWriter(discardWriter{})
	}
	progress := options.Progress
	if progress == nil {
		progress = noProgress{}
	}

	return &Installer{
		Runner:      options.Runner,
		Registry:    options.Registry,
		Verifier:    options.Verifier,
		Environment: options.Environment,
		Services:    options.Services,
		OS:          options.System.OS,
		Arch:        options.System.Arch,
		Home:        options.System.Home,
		Elevated:    options.System.Elevated,
		DryRun:      options.DryRun,
		Endpoints:   defaultEndpoints(),
		Progress:    progress,
		log:         logger,
		failed:      map[string]string{},
	}
}

// BuildPlan inspects the machine and decides what to do with every resolved
// dependency. Nothing is modified: this is the state Bento shows the user
// before asking for confirmation.
func (i *Installer) BuildPlan(ctx context.Context, resolution resolver.Resolution, system detector.System) Plan {
	plan := Plan{Profile: resolution.Profile, System: system, DryRun: i.DryRun}

	for _, excluded := range resolution.Unsupported {
		plan.Items = append(plan.Items, Item{
			Spec: excluded.Spec, Action: dependency.ActionUnsupported, Reason: excluded.Reason,
		})
	}
	for _, skipped := range resolution.Skipped {
		plan.Items = append(plan.Items, Item{
			Spec: skipped.Spec, Action: dependency.ActionSkip, Reason: skipped.Reason,
		})
	}

	for _, resolved := range resolution.Ordered {
		plan.Items = append(plan.Items, i.planOne(ctx, resolved.Spec))
	}
	return plan
}

func (i *Installer) planOne(ctx context.Context, spec dependency.Spec) Item {
	item := Item{Spec: spec}
	item.Current = i.Verifier.Verify(ctx, spec)

	platform := spec.Platforms[i.OS]
	item.RestartRequired = platform.RestartRequired

	if item.Current.Present {
		switch {
		case spec.MinimumVersion == "":
			item.Action = dependency.ActionKeep
			return item
		case !item.Current.VersionKnown:
			// Present but the version could not be read. Upgrading blindly
			// would be worse than leaving a working tool alone.
			item.Action = dependency.ActionKeep
			item.Reason = "installed; version could not be determined"
			return item
		case item.Current.Version.Satisfies(spec.MinimumVersion):
			item.Action = dependency.ActionKeep
			return item
		default:
			item.Action = dependency.ActionUpgrade
			item.Reason = fmt.Sprintf("installed %s is older than the required %s",
				item.Current.Version, spec.MinimumVersion)
		}
	} else {
		item.Action = dependency.ActionInstall
	}

	step, reason := i.selectStep(platform)
	if step == nil {
		item.Action = dependency.ActionSkip
		item.Reason = reason
		return item
	}
	if step.Method == dependency.MethodManual {
		item.Action = dependency.ActionSkip
		item.Reason = step.Reason
		item.Step = step
		return item
	}
	if step.Method == dependency.MethodBundled {
		// Nothing to install: it ships with something else. If it is not
		// present by the time that parent has been installed, verification
		// will say so.
		item.Step = step
		item.Source = "bundled with a prerequisite"
		return item
	}

	item.Step = step
	item.Source = step.Describe()
	item.Elevate = step.Elevate || platform.Elevate
	return item
}

// selectStep picks the first installation candidate this machine can actually
// perform. Candidates are ordered in the catalog, so a machine with snapd uses
// the snap and one without falls through to the next option — the fallback
// logic lives in data, not in Go control flow.
func (i *Installer) selectStep(platform dependency.Platform) (*dependency.Step, string) {
	var lastReason string

	for index := range platform.Install {
		step := platform.Install[index]
		switch step.Method {
		case dependency.MethodPackageManager, dependency.MethodLocalPackage:
			if i.Registry.Has(step.Manager) {
				return &step, ""
			}
			lastReason = fmt.Sprintf("%s is not available on this machine", step.Manager)
		case dependency.MethodLanguagePackage:
			if i.languageToolAvailable(step.Via) {
				return &step, ""
			}
			lastReason = fmt.Sprintf("%s is not available on this machine", step.Via)
		case dependency.MethodCommand:
			if len(step.Command) > 0 && command.Available(i.Runner, step.Command[0]) {
				return &step, ""
			}
			lastReason = fmt.Sprintf("%s is not available on this machine", strings.Join(step.Command, " "))
		case dependency.MethodArchive, dependency.MethodInstaller:
			if step.Resolver == "" && step.URLFor(i.Arch) == "" {
				lastReason = fmt.Sprintf("no download is published for %s", i.Arch)
				continue
			}
			return &step, ""
		case dependency.MethodBundled, dependency.MethodManual:
			return &step, ""
		}
	}

	if lastReason == "" {
		lastReason = "no installation method is available on this machine"
	}
	return nil, lastReason
}

func (i *Installer) languageToolAvailable(via string) bool {
	switch via {
	case "pip":
		return command.Available(i.Runner, "python3") || command.Available(i.Runner, "python") ||
			command.Available(i.Runner, "pip3") || command.Available(i.Runner, "pip")
	case "npm":
		return command.Available(i.Runner, "npm")
	case "cargo":
		return command.Available(i.Runner, "cargo")
	case "uv":
		return command.Available(i.Runner, "uv")
	default:
		return false
	}
}

type noProgress struct{}

func (noProgress) Start(int, int, Item)      {}
func (noProgress) Step(string)               {}
func (noProgress) Finish(dependency.Outcome) {}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
