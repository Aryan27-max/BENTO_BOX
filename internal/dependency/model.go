// Package dependency defines the data model that describes every tool Bento
// knows how to install. The model is deliberately declarative: dependency
// knowledge lives in JSON under config/dependencies, never in Go control flow,
// so that adding a tool is a data change rather than a code change.
package dependency

import (
	"fmt"
	"sort"
	"strings"
)

// Category classifies what a dependency *is*. Installation and, more
// importantly, verification differ per category: a CLI tool is verified by
// running it, a GUI application by locating its bundle, a service by asking the
// service manager.
type Category string

const (
	CategoryLanguage       Category = "LANGUAGE"
	CategoryRuntime        Category = "RUNTIME"
	CategoryCLITool        Category = "CLI_TOOL"
	CategoryGUIApplication Category = "GUI_APPLICATION"
	CategoryPackageManager Category = "PACKAGE_MANAGER"
	CategoryDatabase       Category = "DATABASE"
	CategoryDatabaseClient Category = "DATABASE_CLIENT"
	CategoryService        Category = "SERVICE"
	CategorySDK            Category = "SDK"
	CategoryLibrary        Category = "LIBRARY"
	CategoryDevelopment    Category = "DEVELOPMENT_TOOL"
)

// knownCategories is the closed set accepted by the catalog validator.
var knownCategories = map[Category]bool{
	CategoryLanguage: true, CategoryRuntime: true, CategoryCLITool: true,
	CategoryGUIApplication: true, CategoryPackageManager: true, CategoryDatabase: true,
	CategoryDatabaseClient: true, CategoryService: true, CategorySDK: true,
	CategoryLibrary: true, CategoryDevelopment: true,
}

// Valid reports whether the category is one Bento understands.
func (c Category) Valid() bool { return knownCategories[c] }

// Method names the mechanism used to install a dependency on one platform.
type Method string

const (
	// MethodPackageManager installs through the platform's package manager.
	// This is the preferred source for everything that has a real package.
	MethodPackageManager Method = "package_manager"
	// MethodArchive downloads an official release archive over HTTPS,
	// verifies its checksum where the vendor publishes one, and unpacks it
	// into Bento's managed directory. Used where distro packages are too old
	// to satisfy the version policy (Go and Node on Linux, notably).
	MethodArchive Method = "archive"
	// MethodInstaller downloads an official vendor installer or standalone
	// binary and executes it. Used for rustup, which has no good package in
	// most distributions.
	MethodInstaller Method = "installer"
	// MethodLanguagePackage installs through a language package manager such
	// as pip, npm, uv or cargo. Requires the language runtime first.
	MethodLanguagePackage Method = "language_package"
	// MethodBundled marks a dependency that ships with another one — npm with
	// Node, cargo with the Rust toolchain. Bento verifies it but never
	// installs it directly.
	MethodBundled Method = "bundled"
	// MethodLocalPackage downloads an official .deb or .rpm and hands it to the
	// system package manager, which is how vendors that publish packages but
	// not repositories (VS Code on Linux) are installed correctly.
	MethodLocalPackage Method = "local_package"
	// MethodCommand runs an explicit command declared in the catalog, for the
	// handful of tools installed by a first-party system command such as
	// `xcode-select --install`.
	MethodCommand Method = "command"
	// MethodManual marks a dependency with no reliable automated path on this
	// platform. Bento reports it honestly with a reason and an official URL
	// rather than pretending to install it.
	MethodManual Method = "manual"
)

// Spec is the complete declarative description of one dependency.
type Spec struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name"`
	Description string   `json:"description"`
	Category    Category `json:"category"`
	// Profiles lists the profiles that pull this dependency in. The special
	// profile "core" is inherited by every other profile.
	Profiles []string `json:"profiles"`
	// Required marks a dependency whose failure should be treated as serious.
	// Optional dependencies degrade to a warning instead.
	Required bool `json:"required"`
	// Requires names other dependencies that must be installed first. The
	// resolver turns these edges into a topological installation order.
	Requires []string `json:"requires,omitempty"`
	// MinimumVersion, when set, triggers an upgrade if the installed version
	// is older. Empty means "any installed version is acceptable".
	MinimumVersion string `json:"minimum_version,omitempty"`
	// StablePolicy documents which release channel Bento targets. Version
	// numbers live here in data, never scattered through Go source.
	StablePolicy string `json:"stable_policy,omitempty"`
	Homepage     string `json:"homepage,omitempty"`
	Notes        string `json:"notes,omitempty"`
	// RequiresGPU gates hardware-specific tooling. "nvidia" means the
	// dependency is only planned when an NVIDIA GPU is actually detected —
	// Bento never installs CUDA on a machine that cannot use it.
	RequiresGPU string `json:"requires_gpu,omitempty"`

	// Verify is the default verification strategy, overridable per platform.
	Verify Verification `json:"verify"`
	// Platforms maps an OS name ("windows", "linux", "darwin") to how this
	// dependency is installed there. A missing OS key means the dependency is
	// not supported on that OS and will be reported as UNSUPPORTED.
	Platforms map[string]Platform `json:"platforms"`
	// Unsupported gives a human-readable reason per unsupported OS. Bento
	// shows this instead of a bare "not available".
	Unsupported map[string]string `json:"unsupported,omitempty"`

	// Environment and PathEntries are applied after a successful install.
	Environment []EnvVar    `json:"environment,omitempty"`
	PathEntries []PathEntry `json:"path_entries,omitempty"`
	// Service describes the background service this dependency provides, if
	// any. Installing a database and running it are separate concerns.
	Service *Service `json:"service,omitempty"`
}

// Platform describes installation of one dependency on one operating system.
type Platform struct {
	// Arch limits support to specific architectures. Empty means all.
	Arch []string `json:"arch,omitempty"`
	// Install lists installation candidates in priority order. Bento tries
	// them in sequence and stops at the first that succeeds, which is how
	// fallbacks (a renamed Homebrew cask, a distro without a package) are
	// expressed without hardcoding fallback logic in Go.
	Install []Step `json:"install"`
	// Verify overrides Spec.Verify on this platform.
	Verify *Verification `json:"verify,omitempty"`
	// Elevate marks installs that need administrator or root privileges.
	Elevate     bool        `json:"elevate,omitempty"`
	Environment []EnvVar    `json:"environment,omitempty"`
	PathEntries []PathEntry `json:"path_entries,omitempty"`
	Service     *Service    `json:"service,omitempty"`
	// RestartRequired marks installs that only take effect after a reboot.
	RestartRequired bool `json:"restart_required,omitempty"`
	// Notes carries platform-specific caveats into the report.
	Notes string `json:"notes,omitempty"`
}

// Step is a single installation candidate.
type Step struct {
	Method Method `json:"method"`

	// Manager selects the package manager for MethodPackageManager
	// ("winget", "apt", "dnf", "pacman", "zypper", "brew", "snap").
	Manager string `json:"manager,omitempty"`
	// Packages are the package identifiers to install. Several may be needed
	// for one logical dependency (python3 plus python3-pip plus python3-venv).
	Packages []string `json:"packages,omitempty"`
	// Cask selects a Homebrew cask rather than a formula.
	Cask bool `json:"cask,omitempty"`
	// Classic selects a snap that needs classic confinement.
	Classic bool `json:"classic,omitempty"`
	// Args appends extra arguments to the package-manager invocation.
	Args []string `json:"args,omitempty"`

	// Via selects the language package manager for MethodLanguagePackage
	// ("pip", "npm", "uv", "cargo").
	Via string `json:"via,omitempty"`

	// Command is the argv executed by MethodCommand.
	Command []string `json:"command,omitempty"`
	// PreInstall and PostInstall are commands run around the install itself.
	// Tapping a Homebrew repository or selecting the stable Rust toolchain
	// belongs in data, not in Go control flow.
	PreInstall  [][]string `json:"pre_install,omitempty"`
	PostInstall [][]string `json:"post_install,omitempty"`

	// URL is the official download location for MethodArchive,
	// MethodInstaller, MethodLocalPackage and MethodManual. It must be HTTPS.
	URL string `json:"url,omitempty"`
	// URLs supplies per-architecture download locations, keyed by GOARCH.
	// It takes precedence over URL when the running architecture matches.
	URLs map[string]string `json:"urls,omitempty"`
	// Resolver names a built-in resolver that discovers the current stable
	// release from the vendor's official metadata endpoint, so Bento does not
	// have to hardcode version numbers ("go_stable", "node_lts").
	Resolver string `json:"resolver,omitempty"`
	// Checksum, when set, is the expected SHA-256 of the download. Resolvers
	// usually supply this from vendor metadata instead.
	Checksum string `json:"checksum,omitempty"`
	// InstallDir is the directory under Bento's home where an archive is
	// unpacked.
	InstallDir string `json:"install_dir,omitempty"`
	// InstallerArgs are the flags passed to a downloaded installer to make it
	// run unattended.
	InstallerArgs []string `json:"installer_args,omitempty"`

	// Reason explains, for MethodManual, why no automated path exists. It is
	// shown verbatim in the report.
	Reason string `json:"reason,omitempty"`
	// Elevate marks this individual step as needing elevated privileges.
	Elevate bool `json:"elevate,omitempty"`
}

// Describe renders a short human-readable label for the step, used in plans
// and reports so the user can see exactly where software comes from.
func (s Step) Describe() string {
	switch s.Method {
	case MethodPackageManager:
		return fmt.Sprintf("%s (%s)", s.Manager, strings.Join(s.Packages, ", "))
	case MethodLanguagePackage:
		return fmt.Sprintf("%s (%s)", s.Via, strings.Join(s.Packages, ", "))
	case MethodArchive:
		if s.Resolver != "" {
			return "official archive (" + s.Resolver + ")"
		}
		return "official archive"
	case MethodInstaller:
		return "official installer"
	case MethodLocalPackage:
		return "official package download"
	case MethodCommand:
		return strings.Join(s.Command, " ")
	case MethodBundled:
		return "bundled with a parent dependency"
	case MethodManual:
		return "manual installation required"
	default:
		return string(s.Method)
	}
}

// Verification describes how to confirm a dependency is genuinely present and
// which version it is. Bento never reports a version it did not observe.
type Verification struct {
	// Command is the executable to run for a version check.
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	// Pattern is an optional regular expression with one capturing group used
	// to pull the version out of the command output.
	Pattern string `json:"pattern,omitempty"`
	// Paths lists candidate filesystem locations per OS for GUI applications
	// and other software that is not reliably on PATH. Entries may contain
	// ${HOME}, ${LOCALAPPDATA} and ${PROGRAMFILES} placeholders.
	Paths map[string][]string `json:"paths,omitempty"`
	// Components are sub-tools that must also be present for the dependency to
	// count as verified — rustfmt and clippy for a Rust toolchain, for example.
	Components []Component `json:"components,omitempty"`
	// SkipVersion marks dependencies with no reliable version command, so the
	// report shows presence without inventing a version number.
	SkipVersion bool `json:"skip_version,omitempty"`
}

// Component is a sub-tool verified as part of its parent dependency.
type Component struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	Pattern string   `json:"pattern,omitempty"`
	// Optional components are reported but never fail verification.
	Optional bool `json:"optional,omitempty"`
}

// EnvVar is an environment variable Bento sets after installation.
type EnvVar struct {
	Name string `json:"name"`
	// Value supports ${HOME} and ${BENTO_HOME} placeholders, expanded at
	// apply time against the detected system.
	Value string `json:"value"`
	// FromCommand, when set, derives the value from a command's output
	// (GOROOT comes from `go env GOROOT`, never from a guess).
	FromCommand []string `json:"from_command,omitempty"`
	// OS restricts the variable to one operating system.
	OS string `json:"os,omitempty"`
	// RequirePath makes the variable conditional on its value existing on
	// disk. ANDROID_HOME is only worth setting if the SDK is actually there.
	RequirePath bool `json:"require_path,omitempty"`
}

// PathEntry is a directory added to PATH after installation.
type PathEntry struct {
	// Path is the directory to add. When FromCommand is also set, Path is
	// treated as a suffix appended to the command's output, which is how
	// `brew --prefix openjdk` plus "bin" becomes a real PATH entry.
	Path string `json:"path"`
	OS   string `json:"os,omitempty"`
	// FromCommand derives the directory from a command's output.
	FromCommand []string `json:"from_command,omitempty"`
}

// Service describes a background service provided by a dependency.
type Service struct {
	// Names maps OS to the service identifier used by that OS's service
	// manager.
	Names map[string]string `json:"names"`
	// Required marks a service that a profile genuinely needs running. Only
	// these are started automatically; everything else is installed and left
	// alone, because starting databases without being asked is rude.
	Required bool `json:"required,omitempty"`
	// Manual documents services that Bento will not manage automatically,
	// such as Docker Desktop which is a user-launched application.
	Manual bool `json:"manual,omitempty"`
	// Notes explains service handling in the report.
	Notes string `json:"notes,omitempty"`
}

// SupportsOS reports whether the dependency declares support for an OS.
func (s Spec) SupportsOS(os string) bool {
	platform, ok := s.Platforms[os]
	return ok && len(platform.Install) > 0
}

// SupportsArch reports whether the dependency supports an architecture on an
// OS. An empty Arch list means every architecture is supported.
func (s Spec) SupportsArch(os, arch string) bool {
	platform, ok := s.Platforms[os]
	if !ok {
		return false
	}
	if len(platform.Arch) == 0 {
		return true
	}
	for _, candidate := range platform.Arch {
		if candidate == arch {
			return true
		}
	}
	return false
}

// UnsupportedReason explains why a dependency cannot be installed on the given
// system. It returns an empty string when the dependency is supported.
func (s Spec) UnsupportedReason(os, arch string) string {
	if reason, ok := s.Unsupported[os]; ok {
		return reason
	}
	if !s.SupportsOS(os) {
		return fmt.Sprintf("%s is not available on %s", s.Label(), osDisplayName(os))
	}
	if !s.SupportsArch(os, arch) {
		return fmt.Sprintf("%s is not available for %s on %s", s.Label(), arch, osDisplayName(os))
	}
	return ""
}

// VerificationFor returns the verification strategy for an OS, applying the
// platform override when one is present.
func (s Spec) VerificationFor(os string) Verification {
	if platform, ok := s.Platforms[os]; ok && platform.Verify != nil {
		return *platform.Verify
	}
	return s.Verify
}

// ServiceFor returns the service definition for an OS, if any.
func (s Spec) ServiceFor(os string) *Service {
	if platform, ok := s.Platforms[os]; ok && platform.Service != nil {
		return platform.Service
	}
	return s.Service
}

// EnvironmentFor returns the environment variables to apply on an OS, merging
// the platform-specific list onto the general one and dropping entries scoped
// to a different OS.
func (s Spec) EnvironmentFor(os string) []EnvVar {
	var out []EnvVar
	appendScoped := func(vars []EnvVar) {
		for _, variable := range vars {
			if variable.OS == "" || variable.OS == os {
				out = append(out, variable)
			}
		}
	}
	appendScoped(s.Environment)
	if platform, ok := s.Platforms[os]; ok {
		appendScoped(platform.Environment)
	}
	return out
}

// PathEntriesFor returns the PATH additions to apply on an OS.
func (s Spec) PathEntriesFor(os string) []PathEntry {
	var out []PathEntry
	appendScoped := func(entries []PathEntry) {
		for _, entry := range entries {
			if entry.OS == "" || entry.OS == os {
				out = append(out, entry)
			}
		}
	}
	appendScoped(s.PathEntries)
	if platform, ok := s.Platforms[os]; ok {
		appendScoped(platform.PathEntries)
	}
	return out
}

// Label returns the human-facing name, falling back to the identifier.
func (s Spec) Label() string {
	if s.DisplayName != "" {
		return s.DisplayName
	}
	return s.Name
}

// InProfile reports whether the dependency belongs to a profile.
func (s Spec) InProfile(profile string) bool {
	for _, candidate := range s.Profiles {
		if candidate == profile {
			return true
		}
	}
	return false
}

// Validate checks a spec for internal consistency. The catalog runs this over
// every entry at load time and a test runs it over the shipped catalog, so a
// malformed dependency definition can never reach a user's machine.
func (s Spec) Validate() error {
	if strings.TrimSpace(s.Name) == "" {
		return fmt.Errorf("dependency has no name")
	}
	if !s.Category.Valid() {
		return fmt.Errorf("%s: unknown category %q", s.Name, s.Category)
	}
	if len(s.Profiles) == 0 {
		return fmt.Errorf("%s: belongs to no profile", s.Name)
	}
	if len(s.Platforms) == 0 {
		return fmt.Errorf("%s: declares no platforms", s.Name)
	}

	for osName, platform := range s.Platforms {
		if !validOS[osName] {
			return fmt.Errorf("%s: unknown platform %q", s.Name, osName)
		}
		if len(platform.Install) == 0 {
			return fmt.Errorf("%s/%s: no installation steps", s.Name, osName)
		}
		for index, step := range platform.Install {
			if err := step.validate(); err != nil {
				return fmt.Errorf("%s/%s step %d: %w", s.Name, osName, index, err)
			}
		}
	}

	// A dependency with no default verification must supply one per platform,
	// otherwise Bento would have no way to tell whether it worked.
	if s.Verify.Command == "" && len(s.Verify.Paths) == 0 && len(s.Verify.Components) == 0 {
		for osName, platform := range s.Platforms {
			if platform.Verify == nil {
				return fmt.Errorf("%s: no verification strategy for %s", s.Name, osName)
			}
		}
	}
	return nil
}

func (s Step) validate() error {
	switch s.Method {
	case MethodPackageManager:
		if s.Manager == "" {
			return fmt.Errorf("package_manager step needs a manager")
		}
		if len(s.Packages) == 0 {
			return fmt.Errorf("package_manager step needs at least one package")
		}
	case MethodLanguagePackage:
		if s.Via == "" {
			return fmt.Errorf("language_package step needs a via")
		}
		if len(s.Packages) == 0 {
			return fmt.Errorf("language_package step needs at least one package")
		}
	case MethodArchive, MethodInstaller, MethodLocalPackage:
		if s.URL == "" && s.Resolver == "" && len(s.URLs) == 0 {
			return fmt.Errorf("%s step needs a url, urls or a resolver", s.Method)
		}
		// Every download must be transport-secured; see the security policy in
		// README. A plain-HTTP download would be a supply-chain hole.
		for _, url := range append([]string{s.URL}, mapValues(s.URLs)...) {
			if url != "" && !strings.HasPrefix(url, "https://") {
				return fmt.Errorf("%s step url must be https, got %q", s.Method, url)
			}
		}
	case MethodCommand:
		if len(s.Command) == 0 {
			return fmt.Errorf("command step needs a command")
		}
	case MethodManual:
		if s.Reason == "" {
			return fmt.Errorf("manual step needs a reason")
		}
	case MethodBundled:
		// Nothing to install; verification carries the whole check.
	default:
		return fmt.Errorf("unknown install method %q", s.Method)
	}
	return nil
}

// URLFor returns the download location for an architecture, preferring the
// per-architecture map and falling back to the single URL.
func (s Step) URLFor(arch string) string {
	if url, ok := s.URLs[arch]; ok {
		return url
	}
	return s.URL
}

func mapValues(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, value := range m {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

var validOS = map[string]bool{"windows": true, "linux": true, "darwin": true}

func osDisplayName(os string) string {
	switch os {
	case "windows":
		return "Windows"
	case "linux":
		return "Linux"
	case "darwin":
		return "macOS"
	default:
		return os
	}
}

// SortSpecs orders specs by name so that output is deterministic.
func SortSpecs(specs []Spec) {
	sort.Slice(specs, func(i, j int) bool { return specs[i].Name < specs[j].Name })
}
