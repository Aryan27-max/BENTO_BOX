// Package catalog loads and validates Bento's dependency and profile
// definitions. Every consistency rule that could otherwise blow up on a user's
// machine — an unknown category, a dangling requires edge, a dependency in a
// profile that does not exist — is enforced here at load time and exercised by
// a test that runs against the shipped catalog.
package catalog

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	bentoconfig "github.com/Aryan27-max/bento-box/config"
	"github.com/Aryan27-max/bento-box/internal/dependency"
	"github.com/Aryan27-max/bento-box/internal/profiles"
)

// Catalog is the validated set of everything Bento knows how to install.
type Catalog struct {
	specs    []dependency.Spec
	byName   map[string]dependency.Spec
	profiles *profiles.Set
}

// Load reads the catalog embedded in the binary.
func Load() (*Catalog, error) { return LoadFS(bentoconfig.FS) }

// LoadFS reads a catalog from any filesystem, which lets tests supply small
// fixtures instead of the real data.
func LoadFS(fsys fs.FS) (*Catalog, error) {
	profileList, err := loadProfiles(fsys)
	if err != nil {
		return nil, err
	}
	profileSet, err := profiles.NewSet(profileList)
	if err != nil {
		return nil, fmt.Errorf("profiles: %w", err)
	}

	specs, err := loadDependencies(fsys)
	if err != nil {
		return nil, err
	}

	catalog := &Catalog{
		specs:    specs,
		byName:   make(map[string]dependency.Spec, len(specs)),
		profiles: profileSet,
	}
	for _, spec := range specs {
		if _, duplicate := catalog.byName[spec.Name]; duplicate {
			return nil, fmt.Errorf("duplicate dependency %q", spec.Name)
		}
		catalog.byName[spec.Name] = spec
	}

	if err := catalog.validate(); err != nil {
		return nil, err
	}
	return catalog, nil
}

func loadProfiles(fsys fs.FS) ([]profiles.Profile, error) {
	data, err := fs.ReadFile(fsys, bentoconfig.ProfilesFile)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", bentoconfig.ProfilesFile, err)
	}
	var list []profiles.Profile
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", bentoconfig.ProfilesFile, err)
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("%s defines no profiles", bentoconfig.ProfilesFile)
	}
	return list, nil
}

func loadDependencies(fsys fs.FS) ([]dependency.Spec, error) {
	files, err := fs.Glob(fsys, bentoconfig.DependenciesGlob)
	if err != nil {
		return nil, fmt.Errorf("listing dependency files: %w", err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no dependency definitions found matching %s", bentoconfig.DependenciesGlob)
	}
	sort.Strings(files)

	var specs []dependency.Spec
	for _, file := range files {
		data, err := fs.ReadFile(fsys, file)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", file, err)
		}
		var batch []dependency.Spec
		decoder := json.NewDecoder(strings.NewReader(string(data)))
		// Reject unknown fields so a typo in the catalog is caught at load
		// time rather than silently ignored.
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&batch); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", file, err)
		}
		specs = append(specs, batch...)
	}
	dependency.SortSpecs(specs)
	return specs, nil
}

func (c *Catalog) validate() error {
	knownProfiles := map[string]bool{}
	for _, profile := range c.profiles.All() {
		knownProfiles[profile.ID] = true
	}

	for _, spec := range c.specs {
		if err := spec.Validate(); err != nil {
			return err
		}
		for _, profile := range spec.Profiles {
			if !knownProfiles[profile] {
				return fmt.Errorf("%s: unknown profile %q", spec.Name, profile)
			}
		}
		for _, required := range spec.Requires {
			if _, ok := c.byName[required]; !ok {
				return fmt.Errorf("%s: requires unknown dependency %q", spec.Name, required)
			}
		}
	}
	return c.checkForCycles()
}

// checkForCycles rejects a catalog whose requires edges contain a loop, which
// would otherwise make installation order undefined.
func (c *Catalog) checkForCycles() error {
	const (
		unvisited = 0
		visiting  = 1
		done      = 2
	)
	state := make(map[string]int, len(c.specs))

	var walk func(string, []string) error
	walk = func(name string, path []string) error {
		switch state[name] {
		case done:
			return nil
		case visiting:
			return fmt.Errorf("dependency cycle: %s", strings.Join(append(path, name), " → "))
		}
		state[name] = visiting
		for _, required := range c.byName[name].Requires {
			if err := walk(required, append(path, name)); err != nil {
				return err
			}
		}
		state[name] = done
		return nil
	}

	names := make([]string, 0, len(c.byName))
	for name := range c.byName {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := walk(name, nil); err != nil {
			return err
		}
	}
	return nil
}

// All returns every dependency, sorted by name.
func (c *Catalog) All() []dependency.Spec {
	out := make([]dependency.Spec, len(c.specs))
	copy(out, c.specs)
	return out
}

// Lookup finds a dependency by name.
func (c *Catalog) Lookup(name string) (dependency.Spec, bool) {
	spec, ok := c.byName[name]
	return spec, ok
}

// Profiles returns the profile set.
func (c *Catalog) Profiles() *profiles.Set { return c.profiles }

// ForProfiles returns every dependency belonging to any of the given profiles.
// A dependency reachable through several profiles appears exactly once, which
// is the first half of NUKE's deduplication guarantee — the resolver handles
// the second half when it expands requires edges.
func (c *Catalog) ForProfiles(ids []string) []dependency.Spec {
	wanted := make(map[string]bool, len(ids))
	for _, id := range ids {
		wanted[id] = true
	}

	var out []dependency.Spec
	seen := map[string]bool{}
	for _, spec := range c.specs {
		if seen[spec.Name] {
			continue
		}
		for _, profile := range spec.Profiles {
			if wanted[profile] {
				out = append(out, spec)
				seen[spec.Name] = true
				break
			}
		}
	}
	return out
}

// Count returns the number of dependencies in the catalog.
func (c *Catalog) Count() int { return len(c.specs) }
