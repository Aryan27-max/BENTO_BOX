package dependency

import "time"

// Status is the lifecycle state of one dependency in a Bento run. The set is
// closed and every value is distinguishable in the final report, because
// "it worked" and "it was already there" and "this platform can't do that" are
// genuinely different outcomes and collapsing them would be dishonest.
type Status string

const (
	// StatusPlanned means Bento intends to act on this dependency.
	StatusPlanned Status = "PLANNED"
	// StatusInstalling is the transient state during installation.
	StatusInstalling Status = "INSTALLING"
	// StatusInstalled means Bento installed it during this run.
	StatusInstalled Status = "INSTALLED"
	// StatusAlreadyInstalled means it was present and satisfactory beforehand.
	StatusAlreadyInstalled Status = "ALREADY_INSTALLED"
	// StatusUpdated means an existing installation was upgraded.
	StatusUpdated Status = "UPDATED"
	// StatusVerified means presence and version were confirmed after install.
	StatusVerified Status = "VERIFIED"
	// StatusFailed means installation or verification genuinely failed.
	StatusFailed Status = "FAILED"
	// StatusWarning means it is usable but something is off — an optional
	// component missing, a service not running, a version below preference.
	StatusWarning Status = "WARNING"
	// StatusSkipped means Bento deliberately did not act, for example in
	// dry-run mode or when a prerequisite failed.
	StatusSkipped Status = "SKIPPED"
	// StatusUnsupported means this dependency cannot be installed on this
	// platform and Bento will not pretend otherwise.
	StatusUnsupported Status = "UNSUPPORTED"
)

// Terminal reports whether the status is a final outcome.
func (s Status) Terminal() bool {
	switch s {
	case StatusPlanned, StatusInstalling:
		return false
	default:
		return true
	}
}

// OK reports whether the status represents a working dependency. WARNING
// counts as OK: the tool is usable, something merely deserves attention.
func (s Status) OK() bool {
	switch s {
	case StatusInstalled, StatusAlreadyInstalled, StatusUpdated, StatusVerified, StatusWarning:
		return true
	default:
		return false
	}
}

// Symbol returns the glyph used for this status in terminal output.
func (s Status) Symbol() string {
	switch s {
	case StatusInstalled, StatusAlreadyInstalled, StatusUpdated, StatusVerified:
		return "✓"
	case StatusFailed:
		return "✗"
	case StatusWarning:
		return "!"
	case StatusUnsupported:
		return "—"
	case StatusSkipped:
		return "·"
	default:
		return "→"
	}
}

// Action describes what Bento intends to do with a dependency, decided before
// anything is modified and shown to the user for confirmation.
type Action string

const (
	// ActionInstall installs a dependency that is absent.
	ActionInstall Action = "install"
	// ActionUpgrade upgrades a dependency that is present but too old.
	ActionUpgrade Action = "upgrade"
	// ActionKeep leaves a satisfactory dependency alone.
	ActionKeep Action = "keep"
	// ActionSkip skips a dependency that cannot or should not be touched.
	ActionSkip Action = "skip"
	// ActionUnsupported records a dependency unavailable on this platform.
	ActionUnsupported Action = "unsupported"
)

// ServiceState is the observed state of a dependency's background service,
// tracked separately from installation because a database that is installed
// but not running is a success, not a failure.
type ServiceState string

const (
	ServiceUnknown     ServiceState = "UNKNOWN"
	ServiceRunning     ServiceState = "RUNNING"
	ServiceStopped     ServiceState = "STOPPED"
	ServiceNotFound    ServiceState = "NOT_FOUND"
	ServiceUnmanaged   ServiceState = "UNMANAGED"
	ServiceStartFailed ServiceState = "START_FAILED"
)

// ServiceStatus records what was observed about a service.
type ServiceStatus struct {
	Name  string       `json:"name"`
	State ServiceState `json:"state"`
	Notes string       `json:"notes,omitempty"`
}

// Outcome is the full record of what happened to one dependency during a run.
// It is what the reporter renders and what report.json serialises.
type Outcome struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name"`
	Category    Category `json:"category"`
	Status      Status   `json:"status"`
	Action      Action   `json:"action"`
	// Version is the version observed after the run, never a guess.
	Version string `json:"version,omitempty"`
	// PreviousVersion is what was installed before an upgrade.
	PreviousVersion string `json:"previous_version,omitempty"`
	// Method records how the dependency was actually installed.
	Method string `json:"method,omitempty"`
	// Source is the concrete origin, such as "winget (Git.Git)".
	Source string `json:"source,omitempty"`
	// Reason explains skips, unsupported platforms and manual installs.
	Reason string `json:"reason,omitempty"`
	// URL points at official instructions for manual dependencies.
	URL string `json:"url,omitempty"`

	Components      []ComponentStatus `json:"components,omitempty"`
	Service         *ServiceStatus    `json:"service,omitempty"`
	Environment     []string          `json:"environment,omitempty"`
	Errors          []string          `json:"errors,omitempty"`
	Warnings        []string          `json:"warnings,omitempty"`
	RestartRequired bool              `json:"restart_required,omitempty"`
	DurationSeconds float64           `json:"duration_seconds"`

	duration time.Duration
}

// ComponentStatus records verification of a sub-tool.
type ComponentStatus struct {
	Name    string `json:"name"`
	Present bool   `json:"present"`
	Version string `json:"version,omitempty"`
}

// SetDuration records how long the dependency took and keeps the serialised
// seconds field in sync.
func (o *Outcome) SetDuration(d time.Duration) {
	o.duration = d
	o.DurationSeconds = d.Round(time.Millisecond).Seconds()
}

// Duration returns the recorded duration.
func (o *Outcome) Duration() time.Duration { return o.duration }

// AddError records a failure message.
func (o *Outcome) AddError(message string) { o.Errors = append(o.Errors, message) }

// AddWarning records a non-fatal problem.
func (o *Outcome) AddWarning(message string) { o.Warnings = append(o.Warnings, message) }

// Summary counts outcomes by status for the final report.
type Summary struct {
	Installed        int `json:"installed"`
	AlreadyInstalled int `json:"already_installed"`
	Updated          int `json:"updated"`
	Failed           int `json:"failed"`
	Skipped          int `json:"skipped"`
	Warnings         int `json:"warnings"`
	Unsupported      int `json:"unsupported"`
	Total            int `json:"total"`
}

// Summarise tallies a set of outcomes.
func Summarise(outcomes []Outcome) Summary {
	summary := Summary{Total: len(outcomes)}
	for _, outcome := range outcomes {
		switch outcome.Status {
		case StatusInstalled, StatusVerified:
			summary.Installed++
		case StatusAlreadyInstalled:
			summary.AlreadyInstalled++
		case StatusUpdated:
			summary.Updated++
		case StatusFailed:
			summary.Failed++
		case StatusSkipped:
			summary.Skipped++
		case StatusWarning:
			summary.Warnings++
		case StatusUnsupported:
			summary.Unsupported++
		}
	}
	return summary
}
