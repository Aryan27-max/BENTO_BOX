// Package resolver turns a profile choice into an ordered, deduplicated,
// platform-filtered list of dependencies.
//
// The pipeline is:
//
//	profile → expand includes → collect dependencies → close over requires
//	        → deduplicate → filter by platform, architecture and hardware
//	        → topological sort → installation order
//
// Nothing here touches the machine. Resolution is pure computation over the
// catalog and the detected system, which is what makes it straightforward to
// test exhaustively.
package resolver

import (
	"fmt"
	"sort"

	"github.com/Aryan27-max/bento-box/internal/catalog"
	"github.com/Aryan27-max/bento-box/internal/dependency"
	"github.com/Aryan27-max/bento-box/internal/profiles"
)

// Request describes what to resolve and for which machine.
type Request struct {
	Catalog *catalog.Catalog
	// Profile is the profile identifier or alias the user chose.
	Profile string
	// OS is the Go-style operating system name: windows, linux or darwin.
	OS string
	// Arch is the Go-style architecture name: amd64 or arm64.
	Arch string
	// NvidiaGPU reports whether an NVIDIA GPU was actually detected. Hardware
	// gated dependencies are only planned when this is true.
	NvidiaGPU bool
}

// Item is one dependency in a resolution, together with why it ended up where
// it did.
type Item struct {
	Spec dependency.Spec
	// Reason explains exclusion for unsupported and skipped items.
	Reason string
	// Direct reports whether the dependency came from the chosen profile
	// rather than being pulled in as a prerequisite.
	Direct bool
}

// Resolution is the outcome of resolving a profile against a system.
type Resolution struct {
	Profile profiles.Profile
	// ProfileIDs are the profiles that contributed dependencies, which for
	// NUKE is every profile.
	ProfileIDs []string
	// Ordered holds the installable dependencies in topological order:
	// prerequisites always appear before the things that need them.
	Ordered []Item
	// Unsupported holds dependencies that cannot be installed on this
	// platform, each with an honest reason.
	Unsupported []Item
	// Skipped holds dependencies deliberately left out — hardware-gated
	// tooling on machines without the hardware, and anything whose
	// prerequisite is unavailable.
	Skipped []Item
}

// Total returns the number of dependencies considered.
func (r Resolution) Total() int { return len(r.Ordered) + len(r.Unsupported) + len(r.Skipped) }

// Names returns the ordered installable dependency names, which is the most
// convenient form for tests.
func (r Resolution) Names() []string {
	out := make([]string, 0, len(r.Ordered))
	for _, item := range r.Ordered {
		out = append(out, item.Spec.Name)
	}
	return out
}

// Resolve computes the installation plan skeleton for a request.
func Resolve(request Request) (Resolution, error) {
	if request.Catalog == nil {
		return Resolution{}, fmt.Errorf("resolver: no catalog")
	}
	if request.OS == "" || request.Arch == "" {
		return Resolution{}, fmt.Errorf("resolver: system not detected")
	}

	profileSet := request.Catalog.Profiles()
	profile, ok := profileSet.Lookup(request.Profile)
	if !ok {
		return Resolution{}, fmt.Errorf("unknown profile %q (choose one of: %v)", request.Profile, profileSet.IDs())
	}
	profileIDs, err := profileSet.Expand(profile.ID)
	if err != nil {
		return Resolution{}, err
	}

	// Collect the profile's dependencies, then close over requires edges so
	// prerequisites outside the profile (npm needs node; foundry needs rust)
	// are pulled in exactly once.
	selected := map[string]bool{}
	direct := map[string]bool{}
	var order []string

	var include func(name string, isDirect bool) error
	include = func(name string, isDirect bool) error {
		spec, ok := request.Catalog.Lookup(name)
		if !ok {
			return fmt.Errorf("unknown dependency %q", name)
		}
		if isDirect {
			direct[name] = true
		}
		if selected[name] {
			return nil
		}
		selected[name] = true
		order = append(order, name)
		for _, required := range spec.Requires {
			if err := include(required, false); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
		}
		return nil
	}

	for _, spec := range request.Catalog.ForProfiles(profileIDs) {
		if err := include(spec.Name, true); err != nil {
			return Resolution{}, err
		}
	}
	sort.Strings(order)

	resolution := Resolution{Profile: profile, ProfileIDs: profileIDs}

	// Partition into installable, unsupported and skipped. Hardware gating
	// happens here so CUDA is never planned on a machine without an NVIDIA
	// GPU, rather than being planned and then failing.
	installable := map[string]dependency.Spec{}
	excluded := map[string]string{}

	for _, name := range order {
		spec, _ := request.Catalog.Lookup(name)
		item := Item{Spec: spec, Direct: direct[name]}

		if reason := spec.UnsupportedReason(request.OS, request.Arch); reason != "" {
			item.Reason = reason
			resolution.Unsupported = append(resolution.Unsupported, item)
			excluded[name] = reason
			continue
		}
		if spec.RequiresGPU == "nvidia" && !request.NvidiaGPU {
			item.Reason = "no NVIDIA GPU detected on this machine"
			resolution.Skipped = append(resolution.Skipped, item)
			excluded[name] = item.Reason
			continue
		}
		installable[name] = spec
	}

	// A dependency whose prerequisite is unavailable cannot be installed
	// either. Propagate exclusion transitively so the reason stays truthful
	// all the way down the chain.
	for changed := true; changed; {
		changed = false
		for name, spec := range installable {
			for _, required := range spec.Requires {
				if _, blocked := excluded[required]; !blocked {
					continue
				}
				requiredSpec, _ := request.Catalog.Lookup(required)
				reason := fmt.Sprintf("requires %s, which is unavailable here (%s)", requiredSpec.Label(), excluded[required])
				resolution.Skipped = append(resolution.Skipped, Item{Spec: spec, Reason: reason, Direct: direct[name]})
				excluded[name] = reason
				delete(installable, name)
				changed = true
				break
			}
		}
	}

	ordered, err := topologicalSort(installable)
	if err != nil {
		return Resolution{}, err
	}
	for _, spec := range ordered {
		resolution.Ordered = append(resolution.Ordered, Item{Spec: spec, Direct: direct[spec.Name]})
	}

	sortItems(resolution.Unsupported)
	sortItems(resolution.Skipped)
	return resolution, nil
}

// topologicalSort orders dependencies so every prerequisite precedes the
// dependency that needs it. Ties are broken alphabetically, which makes the
// installation order stable across runs — a user re-running Bento sees the
// same sequence, and the tests can assert on it.
func topologicalSort(specs map[string]dependency.Spec) ([]dependency.Spec, error) {
	inDegree := make(map[string]int, len(specs))
	dependents := make(map[string][]string, len(specs))

	for name := range specs {
		if _, ok := inDegree[name]; !ok {
			inDegree[name] = 0
		}
	}
	for name, spec := range specs {
		for _, required := range spec.Requires {
			// Prerequisites that were filtered out are already accounted for
			// by the exclusion pass; ignore edges pointing outside the set.
			if _, present := specs[required]; !present {
				continue
			}
			inDegree[name]++
			dependents[required] = append(dependents[required], name)
		}
	}

	var ready []string
	for name, degree := range inDegree {
		if degree == 0 {
			ready = append(ready, name)
		}
	}
	sort.Strings(ready)

	out := make([]dependency.Spec, 0, len(specs))
	for len(ready) > 0 {
		name := ready[0]
		ready = ready[1:]
		out = append(out, specs[name])

		var unlocked []string
		for _, dependent := range dependents[name] {
			inDegree[dependent]--
			if inDegree[dependent] == 0 {
				unlocked = append(unlocked, dependent)
			}
		}
		if len(unlocked) > 0 {
			ready = append(ready, unlocked...)
			sort.Strings(ready)
		}
	}

	if len(out) != len(specs) {
		var remaining []string
		for name := range specs {
			if inDegree[name] > 0 {
				remaining = append(remaining, name)
			}
		}
		sort.Strings(remaining)
		return nil, fmt.Errorf("dependency cycle among %v", remaining)
	}
	return out, nil
}

func sortItems(items []Item) {
	sort.Slice(items, func(i, j int) bool { return items[i].Spec.Name < items[j].Spec.Name })
}
