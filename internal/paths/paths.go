// Package paths holds the small amount of path handling shared between the
// verifier, the environment writer and the installer: expanding the
// placeholders used in dependency definitions, and locating Bento's own
// working directory.
//
// Everything that interprets a path comes in two forms. The plain form —
// Expand, Normalise — works in the flavour of the platform Bento is running
// on, which is what anything naming a real directory on this machine wants.
// The "For" form takes the target platform explicitly, because much of what
// Bento handles is path *data* belonging to a named operating system: the
// Windows entry for VS Code is "${LOCALAPPDATA}/Programs/Microsoft VS
// Code/Code.exe" whether or not Bento is running on Windows.
//
// That distinction has to be honoured here rather than delegated, because
// path/filepath is fixed at build time. On Windows both "/" and "\" separate
// elements and a path may carry a volume name; on Linux and macOS a backslash
// is an ordinary character in a filename and carries no structure at all. So
// filepath cannot answer a question about Windows paths on Linux, and this
// package implements both flavours directly instead of asking it to.
package paths

import (
	"os"
	"path"
	"runtime"
	"strings"
)

// HomeDirName is the directory Bento creates under the user's home for
// archives it unpacks, logs and reports.
const HomeDirName = ".bento"

// hostIsWindows is the flavour of the platform this binary was built for. It
// is the only place the host leaks into this package.
var hostIsWindows = runtime.GOOS == "windows"

// Home returns Bento's working directory for a given user home.
func Home(userHome string) string { return joinFor(hostIsWindows, userHome, HomeDirName) }

// OptDir returns the directory where archive installations are unpacked. Each
// dependency gets its own subdirectory so an upgrade replaces only itself.
func OptDir(userHome string) string { return joinFor(hostIsWindows, Home(userHome), "opt") }

// ExpandFor replaces ${NAME} placeholders using lookup and writes the result
// with the separators of the target platform. Unknown placeholders are left
// untouched rather than replaced with an empty string, because silently
// turning "${ANDROID_HOME}/platform-tools" into "/platform-tools" would put a
// nonsense entry on the user's PATH.
func ExpandFor(value string, lookup func(string) string, windows bool) string {
	if lookup == nil {
		lookup = os.Getenv
	}

	var builder strings.Builder
	for {
		start := strings.Index(value, "${")
		if start < 0 {
			builder.WriteString(value)
			break
		}
		end := strings.Index(value[start:], "}")
		if end < 0 {
			builder.WriteString(value)
			break
		}
		end += start

		builder.WriteString(value[:start])
		name := value[start+2 : end]
		if replacement := lookup(name); replacement != "" {
			builder.WriteString(replacement)
		} else {
			builder.WriteString(value[start : end+1])
		}
		value = value[end+1:]
	}

	return FromSlashFor(builder.String(), windows)
}

// Expand is ExpandFor in the flavour of the platform Bento is running on.
func Expand(value string, lookup func(string) string) string {
	return ExpandFor(value, lookup, hostIsWindows)
}

// LookupFor builds the placeholder resolver used for dependency definitions.
// It understands the variables Bento documents — HOME, BENTO_HOME and the
// Windows well-known folders — and falls back to the process environment for
// anything else, which is how ANDROID_HOME resolves once it has been set. The
// target platform matters only for the fallbacks that build a path themselves.
func LookupFor(userHome string, getenv func(string) string, windows bool) func(string) string {
	if getenv == nil {
		getenv = os.Getenv
	}
	return func(name string) string {
		switch strings.ToUpper(name) {
		case "HOME", "USERPROFILE":
			if userHome != "" {
				return userHome
			}
		case "BENTO_HOME":
			if userHome != "" {
				return joinFor(windows, userHome, HomeDirName)
			}
		case "LOCALAPPDATA":
			if value := getenv("LOCALAPPDATA"); value != "" {
				return value
			}
			if userHome != "" {
				return joinFor(windows, userHome, "AppData", "Local")
			}
		case "APPDATA":
			if value := getenv("APPDATA"); value != "" {
				return value
			}
			if userHome != "" {
				return joinFor(windows, userHome, "AppData", "Roaming")
			}
		case "PROGRAMFILES":
			if value := getenv("ProgramFiles"); value != "" {
				return value
			}
			return `C:\Program Files`
		case "PROGRAMFILES(X86)":
			if value := getenv("ProgramFiles(x86)"); value != "" {
				return value
			}
			return `C:\Program Files (x86)`
		}
		return getenv(name)
	}
}

// Lookup is LookupFor in the flavour of the platform Bento is running on.
func Lookup(userHome string, getenv func(string) string) func(string) string {
	return LookupFor(userHome, getenv, hostIsWindows)
}

// NormaliseFor cleans a path for comparison in the flavour of the target
// platform, so that "C:/Go/bin/" and "C:\Go\bin" are recognised as the same
// directory on Windows — and stay two different strings on Linux, where a
// backslash is just a character in a name. This is what stops Bento from
// appending a PATH entry it has already added.
func NormaliseFor(value string, windows bool) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	return CleanFor(trimmed, windows)
}

// Normalise cleans a path in the flavour of the platform Bento is running on.
func Normalise(value string) string { return NormaliseFor(value, hostIsWindows) }

// SameEntry reports whether two PATH entries refer to the same directory.
// Comparison is case-insensitive on Windows, where paths are.
func SameEntry(left, right string, windows bool) bool {
	left, right = NormaliseFor(left, windows), NormaliseFor(right, windows)
	if windows {
		return strings.EqualFold(left, right)
	}
	return left == right
}

// FromSlashFor rewrites forward slashes into the separator of the target
// platform. It is filepath.FromSlash for an explicitly named platform.
func FromSlashFor(value string, windows bool) string {
	if windows {
		return strings.ReplaceAll(value, "/", `\`)
	}
	return value
}

// CleanFor is filepath.Clean for an explicitly named platform: it collapses
// repeated separators, resolves "." and "..", and drops a trailing separator.
// On Windows it accepts both separators, writes back the Windows one, and
// keeps the volume name intact. Cleaning already removes a trailing separator,
// so a bare root such as "/" or `C:\` keeps the separator that gives it its
// meaning; a path that is nothing but a volume ("C:", `\\server\share`) is
// returned as it stands.
func CleanFor(value string, windows bool) string {
	if value == "" {
		return "."
	}
	if !windows {
		// filepath.Clean on Linux and macOS is exactly this: a lexical clean
		// in which "/" is the only separator.
		return path.Clean(value)
	}

	volume := value[:volumeNameLen(value)]
	rest := strings.ReplaceAll(value[len(volume):], `\`, "/")
	volume = strings.ReplaceAll(volume, "/", `\`)
	if rest == "" {
		return volume
	}
	return volume + strings.ReplaceAll(path.Clean(rest), "/", `\`)
}

// joinFor joins elements with the separator of the target platform and cleans
// the result. It is filepath.Join for an explicitly named platform.
func joinFor(windows bool, elements ...string) string {
	separator := "/"
	if windows {
		separator = `\`
	}

	present := make([]string, 0, len(elements))
	for _, element := range elements {
		if element != "" {
			present = append(present, element)
		}
	}
	if len(present) == 0 {
		return ""
	}
	return CleanFor(strings.Join(present, separator), windows)
}

// volumeNameLen returns the length of the volume name leading a Windows path:
// the drive letter in "C:\Go\bin", or the host and share in
// "\\server\share\bin". It mirrors what path/filepath does on Windows, which
// is of no use here because this has to give the same answer on Linux.
func volumeNameLen(value string) int {
	if len(value) >= 2 && value[1] == ':' && isDriveLetter(value[0]) {
		return 2
	}
	if len(value) < 2 || !isWindowsSeparator(value[0]) || !isWindowsSeparator(value[1]) {
		return 0
	}

	// A UNC path: the volume is the whole of "\\host\share", so a comparison
	// never mistakes two shares on the same server for one directory.
	host := scanElement(value, 2)
	if host == 2 || host == len(value) {
		return 0
	}
	share := scanElement(value, host+1)
	if share == host+1 {
		return 0
	}
	return share
}

func scanElement(value string, from int) int {
	for from < len(value) && !isWindowsSeparator(value[from]) {
		from++
	}
	return from
}

func isWindowsSeparator(c byte) bool { return c == '\\' || c == '/' }

func isDriveLetter(c byte) bool {
	return ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}
