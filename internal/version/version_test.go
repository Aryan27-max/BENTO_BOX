package version

import "testing"

func TestParseRealWorldOutput(t *testing.T) {
	// Every string here is genuine output from the tool named in the case.
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"go", "go version go1.26.4 windows/amd64", "1.26.4"},
		{"node", "v22.14.0", "22.14.0"},
		{"python", "Python 3.13.1", "3.13.1"},
		{"git", "git version 2.47.1.windows.1", "2.47.1-windows.1"},
		{"docker", "Docker version 28.0.1, build 068a01e", "28.0.1"},
		{"rustc", "rustc 1.85.0 (4d91de4e4 2025-02-17)", "1.85.0"},
		{"cargo", "cargo 1.85.0 (d73d2caf9 2024-12-31)", "1.85.0"},
		{"psql", "psql (PostgreSQL) 17.4", "17.4.0"},
		{"mongosh", "2.4.0", "2.4.0"},
		{"vscode", "1.97.2\nfabdb6a30b49f79a7aba0f2ad9df9b399473380f\nx64", "1.97.2"},
		{"java", `openjdk version "21.0.6" 2025-01-21`, "21.0.6"},
		{"npm", "10.9.2", "10.9.2"},
		{"two-component", "make 4.4", "4.4.0"},
		{"prerelease", "1.2.0-beta3", "1.2.0-beta3"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.input)
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", tc.input, err)
			}
			if got.String() != tc.want {
				t.Errorf("Parse(%q) = %s, want %s", tc.input, got, tc.want)
			}
		})
	}
}

func TestParseRejectsNonVersions(t *testing.T) {
	for _, input := range []string{"", "command not found", "no numbers here", "42"} {
		if got, err := Parse(input); err == nil {
			t.Errorf("Parse(%q) = %s, want error", input, got)
		}
	}
}

func TestCompare(t *testing.T) {
	cases := []struct {
		left, right string
		want        int
	}{
		{"1.2.3", "1.2.3", 0},
		{"2.0.0", "1.9.9", 1},
		{"1.9.9", "2.0.0", -1},
		{"1.10.0", "1.9.0", 1}, // numeric, not lexicographic
		{"1.2.4", "1.2.3", 1},
		{"22.0.0", "20.11.1", 1},
		{"1.2.3-beta1", "1.2.3", -1}, // pre-release sorts before stable
		{"1.2.3", "1.2.3-rc1", 1},
		{"1.2.3-beta1", "1.2.3-beta2", -1},
	}

	for _, tc := range cases {
		left, right := MustParse(tc.left), MustParse(tc.right)
		if got := left.Compare(right); got != tc.want {
			t.Errorf("Compare(%s, %s) = %d, want %d", tc.left, tc.right, got, tc.want)
		}
		if got := right.Compare(left); got != -tc.want {
			t.Errorf("Compare(%s, %s) = %d, want %d (antisymmetry)", tc.right, tc.left, got, -tc.want)
		}
	}
}

func TestStable(t *testing.T) {
	cases := map[string]bool{
		"1.2.3":            true,
		"2.47.1.windows.1": true, // vendor packaging suffix, still a stable release
		"1.2.3-beta1":      false,
		"1.2.3-rc2":        false,
		"1.2.3-alpha":      false,
		"1.2.3-nightly":    false,
		"1.97.0-insiders":  false,
	}
	for input, want := range cases {
		if got := MustParse(input).Stable(); got != want {
			t.Errorf("MustParse(%q).Stable() = %v, want %v", input, got, want)
		}
	}
}

func TestSatisfies(t *testing.T) {
	cases := []struct {
		installed, minimum string
		want               bool
	}{
		{"22.14.0", "20.0.0", true},
		{"18.20.5", "20.0.0", false},
		{"20.0.0", "20.0.0", true},
		{"1.2.3", "", true},              // no constraint
		{"1.2.3", "not-a-version", true}, // unparseable constraint is ignored
	}
	for _, tc := range cases {
		if got := MustParse(tc.installed).Satisfies(tc.minimum); got != tc.want {
			t.Errorf("%s.Satisfies(%q) = %v, want %v", tc.installed, tc.minimum, got, tc.want)
		}
	}
}

func TestExtractWithPattern(t *testing.T) {
	// `java -version` prints a quoted version; the generic parser would find
	// the right number here anyway, but tools like this are exactly why
	// per-dependency patterns exist.
	got, err := Extract(`openjdk version "21.0.6" 2025-01-21`, `version "([0-9.]+)"`)
	if err != nil {
		t.Fatalf("Extract returned error: %v", err)
	}
	if got.String() != "21.0.6" {
		t.Errorf("Extract = %s, want 21.0.6", got)
	}
}

func TestExtractReportsBadPattern(t *testing.T) {
	if _, err := Extract("1.2.3", "([unclosed"); err == nil {
		t.Error("Extract with invalid pattern should return an error")
	}
	if _, err := Extract("1.2.3", `banana ([0-9]+)`); err == nil {
		t.Error("Extract with non-matching pattern should return an error")
	}
}

func TestLatest(t *testing.T) {
	versions := []Version{MustParse("1.2.0"), MustParse("1.10.0"), MustParse("1.9.3")}
	got, ok := Latest(versions)
	if !ok || got.String() != "1.10.0" {
		t.Errorf("Latest = %s (%v), want 1.10.0", got, ok)
	}
	if _, ok := Latest(nil); ok {
		t.Error("Latest(nil) should report false")
	}
}
