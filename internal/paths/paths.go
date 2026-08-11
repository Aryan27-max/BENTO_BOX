// Package paths holds the small amount of path handling shared between the
// verifier, the environment writer and the installer: expanding the
// placeholders used in dependency definitions, and locating Bento's own
// working directory.
package paths

import (
	"os"
	"path/filepath"
	"strings"
)

// HomeDirName is the directory Bento creates under the user's home for
// archives it unpacks, logs and reports.
const HomeDirName = ".bento"

// Home returns Bento's working directory for a given user home.
func Home(userHome string) string { return filepath.Join(userHome, HomeDirName) }

// OptDir returns the directory where archive installations are unpacked. Each
// dependency gets its own subdirectory so an upgrade replaces only itself.
func OptDir(userHome string) string { return filepath.Join(Home(userHome), "opt") }

// Expand replaces ${NAME} placeholders using lookup. Unknown placeholders are
// left untouched rather than replaced with an empty string, because silently
// turning "${ANDROID_HOME}/platform-tools" into "/platform-tools" would put a
// nonsense entry on the user's PATH.
func Expand(value string, lookup func(string) string) string {
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

	return filepath.FromSlash(builder.String())
}

// Lookup builds the placeholder resolver used for dependency definitions. It
// understands the variables Bento documents — HOME, BENTO_HOME and the Windows
// well-known folders — and falls back to the process environment for anything
// else, which is how ANDROID_HOME resolves once it has been set.
func Lookup(userHome string, getenv func(string) string) func(string) string {
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
				return Home(userHome)
			}
		case "LOCALAPPDATA":
			if value := getenv("LOCALAPPDATA"); value != "" {
				return value
			}
			if userHome != "" {
				return filepath.Join(userHome, "AppData", "Local")
			}
		case "APPDATA":
			if value := getenv("APPDATA"); value != "" {
				return value
			}
			if userHome != "" {
				return filepath.Join(userHome, "AppData", "Roaming")
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

// Normalise cleans a path for comparison: it resolves separators and strips a
// trailing separator so that "C:\Go\bin" and "C:/Go/bin/" are recognised as
// the same directory. This is what stops Bento from appending a PATH entry it
// has already added.
func Normalise(path string) string {
	if path == "" {
		return ""
	}
	cleaned := filepath.Clean(filepath.FromSlash(strings.TrimSpace(path)))
	return strings.TrimRight(cleaned, string(filepath.Separator))
}

// SameEntry reports whether two PATH entries refer to the same directory.
// Comparison is case-insensitive on Windows, where paths are.
func SameEntry(left, right string, windows bool) bool {
	left, right = Normalise(left), Normalise(right)
	if windows {
		return strings.EqualFold(left, right)
	}
	return left == right
}
