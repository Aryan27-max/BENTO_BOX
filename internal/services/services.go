// Package services reports on the background services that some dependencies
// provide. Installation and runtime are deliberately separate concerns here: a
// database that is installed but not running is a successful installation, and
// Bento says exactly that instead of calling it a failure.
package services

import (
	"context"
	"fmt"
	"strings"

	"github.com/Aryan27-max/bento-box/internal/command"
	"github.com/Aryan27-max/bento-box/internal/dependency"
)

// Manager inspects and, when a profile genuinely requires it, starts services.
type Manager struct {
	Runner command.Runner
	OS     string
	// Elevated reports whether Bento can manage system services without sudo.
	Elevated bool
	// DryRun prevents any service from actually being started.
	DryRun bool
}

// New returns a service manager.
func New(runner command.Runner, osName string, elevated bool) *Manager {
	return &Manager{Runner: runner, OS: osName, Elevated: elevated}
}

// Status reports the observed state of a dependency's service.
func (m *Manager) Status(ctx context.Context, service *dependency.Service) *dependency.ServiceStatus {
	if service == nil {
		return nil
	}
	name := service.Names[m.OS]
	if name == "" {
		return nil
	}

	status := &dependency.ServiceStatus{Name: name, State: dependency.ServiceUnknown, Notes: service.Notes}
	if service.Manual {
		// Docker Desktop and Ollama are launched by the user; reporting them
		// as "stopped" would imply Bento should have started them.
		status.State = dependency.ServiceUnmanaged
		return status
	}
	status.State = m.observe(ctx, name)
	return status
}

func (m *Manager) observe(ctx context.Context, name string) dependency.ServiceState {
	switch m.OS {
	case "windows":
		return m.observeWindows(ctx, name)
	case "linux":
		return m.observeSystemd(ctx, name)
	case "darwin":
		return m.observeLaunchd(ctx, name)
	default:
		return dependency.ServiceUnknown
	}
}

func (m *Manager) observeWindows(ctx context.Context, name string) dependency.ServiceState {
	result, err := m.Runner.Run(ctx, command.Spec{
		Name: "sc", Args: []string{"query", name}, AllowFailure: true,
	})
	if err != nil {
		return dependency.ServiceUnknown
	}
	output := result.Stdout + result.Stderr
	switch {
	case strings.Contains(output, "1060") || strings.Contains(output, "does not exist"):
		return dependency.ServiceNotFound
	case strings.Contains(output, "RUNNING"):
		return dependency.ServiceRunning
	case strings.Contains(output, "STOPPED"), strings.Contains(output, "START_PENDING"):
		return dependency.ServiceStopped
	default:
		return dependency.ServiceUnknown
	}
}

func (m *Manager) observeSystemd(ctx context.Context, name string) dependency.ServiceState {
	if !command.Available(m.Runner, "systemctl") {
		// Containers and minimal images frequently have no init system at
		// all; "unknown" is the truthful answer, not "stopped".
		return dependency.ServiceUnknown
	}
	result, err := m.Runner.Run(ctx, command.Spec{
		Name: "systemctl", Args: []string{"is-active", name}, AllowFailure: true,
	})
	if err != nil {
		return dependency.ServiceUnknown
	}
	switch strings.TrimSpace(result.Output()) {
	case "active", "activating":
		return dependency.ServiceRunning
	case "inactive", "failed", "deactivating":
		return dependency.ServiceStopped
	case "unknown":
		return dependency.ServiceNotFound
	default:
		return dependency.ServiceUnknown
	}
}

func (m *Manager) observeLaunchd(ctx context.Context, name string) dependency.ServiceState {
	// Almost everything Bento installs on macOS comes from Homebrew, which
	// manages its services through `brew services`.
	if command.Available(m.Runner, "brew") {
		result, err := m.Runner.Run(ctx, command.Spec{
			Name: "brew", Args: []string{"services", "list"}, AllowFailure: true,
		})
		if err == nil && result.Success() {
			for _, line := range strings.Split(result.Stdout, "\n") {
				fields := strings.Fields(line)
				if len(fields) < 2 || fields[0] != name {
					continue
				}
				switch fields[1] {
				case "started":
					return dependency.ServiceRunning
				case "stopped", "none":
					return dependency.ServiceStopped
				default:
					return dependency.ServiceUnknown
				}
			}
			return dependency.ServiceNotFound
		}
	}
	return dependency.ServiceUnknown
}

// Start attempts to start a service. It is only called for services a profile
// genuinely requires: Bento does not start every database it installs, because
// doing so silently changes what runs on the user's machine at boot.
func (m *Manager) Start(ctx context.Context, service *dependency.Service) (dependency.ServiceState, error) {
	if service == nil {
		return dependency.ServiceUnknown, nil
	}
	name := service.Names[m.OS]
	if name == "" {
		return dependency.ServiceUnknown, fmt.Errorf("no service name known for %s", m.OS)
	}
	if service.Manual {
		return dependency.ServiceUnmanaged, nil
	}
	if m.DryRun {
		return m.observe(ctx, name), nil
	}

	var spec command.Spec
	switch m.OS {
	case "windows":
		spec = command.Spec{Name: "sc", Args: []string{"start", name}}
	case "linux":
		if !command.Available(m.Runner, "systemctl") {
			return dependency.ServiceUnknown, fmt.Errorf("no systemctl on this system")
		}
		spec = command.Spec{Name: "systemctl", Args: []string{"start", name}}
		if !m.Elevated {
			if !command.Available(m.Runner, "sudo") {
				return dependency.ServiceStopped, fmt.Errorf("starting %s needs root privileges", name)
			}
			spec = command.Spec{Name: "sudo", Args: []string{"-n", "systemctl", "start", name}}
		}
	case "darwin":
		spec = command.Spec{Name: "brew", Args: []string{"services", "start", name}}
	default:
		return dependency.ServiceUnknown, fmt.Errorf("service management is not supported on %s", m.OS)
	}

	result, err := m.Runner.Run(ctx, spec)
	if err != nil {
		return dependency.ServiceStartFailed, err
	}
	if !result.Success() {
		return dependency.ServiceStartFailed, fmt.Errorf("%s exited with code %d", spec.String(), result.ExitCode)
	}
	return m.observe(ctx, name), nil
}
