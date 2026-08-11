package resolver

import (
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Aryan27-max/bento-box/internal/catalog"
)

const testProfiles = `[
	{"id":"core","name":"Core","order":0},
	{"id":"web","name":"Web","order":1},
	{"id":"ai","name":"AI","order":2},
	{"id":"nuke","name":"Nuke","includes":["web","ai"],"order":3}
]`

// testCatalog builds a small catalog whose shape mirrors the real one: a
// shared prerequisite chain, a dependency in two profiles, an OS-restricted
// tool and a hardware-gated tool.
const testDeps = `[
	{"name":"git","display_name":"Git","category":"DEVELOPMENT_TOOL","profiles":["core"],
	 "verify":{"command":"git"},
	 "platforms":{"windows":{"install":[{"method":"package_manager","manager":"winget","packages":["Git.Git"]}]},
	              "linux":{"install":[{"method":"package_manager","manager":"apt","packages":["git"]}]},
	              "darwin":{"install":[{"method":"package_manager","manager":"brew","packages":["git"]}]}}},

	{"name":"python","display_name":"Python","category":"LANGUAGE","profiles":["web","ai"],
	 "verify":{"command":"python3"},
	 "platforms":{"windows":{"install":[{"method":"package_manager","manager":"winget","packages":["Python.Python.3.13"]}]},
	              "linux":{"install":[{"method":"package_manager","manager":"apt","packages":["python3"]}]},
	              "darwin":{"install":[{"method":"package_manager","manager":"brew","packages":["python"]}]}}},

	{"name":"pip","display_name":"pip","category":"PACKAGE_MANAGER","profiles":["ai"],"requires":["python"],
	 "verify":{"command":"pip3"},
	 "platforms":{"windows":{"install":[{"method":"bundled"}]},
	              "linux":{"install":[{"method":"bundled"}]},
	              "darwin":{"install":[{"method":"bundled"}]}}},

	{"name":"jupyter","display_name":"Jupyter","category":"DEVELOPMENT_TOOL","profiles":["ai"],"requires":["pip"],
	 "verify":{"command":"jupyter"},
	 "platforms":{"windows":{"install":[{"method":"language_package","via":"pip","packages":["jupyterlab"]}]},
	              "linux":{"install":[{"method":"language_package","via":"pip","packages":["jupyterlab"]}]},
	              "darwin":{"install":[{"method":"language_package","via":"pip","packages":["jupyterlab"]}]}}},

	{"name":"node","display_name":"Node.js","category":"RUNTIME","profiles":["web"],
	 "verify":{"command":"node"},
	 "platforms":{"windows":{"install":[{"method":"package_manager","manager":"winget","packages":["OpenJS.NodeJS.LTS"]}]},
	              "linux":{"install":[{"method":"package_manager","manager":"apt","packages":["nodejs"]}]},
	              "darwin":{"install":[{"method":"package_manager","manager":"brew","packages":["node"]}]}}},

	{"name":"npm","display_name":"npm","category":"PACKAGE_MANAGER","profiles":["web"],"requires":["node"],
	 "verify":{"command":"npm"},
	 "platforms":{"windows":{"install":[{"method":"bundled"}]},
	              "linux":{"install":[{"method":"bundled"}]},
	              "darwin":{"install":[{"method":"bundled"}]}}},

	{"name":"redis","display_name":"Redis","category":"DATABASE","profiles":["web"],
	 "verify":{"command":"redis-cli"},
	 "unsupported":{"windows":"Redis publishes no official Windows build."},
	 "platforms":{"linux":{"install":[{"method":"package_manager","manager":"apt","packages":["redis-server"]}]},
	              "darwin":{"install":[{"method":"package_manager","manager":"brew","packages":["redis"]}]}}},

	{"name":"redis-tool","display_name":"Redis Tool","category":"CLI_TOOL","profiles":["web"],"requires":["redis"],
	 "verify":{"command":"redis-tool"},
	 "platforms":{"windows":{"install":[{"method":"package_manager","manager":"winget","packages":["x"]}]},
	              "linux":{"install":[{"method":"package_manager","manager":"apt","packages":["redis-tools"]}]},
	              "darwin":{"install":[{"method":"package_manager","manager":"brew","packages":["redis"]}]}}},

	{"name":"cuda","display_name":"CUDA","category":"SDK","profiles":["ai"],"requires_gpu":"nvidia",
	 "verify":{"command":"nvcc"},
	 "unsupported":{"darwin":"CUDA is unsupported on macOS."},
	 "platforms":{"windows":{"install":[{"method":"package_manager","manager":"winget","packages":["Nvidia.CUDA"]}]},
	              "linux":{"install":[{"method":"package_manager","manager":"apt","packages":["nvidia-cuda-toolkit"]}]}}},

	{"name":"bun","display_name":"Bun","category":"RUNTIME","profiles":["web"],
	 "verify":{"command":"bun"},
	 "platforms":{"windows":{"arch":["amd64"],"install":[{"method":"package_manager","manager":"winget","packages":["Oven-sh.Bun"]}]},
	              "linux":{"install":[{"method":"package_manager","manager":"apt","packages":["bun"]}]},
	              "darwin":{"install":[{"method":"package_manager","manager":"brew","packages":["bun"]}]}}}
]`

func testCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	fsys := fstest.MapFS{
		"profiles.json":          {Data: []byte(testProfiles)},
		"dependencies/test.json": {Data: []byte(testDeps)},
	}
	loaded, err := catalog.LoadFS(fsys)
	if err != nil {
		t.Fatalf("LoadFS: %v", err)
	}
	return loaded
}

func resolve(t *testing.T, profile, osName, arch string, gpu bool) Resolution {
	t.Helper()
	resolution, err := Resolve(Request{
		Catalog: testCatalog(t), Profile: profile, OS: osName, Arch: arch, NvidiaGPU: gpu,
	})
	if err != nil {
		t.Fatalf("Resolve(%s/%s/%s): %v", profile, osName, arch, err)
	}
	return resolution
}

func TestCoreIsInheritedByEveryProfile(t *testing.T) {
	for _, profile := range []string{"web", "ai", "nuke"} {
		resolution := resolve(t, profile, "linux", "amd64", false)
		if !slices.Contains(resolution.Names(), "git") {
			t.Errorf("profile %q did not inherit core dependency git: %v", profile, resolution.Names())
		}
	}
}

func TestPrerequisitesArePulledInAndOrderedFirst(t *testing.T) {
	resolution := resolve(t, "ai", "linux", "amd64", false)
	names := resolution.Names()

	// jupyter needs pip needs python, and none of python's prerequisites are
	// in the AI profile directly — they must be pulled in by the closure.
	for _, want := range []string{"python", "pip", "jupyter"} {
		if !slices.Contains(names, want) {
			t.Fatalf("resolution missing %q: %v", want, names)
		}
	}
	python := slices.Index(names, "python")
	pip := slices.Index(names, "pip")
	jupyter := slices.Index(names, "jupyter")
	if !(python < pip && pip < jupyter) {
		t.Errorf("installation order %v does not place python before pip before jupyter", names)
	}
}

func TestOrderIsDeterministic(t *testing.T) {
	first := resolve(t, "nuke", "linux", "amd64", true).Names()
	for i := 0; i < 5; i++ {
		got := resolve(t, "nuke", "linux", "amd64", true).Names()
		if strings.Join(got, ",") != strings.Join(first, ",") {
			t.Fatalf("order changed between runs:\n first: %v\n then:  %v", first, got)
		}
	}
}

func TestNukeDeduplicatesSharedDependencies(t *testing.T) {
	resolution := resolve(t, "nuke", "linux", "amd64", true)

	counts := map[string]int{}
	for _, name := range resolution.Names() {
		counts[name]++
	}
	for name, count := range counts {
		if count != 1 {
			t.Errorf("NUKE plans %q %d times, want 1", name, count)
		}
	}

	// python is reachable through both web and ai; it must appear once.
	if counts["python"] != 1 {
		t.Errorf("python planned %d times, want exactly 1", counts["python"])
	}
}

func TestNukeIsTheUnionOfWebAndAI(t *testing.T) {
	nuke := resolve(t, "nuke", "linux", "amd64", true).Names()
	for _, profile := range []string{"web", "ai"} {
		for _, name := range resolve(t, profile, "linux", "amd64", true).Names() {
			if !slices.Contains(nuke, name) {
				t.Errorf("NUKE is missing %q from profile %q", name, profile)
			}
		}
	}
}

func TestUnsupportedDependenciesAreExcludedWithAReason(t *testing.T) {
	resolution := resolve(t, "web", "windows", "amd64", false)

	if slices.Contains(resolution.Names(), "redis") {
		t.Error("redis should not be installable on Windows")
	}
	var found bool
	for _, item := range resolution.Unsupported {
		if item.Spec.Name == "redis" {
			found = true
			if !strings.Contains(item.Reason, "no official Windows build") {
				t.Errorf("redis reason = %q, want the catalog's explanation", item.Reason)
			}
		}
	}
	if !found {
		t.Error("redis missing from the unsupported list")
	}
}

func TestDependentsOfUnsupportedToolsAreSkippedNotFailed(t *testing.T) {
	resolution := resolve(t, "web", "windows", "amd64", false)

	if slices.Contains(resolution.Names(), "redis-tool") {
		t.Error("redis-tool should not be planned when its prerequisite is unsupported")
	}
	var reason string
	for _, item := range resolution.Skipped {
		if item.Spec.Name == "redis-tool" {
			reason = item.Reason
		}
	}
	if reason == "" {
		t.Fatal("redis-tool missing from the skipped list")
	}
	if !strings.Contains(reason, "Redis") {
		t.Errorf("skip reason %q should name the missing prerequisite", reason)
	}
}

func TestArchitectureFiltering(t *testing.T) {
	amd64 := resolve(t, "web", "windows", "amd64", false)
	if !slices.Contains(amd64.Names(), "bun") {
		t.Error("bun should be planned on windows/amd64")
	}

	arm64 := resolve(t, "web", "windows", "arm64", false)
	if slices.Contains(arm64.Names(), "bun") {
		t.Error("bun should not be planned on windows/arm64")
	}
	var reason string
	for _, item := range arm64.Unsupported {
		if item.Spec.Name == "bun" {
			reason = item.Reason
		}
	}
	if !strings.Contains(reason, "arm64") {
		t.Errorf("bun exclusion reason = %q, want it to mention the architecture", reason)
	}
}

func TestHardwareGatedDependenciesNeedTheHardware(t *testing.T) {
	without := resolve(t, "ai", "linux", "amd64", false)
	if slices.Contains(without.Names(), "cuda") {
		t.Error("CUDA must not be planned without an NVIDIA GPU")
	}
	var reason string
	for _, item := range without.Skipped {
		if item.Spec.Name == "cuda" {
			reason = item.Reason
		}
	}
	if !strings.Contains(reason, "NVIDIA") {
		t.Errorf("cuda skip reason = %q, want it to mention the missing GPU", reason)
	}

	with := resolve(t, "ai", "linux", "amd64", true)
	if !slices.Contains(with.Names(), "cuda") {
		t.Error("CUDA should be planned when an NVIDIA GPU is present")
	}

	// Even with a GPU, macOS cannot run CUDA at all.
	mac := resolve(t, "ai", "darwin", "arm64", true)
	if slices.Contains(mac.Names(), "cuda") {
		t.Error("CUDA must never be planned on macOS")
	}
}

func TestDirectAndTransitiveDependenciesAreDistinguished(t *testing.T) {
	resolution := resolve(t, "ai", "linux", "amd64", false)

	direct := map[string]bool{}
	for _, item := range resolution.Ordered {
		direct[item.Spec.Name] = item.Direct
	}
	if !direct["jupyter"] {
		t.Error("jupyter is in the AI profile and should be marked direct")
	}
	if direct["node"] {
		t.Error("node is not in the AI profile at all")
	}
}

func TestResolveRejectsBadRequests(t *testing.T) {
	loaded := testCatalog(t)

	if _, err := Resolve(Request{Profile: "web", OS: "linux", Arch: "amd64"}); err == nil {
		t.Error("expected an error without a catalog")
	}
	if _, err := Resolve(Request{Catalog: loaded, Profile: "web"}); err == nil {
		t.Error("expected an error without a detected system")
	}
	if _, err := Resolve(Request{Catalog: loaded, Profile: "nonsense", OS: "linux", Arch: "amd64"}); err == nil {
		t.Error("expected an error for an unknown profile")
	}
}

func TestAliasesResolve(t *testing.T) {
	loaded := testCatalog(t)
	resolution, err := Resolve(Request{Catalog: loaded, Profile: "NUKE", OS: "linux", Arch: "amd64"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolution.Profile.ID != "nuke" {
		t.Errorf("profile = %q, want nuke", resolution.Profile.ID)
	}
}

// TestRealCatalogResolvesOnEveryPlatform runs the shipped catalog through the
// resolver for every supported OS and architecture. This is the check that the
// real data produces a usable plan everywhere, not just on the developer's own
// machine.
func TestRealCatalogResolvesOnEveryPlatform(t *testing.T) {
	loaded, err := catalog.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	for _, osName := range []string{"windows", "linux", "darwin"} {
		for _, arch := range []string{"amd64", "arm64"} {
			for _, profile := range []string{"ai", "web", "blockchain", "app", "nuke"} {
				resolution, err := Resolve(Request{Catalog: loaded, Profile: profile, OS: osName, Arch: arch})
				if err != nil {
					t.Fatalf("Resolve(%s, %s/%s): %v", profile, osName, arch, err)
				}
				if len(resolution.Ordered) == 0 {
					t.Errorf("%s on %s/%s produced an empty plan", profile, osName, arch)
				}
				assertPrerequisitesComeFirst(t, resolution, osName, arch, profile)
				for _, item := range append(resolution.Unsupported, resolution.Skipped...) {
					if item.Reason == "" {
						t.Errorf("%s on %s/%s: %s excluded without a reason", profile, osName, arch, item.Spec.Name)
					}
				}
			}
		}
	}
}

func assertPrerequisitesComeFirst(t *testing.T, resolution Resolution, osName, arch, profile string) {
	t.Helper()
	position := map[string]int{}
	for index, item := range resolution.Ordered {
		position[item.Spec.Name] = index
	}
	for name, index := range position {
		for _, item := range resolution.Ordered {
			if item.Spec.Name != name {
				continue
			}
			for _, required := range item.Spec.Requires {
				if requiredIndex, planned := position[required]; planned && requiredIndex > index {
					t.Errorf("%s on %s/%s: %s is installed before its prerequisite %s",
						profile, osName, arch, name, required)
				}
			}
		}
	}
}
