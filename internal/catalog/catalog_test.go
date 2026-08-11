package catalog

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Aryan27-max/bento-box/internal/dependency"
	"github.com/Aryan27-max/bento-box/internal/profiles"
)

// TestShippedCatalogIsValid runs the full validator against the catalog that
// is compiled into the binary. If someone adds a dependency with a typo in a
// category, a dangling requires edge or a plain-HTTP download, this fails
// before it can reach a user's machine.
func TestShippedCatalogIsValid(t *testing.T) {
	catalog, err := Load()
	if err != nil {
		t.Fatalf("shipped catalog failed to load: %v", err)
	}
	if catalog.Count() == 0 {
		t.Fatal("shipped catalog is empty")
	}
	t.Logf("catalog holds %d dependencies across %d profiles", catalog.Count(), len(catalog.Profiles().All()))
}

// TestShippedCatalogCoversRequiredProfiles pins the profile list from the
// product specification: exactly these five choices, no more.
func TestShippedCatalogCoversRequiredProfiles(t *testing.T) {
	catalog, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want := []string{"ai", "web", "blockchain", "app", "nuke"}
	got := catalog.Profiles().Selectable()
	if len(got) != len(want) {
		t.Fatalf("selectable profiles = %d, want %d", len(got), len(want))
	}
	for index, id := range want {
		if got[index].ID != id {
			t.Errorf("profile %d = %q, want %q", index, got[index].ID, id)
		}
		if got[index].Emoji == "" {
			t.Errorf("profile %q has no emoji", id)
		}
	}
}

// TestEveryProfileHasDependencies guards against a profile that resolves to
// nothing, which would silently produce an empty installation plan.
func TestEveryProfileHasDependencies(t *testing.T) {
	catalog, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, profile := range catalog.Profiles().All() {
		expanded, err := catalog.Profiles().Expand(profile.ID)
		if err != nil {
			t.Fatalf("Expand(%q): %v", profile.ID, err)
		}
		if specs := catalog.ForProfiles(expanded); len(specs) == 0 {
			t.Errorf("profile %q resolves to no dependencies", profile.ID)
		}
	}
}

// TestNukeIsTheUnionOfEveryProfile is the deduplication guarantee stated in the
// product spec: NUKE must contain every dependency of every other profile, and
// must contain each one exactly once.
func TestNukeIsTheUnionOfEveryProfile(t *testing.T) {
	catalog, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	nukeProfiles, err := catalog.Profiles().Expand("nuke")
	if err != nil {
		t.Fatalf("Expand(nuke): %v", err)
	}
	nuke := catalog.ForProfiles(nukeProfiles)

	inNuke := map[string]int{}
	for _, spec := range nuke {
		inNuke[spec.Name]++
	}
	for name, count := range inNuke {
		if count != 1 {
			t.Errorf("NUKE contains %q %d times, want exactly 1", name, count)
		}
	}

	for _, profileID := range []string{"ai", "web", "blockchain", "app"} {
		expanded, err := catalog.Profiles().Expand(profileID)
		if err != nil {
			t.Fatalf("Expand(%q): %v", profileID, err)
		}
		for _, spec := range catalog.ForProfiles(expanded) {
			if inNuke[spec.Name] == 0 {
				t.Errorf("NUKE is missing %q, which profile %q requires", spec.Name, profileID)
			}
		}
	}
}

// TestPythonIsSharedButAppearsOnce is the worked example from the product spec:
// AI, Web and App all want Python, and NUKE must plan it a single time.
func TestPythonIsSharedButAppearsOnce(t *testing.T) {
	catalog, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	python, ok := catalog.Lookup("python")
	if !ok {
		t.Fatal("catalog has no python dependency")
	}
	if !python.InProfile("ai") || !python.InProfile("web") {
		t.Fatalf("python profiles = %v, want at least ai and web", python.Profiles)
	}

	expanded, err := catalog.Profiles().Expand("nuke")
	if err != nil {
		t.Fatalf("Expand(nuke): %v", err)
	}
	count := 0
	for _, spec := range catalog.ForProfiles(expanded) {
		if spec.Name == "python" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("python appears %d times in NUKE, want 1", count)
	}
}

// TestEveryDependencyIsInstallableOrHonest enforces the no-fake-support rule:
// a dependency either has a real installation path on a platform, or says
// plainly why it does not.
func TestEveryDependencyIsInstallableOrHonest(t *testing.T) {
	catalog, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	for _, spec := range catalog.All() {
		for _, osName := range []string{"windows", "linux", "darwin"} {
			if spec.SupportsOS(osName) {
				continue
			}
			if reason := spec.UnsupportedReason(osName, "amd64"); reason == "" {
				t.Errorf("%s: no install path and no reason for %s", spec.Name, osName)
			}
		}
		for _, platform := range spec.Platforms {
			for _, step := range platform.Install {
				if step.Method == dependency.MethodManual && step.Reason == "" {
					t.Errorf("%s: manual step without a reason", spec.Name)
				}
			}
		}
	}
}

// TestDownloadsUseHTTPS is a supply-chain guard: no dependency may fetch
// anything over plain HTTP.
func TestDownloadsUseHTTPS(t *testing.T) {
	catalog, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, spec := range catalog.All() {
		for osName, platform := range spec.Platforms {
			for _, step := range platform.Install {
				urls := append([]string{step.URL}, mapValues(step.URLs)...)
				for _, url := range urls {
					if url != "" && !strings.HasPrefix(url, "https://") {
						t.Errorf("%s/%s: non-HTTPS url %q", spec.Name, osName, url)
					}
				}
			}
		}
	}
}

// TestGUIApplicationsAreVerifiedByPath checks that GUI applications do not rely
// on PATH alone, which they frequently are not on.
func TestGUIApplicationsAreVerifiedByPath(t *testing.T) {
	catalog, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, spec := range catalog.All() {
		if spec.Category != dependency.CategoryGUIApplication {
			continue
		}
		if len(spec.Verify.Paths) == 0 {
			t.Errorf("%s is a GUI application but has no filesystem verification paths", spec.Name)
		}
		for osName := range spec.Platforms {
			if _, ok := spec.Verify.Paths[osName]; !ok {
				t.Errorf("%s: no verification path for %s, where it can be installed", spec.Name, osName)
			}
		}
	}
}

func mapValues(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, value := range m {
		out = append(out, value)
	}
	return out
}

// --- Loader behaviour, exercised against small fixtures ------------------

const minimalProfiles = `[{"id":"core","name":"Core","order":0},{"id":"web","name":"Web","order":1}]`

func fixture(t *testing.T, deps string) *Catalog {
	t.Helper()
	fsys := fstest.MapFS{
		"profiles.json":          {Data: []byte(minimalProfiles)},
		"dependencies/test.json": {Data: []byte(deps)},
	}
	catalog, err := LoadFS(fsys)
	if err != nil {
		t.Fatalf("LoadFS: %v", err)
	}
	return catalog
}

func loadFixtureErr(deps string) error {
	fsys := fstest.MapFS{
		"profiles.json":          {Data: []byte(minimalProfiles)},
		"dependencies/test.json": {Data: []byte(deps)},
	}
	_, err := LoadFS(fsys)
	return err
}

const validDep = `[{
  "name": "tool", "display_name": "Tool", "category": "CLI_TOOL",
  "profiles": ["web"], "verify": {"command": "tool", "args": ["--version"]},
  "platforms": {"linux": {"install": [{"method": "package_manager", "manager": "apt", "packages": ["tool"]}]}}
}]`

func TestLoaderAcceptsValidCatalog(t *testing.T) {
	catalog := fixture(t, validDep)
	if _, ok := catalog.Lookup("tool"); !ok {
		t.Error("expected tool to be present")
	}
}

func TestLoaderRejectsBadCatalogs(t *testing.T) {
	cases := map[string]string{
		"unknown category": `[{"name":"x","category":"NONSENSE","profiles":["web"],"verify":{"command":"x"},
			"platforms":{"linux":{"install":[{"method":"package_manager","manager":"apt","packages":["x"]}]}}}]`,
		"unknown profile": `[{"name":"x","category":"CLI_TOOL","profiles":["nope"],"verify":{"command":"x"},
			"platforms":{"linux":{"install":[{"method":"package_manager","manager":"apt","packages":["x"]}]}}}]`,
		"dangling requires": `[{"name":"x","category":"CLI_TOOL","profiles":["web"],"requires":["ghost"],"verify":{"command":"x"},
			"platforms":{"linux":{"install":[{"method":"package_manager","manager":"apt","packages":["x"]}]}}}]`,
		"no verification": `[{"name":"x","category":"CLI_TOOL","profiles":["web"],
			"platforms":{"linux":{"install":[{"method":"package_manager","manager":"apt","packages":["x"]}]}}}]`,
		"no platforms": `[{"name":"x","category":"CLI_TOOL","profiles":["web"],"verify":{"command":"x"},"platforms":{}}]`,
		"unknown platform": `[{"name":"x","category":"CLI_TOOL","profiles":["web"],"verify":{"command":"x"},
			"platforms":{"solaris":{"install":[{"method":"package_manager","manager":"apt","packages":["x"]}]}}}]`,
		"plain http download": `[{"name":"x","category":"CLI_TOOL","profiles":["web"],"verify":{"command":"x"},
			"platforms":{"linux":{"install":[{"method":"archive","url":"http://example.com/x.tar.gz"}]}}}]`,
		"manual without reason": `[{"name":"x","category":"CLI_TOOL","profiles":["web"],"verify":{"command":"x"},
			"platforms":{"linux":{"install":[{"method":"manual","url":"https://example.com"}]}}}]`,
		"unknown field": `[{"name":"x","category":"CLI_TOOL","profiles":["web"],"verify":{"command":"x"},"typo_field":1,
			"platforms":{"linux":{"install":[{"method":"package_manager","manager":"apt","packages":["x"]}]}}}]`,
		"duplicate name": `[
			{"name":"x","category":"CLI_TOOL","profiles":["web"],"verify":{"command":"x"},
			 "platforms":{"linux":{"install":[{"method":"package_manager","manager":"apt","packages":["x"]}]}}},
			{"name":"x","category":"CLI_TOOL","profiles":["web"],"verify":{"command":"x"},
			 "platforms":{"linux":{"install":[{"method":"package_manager","manager":"apt","packages":["x"]}]}}}]`,
	}

	for name, deps := range cases {
		t.Run(name, func(t *testing.T) {
			if err := loadFixtureErr(deps); err == nil {
				t.Errorf("expected %s to be rejected", name)
			}
		})
	}
}

func TestLoaderRejectsDependencyCycles(t *testing.T) {
	cyclic := `[
		{"name":"a","category":"CLI_TOOL","profiles":["web"],"requires":["b"],"verify":{"command":"a"},
		 "platforms":{"linux":{"install":[{"method":"package_manager","manager":"apt","packages":["a"]}]}}},
		{"name":"b","category":"CLI_TOOL","profiles":["web"],"requires":["a"],"verify":{"command":"b"},
		 "platforms":{"linux":{"install":[{"method":"package_manager","manager":"apt","packages":["b"]}]}}}]`
	err := loadFixtureErr(cyclic)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Errorf("expected a cycle error, got %v", err)
	}
}

func TestProfileExpansionIncludesCore(t *testing.T) {
	set, err := profiles.NewSet([]profiles.Profile{
		{ID: "core", Name: "Core"},
		{ID: "web", Name: "Web"},
		{ID: "nuke", Name: "Nuke", Includes: []string{"web"}},
	})
	if err != nil {
		t.Fatalf("NewSet: %v", err)
	}

	expanded, err := set.Expand("web")
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if strings.Join(expanded, ",") != "core,web" {
		t.Errorf("Expand(web) = %v, want [core web]", expanded)
	}

	expanded, err = set.Expand("nuke")
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if strings.Join(expanded, ",") != "core,nuke,web" {
		t.Errorf("Expand(nuke) = %v, want [core nuke web]", expanded)
	}
}

func TestProfileLookupAcceptsAliases(t *testing.T) {
	catalog, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for alias, want := range map[string]string{
		"ai": "ai", "ML": "ai", "machine-learning": "ai",
		"web": "web", "fullstack": "web",
		"web3": "blockchain", "all": "nuke", "NUKE": "nuke", "mobile": "app",
	} {
		profile, ok := catalog.Profiles().Lookup(alias)
		if !ok {
			t.Errorf("Lookup(%q) failed", alias)
			continue
		}
		if profile.ID != want {
			t.Errorf("Lookup(%q) = %q, want %q", alias, profile.ID, want)
		}
	}
	if _, ok := catalog.Profiles().Lookup("nonsense"); ok {
		t.Error("Lookup should reject an unknown profile")
	}
}
