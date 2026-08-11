// Package version parses and compares the wildly inconsistent version strings
// that development tools print. It deliberately implements a forgiving subset
// of semantic versioning: real tools emit "go version go1.26.4 windows/amd64",
// "Python 3.13.1", "v22.14.0" and "git version 2.47.1.windows.1", and Bento has
// to make sense of all of them without pretending they are strict semver.
package version

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ErrNoVersion reports that no version-shaped substring could be found.
var ErrNoVersion = errors.New("no version found in output")

// Version is a parsed, comparable version.
type Version struct {
	Major int
	Minor int
	Patch int
	// Pre holds a pre-release identifier such as "beta1" or "rc2". It is empty
	// for stable releases.
	Pre string
	// Raw is the exact substring that was parsed, useful for display.
	Raw string
}

// pattern matches a dotted numeric version with an optional pre-release
// suffix. The suffix must be attached with '-' or '.' followed by a letter so
// that "2.47.1.windows.1" parses as 2.47.1 with pre-release "windows.1" while
// "1.2.3.4" keeps its numeric tail out of the pre-release field.
var pattern = regexp.MustCompile(`(\d+)\.(\d+)(?:\.(\d+))?(?:[-_.]?([A-Za-z][0-9A-Za-z.\-+]*))?`)

// Parse extracts the first version-shaped substring from s. Leading "v" and
// tool names are ignored, so both "v22.14.0" and "Python 3.13.1" work.
func Parse(s string) (Version, error) {
	match := pattern.FindStringSubmatch(s)
	if match == nil {
		return Version{}, fmt.Errorf("%w: %q", ErrNoVersion, strings.TrimSpace(s))
	}

	version := Version{Raw: match[0]}
	version.Major, _ = strconv.Atoi(match[1])
	version.Minor, _ = strconv.Atoi(match[2])
	if match[3] != "" {
		version.Patch, _ = strconv.Atoi(match[3])
	}
	version.Pre = strings.Trim(match[4], ".-_")
	return version, nil
}

// MustParse is Parse for constants in tests and table data.
func MustParse(s string) Version {
	version, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return version
}

// String renders the version in canonical major.minor.patch form.
func (v Version) String() string {
	base := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Pre != "" {
		return base + "-" + v.Pre
	}
	return base
}

// Short renders major.minor, the granularity used in summary reports.
func (v Version) Short() string { return fmt.Sprintf("%d.%d", v.Major, v.Minor) }

// IsZero reports whether the version carries no information.
func (v Version) IsZero() bool {
	return v.Major == 0 && v.Minor == 0 && v.Patch == 0 && v.Pre == ""
}

// preReleaseMarkers are the tokens that identify a build as not-stable. Bento's
// version policy (install the latest *stable* release) depends on recognising
// them.
var preReleaseMarkers = []string{"alpha", "beta", "rc", "nightly", "dev", "preview", "snapshot", "canary", "insiders"}

// Stable reports whether the version looks like a stable release rather than a
// pre-release. Vendor-specific suffixes such as "windows.1" in Git for Windows
// are treated as stable because they are packaging metadata, not pre-releases.
func (v Version) Stable() bool {
	pre := strings.ToLower(v.Pre)
	if pre == "" {
		return true
	}
	for _, marker := range preReleaseMarkers {
		if strings.HasPrefix(pre, marker) {
			return false
		}
	}
	return true
}

// Compare returns -1 if v sorts before other, 0 if they are equal and +1 if v
// sorts after other. A pre-release sorts before the equivalent stable release,
// matching semver ordering.
func (v Version) Compare(other Version) int {
	for _, pair := range [][2]int{
		{v.Major, other.Major},
		{v.Minor, other.Minor},
		{v.Patch, other.Patch},
	} {
		if pair[0] != pair[1] {
			if pair[0] < pair[1] {
				return -1
			}
			return 1
		}
	}

	switch vStable, otherStable := v.Stable(), other.Stable(); {
	case vStable && !otherStable:
		return 1
	case !vStable && otherStable:
		return -1
	}
	return strings.Compare(strings.ToLower(v.Pre), strings.ToLower(other.Pre))
}

// AtLeast reports whether v is greater than or equal to minimum.
func (v Version) AtLeast(minimum Version) bool { return v.Compare(minimum) >= 0 }

// Satisfies reports whether v meets a minimum version expressed as a string.
// An empty or unparseable constraint means "no constraint", which is the
// common case: most dependencies simply want the latest stable release.
func (v Version) Satisfies(minimum string) bool {
	if strings.TrimSpace(minimum) == "" {
		return true
	}
	want, err := Parse(minimum)
	if err != nil {
		return true
	}
	return v.AtLeast(want)
}

// Extract pulls a version out of command output using an optional
// tool-specific regular expression. When the expression is empty the generic
// parser is used. The expression must contain at least one capturing group,
// whose first match is parsed.
func Extract(output, expression string) (Version, error) {
	if strings.TrimSpace(expression) == "" {
		return Parse(output)
	}
	re, err := regexp.Compile(expression)
	if err != nil {
		return Version{}, fmt.Errorf("invalid version pattern %q: %w", expression, err)
	}
	match := re.FindStringSubmatch(output)
	if match == nil {
		return Version{}, fmt.Errorf("%w: pattern %q did not match", ErrNoVersion, expression)
	}
	if len(match) > 1 {
		return Parse(match[1])
	}
	return Parse(match[0])
}

// Latest returns the highest version in the list. It reports false when the
// list is empty.
func Latest(versions []Version) (Version, bool) {
	if len(versions) == 0 {
		return Version{}, false
	}
	best := versions[0]
	for _, candidate := range versions[1:] {
		if candidate.Compare(best) > 0 {
			best = candidate
		}
	}
	return best, true
}
