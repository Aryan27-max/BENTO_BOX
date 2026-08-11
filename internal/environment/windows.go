//go:build windows

package environment

import (
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// On Windows the user's environment lives in the registry under
// HKCU\Environment, and that is the only place Bento writes. Editing shell
// startup files would not work here: PowerShell, cmd and every GUI program
// read their environment from the registry-backed user environment block.
//
// Bento deliberately never touches HKLM, the machine environment. A developer
// tool has no business changing the environment of every user on the box.

const environmentKey = `Environment`

// platformBackend uses the registry only when Bento is genuinely configuring a
// Windows machine. A Manager targeting Linux — which is what the test suite
// uses — gets the state backend even on a Windows host, so tests can never
// write to the developer's own registry.
func platformBackend(osName string) backend {
	if osName == "windows" {
		return registryBackend{}
	}
	return stateBackend{}
}

// registryBackend reads and writes HKCU\Environment.
type registryBackend struct{}

// shellManaged implements backend. Windows has no shell file to regenerate.
func (registryBackend) shellManaged() bool { return false }

// variable implements backend.
func (registryBackend) variable(name string) (string, bool) {
	key, err := registry.OpenKey(registry.CURRENT_USER, environmentKey, registry.QUERY_VALUE)
	if err != nil {
		return "", false
	}
	defer key.Close()

	value, _, err := key.GetStringValue(name)
	if err != nil {
		return "", false
	}
	return value, true
}

// path implements backend, reading the user's PATH. The machine PATH is
// covered separately by the process environment Bento inherited.
func (registryBackend) path() []string {
	value, _, err := readUserPath()
	if err != nil {
		return nil
	}
	return splitPath(value)
}

func readUserPath() (string, uint32, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, environmentKey, registry.QUERY_VALUE)
	if err != nil {
		return "", 0, err
	}
	defer key.Close()

	value, valueType, err := key.GetStringValue("Path")
	if err != nil {
		if err == registry.ErrNotExist {
			// A user with no personal PATH at all is unusual but legal.
			return "", registry.EXPAND_SZ, nil
		}
		return "", 0, err
	}
	return value, valueType, nil
}

// setVariable implements backend.
func (registryBackend) setVariable(name, value string) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, environmentKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("opening HKCU\\%s: %w", environmentKey, err)
	}
	defer key.Close()

	if err := key.SetStringValue(name, value); err != nil {
		return fmt.Errorf("setting %s: %w", name, err)
	}
	broadcastEnvironmentChange()
	return nil
}

// addPath implements backend. It appends a directory to the user's PATH,
// preserving everything already there and keeping the original value type:
// writing a plain string over a REG_EXPAND_SZ PATH would break any %VAR%
// references the user has.
func (registryBackend) addPath(dir string) error {
	current, valueType, err := readUserPath()
	if err != nil {
		return fmt.Errorf("reading user PATH: %w", err)
	}

	entries, changed := MergePath(splitPath(current), dir, true)
	if !changed {
		return nil
	}

	key, _, err := registry.CreateKey(registry.CURRENT_USER, environmentKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("opening HKCU\\%s: %w", environmentKey, err)
	}
	defer key.Close()

	joined := strings.Join(entries, ";")
	if valueType == registry.SZ {
		err = key.SetStringValue("Path", joined)
	} else {
		err = key.SetExpandStringValue("Path", joined)
	}
	if err != nil {
		return fmt.Errorf("writing user PATH: %w", err)
	}

	broadcastEnvironmentChange()
	return nil
}

func splitPath(value string) []string {
	var entries []string
	for _, entry := range strings.Split(value, ";") {
		if trimmed := strings.TrimSpace(entry); trimmed != "" {
			entries = append(entries, trimmed)
		}
	}
	return entries
}

// broadcastEnvironmentChange tells running programs that the environment
// changed. Explorer and newly-launched shells pick the change up; processes
// that are already running keep their copy, which is why Bento still tells the
// user to open a new terminal.
func broadcastEnvironmentChange() {
	const (
		hwndBroadcast   = 0xffff
		wmSettingChange = 0x001A
		smtoAbortIfHung = 0x0002
	)

	user32 := windows.NewLazySystemDLL("user32.dll")
	sendMessageTimeout := user32.NewProc("SendMessageTimeoutW")

	message, err := windows.UTF16PtrFromString("Environment")
	if err != nil {
		return
	}
	var result uintptr
	// Failure here is not worth surfacing: the registry write already
	// succeeded, and the notice Bento prints covers the user either way.
	_, _, _ = sendMessageTimeout.Call(
		uintptr(hwndBroadcast),
		uintptr(wmSettingChange),
		0,
		uintptr(unsafe.Pointer(message)),
		uintptr(smtoAbortIfHung),
		uintptr(5000),
		uintptr(unsafe.Pointer(&result)),
	)
}
