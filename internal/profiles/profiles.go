// Package profiles defines the developer profiles a user chooses between.
// Profiles are the only choice Bento asks the user to make: they decide which
// dependencies are pulled from the catalog, and the resolver does the rest.
package profiles

import (
	"fmt"
	"sort"
	"strings"
)

// Core is the implicit profile inherited by every other profile. It holds the
// tooling any developer needs regardless of what they are building.
const Core = "core"

// Profile is a named bundle of dependencies.
type Profile struct {
	// ID is the stable identifier used on the command line and in reports.
	ID string `json:"id"`
	// Name is the human-facing name.
	Name string `json:"name"`
	// Emoji brands the profile in the selector.
	Emoji string `json:"emoji"`
	// Description is the one-line explanation shown under the name.
	Description string `json:"description"`
	// Includes lists other profiles absorbed by this one. NUKE uses this to
	// become the union of every other profile without duplicating any data.
	Includes []string `json:"includes,omitempty"`
	// Aliases are alternative identifiers accepted on the command line.
	Aliases []string `json:"aliases,omitempty"`
	// Order controls the position in the selector.
	Order int `json:"order"`
}

// Label renders the profile as it appears in menus.
func (p Profile) Label() string {
	if p.Emoji == "" {
		return p.Name
	}
	return p.Emoji + "  " + p.Name
}

// Set is a collection of profiles that can be looked up and expanded.
type Set struct {
	byID    map[string]Profile
	ordered []Profile
}

// NewSet builds a Set, rejecting duplicates and unknown includes so a broken
// profile definition cannot reach a user.
func NewSet(list []Profile) (*Set, error) {
	set := &Set{byID: make(map[string]Profile, len(list))}
	for _, profile := range list {
		if profile.ID == "" {
			return nil, fmt.Errorf("profile with empty id")
		}
		if _, exists := set.byID[profile.ID]; exists {
			return nil, fmt.Errorf("duplicate profile %q", profile.ID)
		}
		set.byID[profile.ID] = profile
	}
	for _, profile := range list {
		for _, include := range profile.Includes {
			if _, ok := set.byID[include]; !ok {
				return nil, fmt.Errorf("profile %q includes unknown profile %q", profile.ID, include)
			}
		}
	}

	set.ordered = append(set.ordered, list...)
	sort.SliceStable(set.ordered, func(i, j int) bool { return set.ordered[i].Order < set.ordered[j].Order })
	return set, nil
}

// All returns every profile in display order.
func (s *Set) All() []Profile {
	out := make([]Profile, len(s.ordered))
	copy(out, s.ordered)
	return out
}

// Selectable returns the profiles offered in the interactive menu, which
// excludes the implicit core profile.
func (s *Set) Selectable() []Profile {
	var out []Profile
	for _, profile := range s.ordered {
		if profile.ID != Core {
			out = append(out, profile)
		}
	}
	return out
}

// Lookup finds a profile by ID or alias, case-insensitively.
func (s *Set) Lookup(name string) (Profile, bool) {
	needle := strings.ToLower(strings.TrimSpace(name))
	if profile, ok := s.byID[needle]; ok {
		return profile, true
	}
	for _, profile := range s.ordered {
		for _, alias := range profile.Aliases {
			if strings.ToLower(alias) == needle {
				return profile, true
			}
		}
	}
	return Profile{}, false
}

// Expand returns the full set of profile IDs implied by a selection: the
// profile itself, everything it includes (transitively) and the implicit core
// profile. This is where NUKE becomes the union of every other profile, and
// where deduplication starts — the result is a set, so a dependency reachable
// through three profiles is still requested once.
func (s *Set) Expand(id string) ([]string, error) {
	profile, ok := s.Lookup(id)
	if !ok {
		return nil, fmt.Errorf("unknown profile %q (known: %s)", id, strings.Join(s.IDs(), ", "))
	}

	seen := map[string]bool{}
	var walk func(string) error
	walk = func(current string) error {
		if seen[current] {
			return nil
		}
		seen[current] = true
		included, ok := s.byID[current]
		if !ok {
			return fmt.Errorf("unknown profile %q", current)
		}
		for _, child := range included.Includes {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(profile.ID); err != nil {
		return nil, err
	}
	// Every profile inherits core tooling.
	if _, hasCore := s.byID[Core]; hasCore {
		seen[Core] = true
	}

	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

// IDs returns every profile identifier in display order.
func (s *Set) IDs() []string {
	out := make([]string, 0, len(s.ordered))
	for _, profile := range s.ordered {
		out = append(out, profile.ID)
	}
	return out
}
