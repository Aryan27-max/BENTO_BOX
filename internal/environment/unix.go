//go:build !windows

package environment

// platformBackend selects where environment changes are written. On a non
// Windows host there is only one possibility, because the Windows registry is
// not reachable from here.
func platformBackend(string) backend { return stateBackend{} }
