package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Aryan27-max/bento-box/internal/catalog"
	"github.com/Aryan27-max/bento-box/internal/command"
	"github.com/Aryan27-max/bento-box/internal/detector"
	"github.com/Aryan27-max/bento-box/internal/environment"
	"github.com/Aryan27-max/bento-box/internal/installer"
	"github.com/Aryan27-max/bento-box/internal/logging"
	"github.com/Aryan27-max/bento-box/internal/paths"
	"github.com/Aryan27-max/bento-box/internal/pkgmanager"
	"github.com/Aryan27-max/bento-box/internal/reporter"
	"github.com/Aryan27-max/bento-box/internal/resolver"
	"github.com/Aryan27-max/bento-box/internal/services"
	"github.com/Aryan27-max/bento-box/internal/verifier"
)

// Exit codes. They are stable so that scripts can act on them.
const (
	// ExitOK means the run finished and nothing required failed.
	ExitOK = 0
	// ExitFailure means at least one dependency failed to install.
	ExitFailure = 1
	// ExitUsage means the command line was wrong.
	ExitUsage = 2
	// ExitCancelled means the user declined at a prompt. Nothing was changed.
	ExitCancelled = 130
)

// Options are the parsed command-line flags.
type Options struct {
	Profile   string
	AssumeYes bool
	DryRun    bool
	PlanOnly  bool
	JSON      bool
	NoColor   bool
	Verbose   bool
	Version   bool
	List      bool
}

const usage = `🍱 Bento Box — turn a fresh machine into a developer machine.

Usage:
  bento [flags]

Flags:
  -p, --profile <name>   Install a profile without being asked
                         (ai, web, blockchain, app, nuke)
  -y, --yes              Skip the confirmation prompt
      --dry-run          Show what would happen without changing anything
      --plan             Show the plan and stop
      --json             Write the report as JSON to stdout
      --verbose          Show every installation step
      --no-color         Disable coloured output
      --list             List the available profiles
  -v, --version          Print the Bento version
  -h, --help             Show this help

Examples:
  bento                            Choose a profile interactively
  bento --profile web --yes        Install the web profile unattended
  bento --profile nuke --dry-run   See what NUKE would install
`

// ParseOptions reads the command line.
func ParseOptions(arguments []string, output io.Writer) (Options, error) {
	var options Options

	flags := flag.NewFlagSet("bento", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.Usage = func() { fmt.Fprint(output, usage) }

	flags.StringVar(&options.Profile, "profile", "", "profile to install")
	flags.StringVar(&options.Profile, "p", "", "profile to install")
	flags.BoolVar(&options.AssumeYes, "yes", false, "skip confirmation")
	flags.BoolVar(&options.AssumeYes, "y", false, "skip confirmation")
	flags.BoolVar(&options.DryRun, "dry-run", false, "change nothing")
	flags.BoolVar(&options.PlanOnly, "plan", false, "show the plan and stop")
	flags.BoolVar(&options.JSON, "json", false, "write the report as JSON")
	flags.BoolVar(&options.NoColor, "no-color", false, "disable colour")
	flags.BoolVar(&options.Verbose, "verbose", false, "show every step")
	flags.BoolVar(&options.Version, "version", false, "print the version")
	flags.BoolVar(&options.Version, "v", false, "print the version")
	flags.BoolVar(&options.List, "list", false, "list profiles")

	if err := flags.Parse(arguments); err != nil {
		return options, err
	}
	if extra := flags.Args(); len(extra) > 0 {
		// A bare profile name is what people type first; accept it.
		if options.Profile == "" && len(extra) == 1 {
			options.Profile = extra[0]
		} else {
			return options, fmt.Errorf("unexpected arguments: %s", strings.Join(extra, " "))
		}
	}
	return options, nil
}

// Version is the Bento version, set at build time via -ldflags.
var Version = "dev"

// Run executes Bento and returns a process exit code.
func Run(ctx context.Context, arguments []string, stdout, stderr io.Writer) int {
	options, err := ParseOptions(arguments, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitOK
		}
		fmt.Fprintf(stderr, "bento: %v\n", err)
		return ExitUsage
	}

	style := NewStyle(os.Stdout, options.NoColor || options.JSON)
	ui := &UI{Output: stdout, Style: style, Verbose: options.Verbose}

	// In JSON mode the report is the only thing on stdout, so the human
	// interface goes to stderr where it will not corrupt the output.
	if options.JSON {
		ui.Output = stderr
	}

	if options.Version {
		fmt.Fprintf(stdout, "bento %s\n", Version)
		return ExitOK
	}

	loaded, err := catalog.Load()
	if err != nil {
		fmt.Fprintf(stderr, "bento: the built-in dependency catalog is invalid: %v\n", err)
		return ExitFailure
	}

	if options.List {
		listProfiles(stdout, loaded, style)
		return ExitOK
	}

	return run(ctx, options, ui, loaded, stdout, stderr)
}

func listProfiles(output io.Writer, loaded *catalog.Catalog, style Style) {
	fmt.Fprintf(output, "%s\n\n", style.Bold("Profiles"))
	for _, profile := range loaded.Profiles().Selectable() {
		fmt.Fprintf(output, "  %s %s\n", pad(profile.ID, 12), profile.Label())
		fmt.Fprintf(output, "  %s %s\n", pad("", 12), style.Dim(profile.Description))
	}
}

func run(ctx context.Context, options Options, ui *UI, loaded *catalog.Catalog, stdout, stderr io.Writer) int {
	started := time.Now()

	ui.Banner(Version)

	// --- detect ---------------------------------------------------------
	logger := logging.NewWriter(io.Discard)
	runner := command.NewSystemRunner()

	system := detector.New(runner).Detect(ctx)
	if system.Home != "" {
		logger = logging.New(paths.Home(system.Home))
		defer logger.Close()
	}
	runner.Observer = logger.Observer()
	logger.Printf("bento %s starting on %s %s/%s", Version, system.Display(), system.OS, system.Arch)

	ui.System(system)

	// --- choose a profile ------------------------------------------------
	prompt := NewPrompt(ui.Style, ui.Output)

	profileID := options.Profile
	if profileID == "" {
		profile, err := prompt.SelectProfile(loaded.Profiles().Selectable())
		if err != nil {
			if errors.Is(err, ErrCancelled) {
				fmt.Fprintf(ui.Output, "%s\n", ui.Style.Dim("Cancelled. Nothing was changed."))
				return ExitCancelled
			}
			fmt.Fprintf(stderr, "bento: %v\n", err)
			return ExitUsage
		}
		profileID = profile.ID
	}

	profile, ok := loaded.Profiles().Lookup(profileID)
	if !ok {
		fmt.Fprintf(stderr, "bento: unknown profile %q. Known profiles: %s\n",
			profileID, strings.Join(loaded.Profiles().IDs(), ", "))
		return ExitUsage
	}

	// --- resolve ---------------------------------------------------------
	resolution, err := resolver.Resolve(resolver.Request{
		Catalog: loaded, Profile: profile.ID,
		OS: system.OS, Arch: system.Arch, NvidiaGPU: system.NvidiaGPU,
	})
	if err != nil {
		fmt.Fprintf(stderr, "bento: %v\n", err)
		return ExitFailure
	}
	logger.Printf("resolved %d dependencies for profile %s", resolution.Total(), profile.ID)

	// --- build the plan ---------------------------------------------------
	fmt.Fprintf(ui.Output, "%s\n", ui.Style.Dim("Checking what is already installed…"))

	registry := pkgmanager.NewRegistry(pkgmanager.Options{
		Runner: runner, Elevated: system.Elevated, DryRun: options.DryRun,
	})
	check := verifier.New(runner, system.OS, system.Home)
	environmentManager := environment.New(system.OS, system.Home, system.Shell.ConfigFile, system.Shell.Name, options.DryRun)
	serviceManager := services.New(runner, system.OS, system.Elevated)
	serviceManager.DryRun = options.DryRun

	progress := &Progress{UI: ui}
	engine := installer.New(installer.Options{
		Runner: runner, Registry: registry, Verifier: check,
		Environment: environmentManager, Services: serviceManager,
		Logger: logger, System: system, DryRun: options.DryRun, Progress: progress,
	})

	plan := engine.BuildPlan(ctx, resolution, system)
	ui.ClearLine()
	ui.Plan(plan)

	if options.PlanOnly {
		return ExitOK
	}

	// --- confirm ----------------------------------------------------------
	// Nothing above this line modified the machine.
	if plan.ChangesNothing() {
		fmt.Fprintf(ui.Output, "%s\n", ui.Style.Green("Everything in this profile is already installed. Nothing to do."))
		if !options.JSON {
			return ExitOK
		}
	} else if !options.AssumeYes && !options.DryRun {
		confirmed, err := prompt.Confirm("Continue?")
		if err != nil {
			fmt.Fprintf(stderr, "bento: %v\n", err)
			return ExitUsage
		}
		if !confirmed {
			fmt.Fprintf(ui.Output, "%s\n", ui.Style.Dim("Cancelled. Nothing was changed."))
			return ExitCancelled
		}
		fmt.Fprintln(ui.Output)
	}

	// --- install ----------------------------------------------------------
	outcomes := engine.Execute(ctx, plan)

	// --- report -----------------------------------------------------------
	report := reporter.Build(reporter.Input{
		Version:       Version,
		System:        system,
		ProfileID:     profile.ID,
		ProfileName:   profile.Label(),
		DryRun:        options.DryRun,
		Outcomes:      outcomes,
		Environment:   environmentManager.Changes(),
		Duration:      time.Since(started),
		LogFile:       logger.Path(),
		RestartNotice: environmentManager.RestartNotice(),
	})

	if options.JSON {
		if err := report.WriteJSONTo(stdout); err != nil {
			fmt.Fprintf(stderr, "bento: could not write the JSON report: %v\n", err)
			return ExitFailure
		}
	} else {
		fmt.Fprintln(ui.Output)
		report.Render(ui.Output, ui.Style.Palette())
	}

	if !options.DryRun && system.Home != "" {
		path, err := report.WriteJSON(system.Home)
		if err != nil {
			fmt.Fprintf(stderr, "bento: could not write report.json: %v\n", err)
		} else if !options.JSON {
			fmt.Fprintf(ui.Output, "%s\n", ui.Style.Dim("Report: "+path))
		}
	}

	if !options.JSON {
		if report.EnvironmentReady {
			fmt.Fprintf(ui.Output, "\n%s\n\n", ui.Style.Green("🚀 Your development environment is ready."))
		} else if report.Summary.Failed > 0 {
			fmt.Fprintf(ui.Output, "\n%s\n\n", ui.Style.Yellow("Some dependencies could not be installed. See the details above."))
		}
	}

	if report.Summary.Failed > 0 {
		return ExitFailure
	}
	return ExitOK
}
