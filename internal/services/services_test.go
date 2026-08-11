package services

import (
	"context"
	"testing"

	"github.com/Aryan27-max/bento-box/internal/command"
	"github.com/Aryan27-max/bento-box/internal/dependency"
)

func postgres() *dependency.Service {
	return &dependency.Service{Names: map[string]string{
		"linux": "postgresql", "windows": "postgresql-x64-17", "darwin": "postgresql@17",
	}}
}

func TestSystemdStates(t *testing.T) {
	cases := map[string]dependency.ServiceState{
		"active":     dependency.ServiceRunning,
		"activating": dependency.ServiceRunning,
		"inactive":   dependency.ServiceStopped,
		"failed":     dependency.ServiceStopped,
		"unknown":    dependency.ServiceNotFound,
	}

	for output, want := range cases {
		runner := command.NewMock()
		runner.AddPath("systemctl", "/usr/bin/systemctl")
		runner.Respond("systemctl is-active postgresql", command.Result{Stdout: output + "\n", ExitCode: 3})

		manager := New(runner, "linux", false)
		if got := manager.Status(context.Background(), postgres()).State; got != want {
			t.Errorf("systemctl reporting %q gave %s, want %s", output, got, want)
		}
	}
}

// TestNoInitSystemIsUnknownNotStopped: containers and minimal images have no
// service manager at all, and claiming a service is "stopped" there would be a
// guess.
func TestNoInitSystemIsUnknownNotStopped(t *testing.T) {
	manager := New(command.NewMock(), "linux", false)

	if got := manager.Status(context.Background(), postgres()).State; got != dependency.ServiceUnknown {
		t.Errorf("state = %s, want UNKNOWN when there is no systemctl", got)
	}
}

func TestWindowsServiceStates(t *testing.T) {
	cases := map[string]dependency.ServiceState{
		"SERVICE_NAME: postgresql-x64-17\n  STATE : 4  RUNNING": dependency.ServiceRunning,
		"SERVICE_NAME: postgresql-x64-17\n  STATE : 1  STOPPED": dependency.ServiceStopped,
		"[SC] EnumQueryServicesStatus:OpenService FAILED 1060":  dependency.ServiceNotFound,
	}

	for output, want := range cases {
		runner := command.NewMock()
		runner.RespondOK("sc query postgresql-x64-17", output)

		manager := New(runner, "windows", true)
		if got := manager.Status(context.Background(), postgres()).State; got != want {
			t.Errorf("sc output %q gave %s, want %s", output, got, want)
		}
	}
}

func TestBrewServicesStates(t *testing.T) {
	runner := command.NewMock()
	runner.AddPath("brew", "/opt/homebrew/bin/brew")
	runner.RespondOK("brew services list",
		"Name          Status   User  File\nredis         started  dev   ~/Library/LaunchAgents/redis.plist\npostgresql@17 stopped")

	manager := New(runner, "darwin", false)

	if got := manager.Status(context.Background(), postgres()).State; got != dependency.ServiceStopped {
		t.Errorf("postgresql@17 = %s, want STOPPED", got)
	}

	redis := &dependency.Service{Names: map[string]string{"darwin": "redis"}}
	if got := manager.Status(context.Background(), redis).State; got != dependency.ServiceRunning {
		t.Errorf("redis = %s, want RUNNING", got)
	}
}

// TestManualServicesAreNotJudged: Docker Desktop is launched by the user, so
// reporting it as "stopped" would imply Bento should have started it.
func TestManualServicesAreNotJudged(t *testing.T) {
	docker := &dependency.Service{
		Names:  map[string]string{"windows": "com.docker.service"},
		Manual: true,
		Notes:  "Docker Desktop is launched by the user.",
	}

	runner := command.NewMock()
	manager := New(runner, "windows", true)

	status := manager.Status(context.Background(), docker)
	if status.State != dependency.ServiceUnmanaged {
		t.Errorf("state = %s, want UNMANAGED", status.State)
	}
	if len(runner.Calls) != 0 {
		t.Error("a manually-managed service should not be probed")
	}
	if status.Notes == "" {
		t.Error("the explanation from the catalog should reach the report")
	}
}

func TestServiceWithoutANameOnThisOSIsNotReported(t *testing.T) {
	redis := &dependency.Service{Names: map[string]string{"linux": "redis-server"}}

	manager := New(command.NewMock(), "windows", true)
	if status := manager.Status(context.Background(), redis); status != nil {
		t.Errorf("status = %+v, want nil when the OS has no service name", status)
	}
}

func TestNilServiceIsNil(t *testing.T) {
	if status := New(command.NewMock(), "linux", false).Status(context.Background(), nil); status != nil {
		t.Error("a dependency without a service should produce no service status")
	}
}

func TestStartUsesSudoWhenNotRoot(t *testing.T) {
	runner := command.NewMock()
	runner.AddPath("systemctl", "/usr/bin/systemctl")
	runner.AddPath("sudo", "/usr/bin/sudo")
	runner.RespondOK("sudo -n systemctl start postgresql", "")
	runner.RespondOK("systemctl is-active postgresql", "active")

	manager := New(runner, "linux", false)
	state, err := manager.Start(context.Background(), postgres())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if state != dependency.ServiceRunning {
		t.Errorf("state = %s, want RUNNING", state)
	}
	if !runner.Ran("sudo -n systemctl start") {
		t.Errorf("expected a sudo systemctl call, got %v", runner.Invocations())
	}
}

func TestStartWithoutPrivilegesFailsHonestly(t *testing.T) {
	runner := command.NewMock()
	runner.AddPath("systemctl", "/usr/bin/systemctl")

	manager := New(runner, "linux", false)
	state, err := manager.Start(context.Background(), postgres())
	if err == nil {
		t.Fatal("expected an error when the service cannot be started")
	}
	if state == dependency.ServiceRunning {
		t.Error("a service that could not be started must not be reported as running")
	}
}

func TestDryRunNeverStartsAnything(t *testing.T) {
	runner := command.NewMock()
	runner.AddPath("systemctl", "/usr/bin/systemctl")
	runner.RespondOK("systemctl is-active postgresql", "inactive")

	manager := New(runner, "linux", true)
	manager.DryRun = true

	state, err := manager.Start(context.Background(), postgres())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if state != dependency.ServiceStopped {
		t.Errorf("state = %s, want the observed state", state)
	}
	if runner.Ran("systemctl start") {
		t.Error("dry run must not start a service")
	}
}
