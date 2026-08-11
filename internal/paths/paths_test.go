package paths

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestExpandKnownPlaceholders(t *testing.T) {
	lookup := Lookup("/home/dev", func(name string) string {
		if name == "LOCALAPPDATA" {
			return `C:\Users\dev\AppData\Local`
		}
		return ""
	})

	cases := map[string]string{
		"${HOME}/.cargo/bin":          filepath.FromSlash("/home/dev/.cargo/bin"),
		"${BENTO_HOME}/opt":           filepath.FromSlash("/home/dev/.bento/opt"),
		"${LOCALAPPDATA}/Android/Sdk": filepath.FromSlash(`C:\Users\dev\AppData\Local/Android/Sdk`),
		"/usr/local/go/bin":           filepath.FromSlash("/usr/local/go/bin"),
	}
	for input, want := range cases {
		if got := Expand(input, lookup); got != want {
			t.Errorf("Expand(%q) = %q, want %q", input, got, want)
		}
	}
}

// TestExpandLeavesUnknownPlaceholdersAlone is what stops Bento from turning
// "${ANDROID_HOME}/platform-tools" into "/platform-tools" and putting that
// nonsense on someone's PATH.
func TestExpandLeavesUnknownPlaceholdersAlone(t *testing.T) {
	lookup := Lookup("/home/dev", func(string) string { return "" })

	got := Expand("${SOMETHING_UNSET}/bin", lookup)
	if got == filepath.FromSlash("/bin") {
		t.Fatalf("an unresolved placeholder collapsed into %q", got)
	}
	if got != filepath.FromSlash("${SOMETHING_UNSET}/bin") {
		t.Errorf("Expand = %q, want the placeholder left intact", got)
	}
}

func TestExpandHandlesMalformedInput(t *testing.T) {
	lookup := Lookup("/home/dev", func(string) string { return "" })
	for _, input := range []string{"${unclosed", "plain", "", "$NOTBRACED"} {
		// The only requirement is that it does not panic and returns something.
		_ = Expand(input, lookup)
	}
}

func TestHomeAndOptDir(t *testing.T) {
	if got, want := Home("/home/dev"), filepath.Join("/home/dev", ".bento"); got != want {
		t.Errorf("Home = %q, want %q", got, want)
	}
	if got, want := OptDir("/home/dev"), filepath.Join("/home/dev", ".bento", "opt"); got != want {
		t.Errorf("OptDir = %q, want %q", got, want)
	}
}

func TestNormalise(t *testing.T) {
	cases := map[string]string{
		"/usr/local/bin/": filepath.FromSlash("/usr/local/bin"),
		"/usr/local/bin":  filepath.FromSlash("/usr/local/bin"),
		"  /usr/bin  ":    filepath.FromSlash("/usr/bin"),
		"/usr//local/bin": filepath.FromSlash("/usr/local/bin"),
		"":                "",
	}
	for input, want := range cases {
		if got := Normalise(input); got != want {
			t.Errorf("Normalise(%q) = %q, want %q", input, got, want)
		}
	}
}

// TestSameEntry is the comparison behind "never add a PATH entry twice".
func TestSameEntry(t *testing.T) {
	cases := []struct {
		left, right string
		windows     bool
		want        bool
	}{
		{"/usr/local/bin", "/usr/local/bin/", false, true},
		{"/usr/local/bin", "/usr/local/lib", false, false},
		{`C:\Program Files\Go\bin`, `c:\program files\go\bin`, true, true},
		{`C:\Program Files\Go\bin`, `c:\program files\go\bin`, false, false},
		{`C:\Go\bin\`, `C:\Go\bin`, true, true},
	}
	for _, tc := range cases {
		if got := SameEntry(tc.left, tc.right, tc.windows); got != tc.want {
			t.Errorf("SameEntry(%q, %q, windows=%v) = %v, want %v",
				tc.left, tc.right, tc.windows, got, tc.want)
		}
	}
}

func TestSeparatorsAreNormalisedForTheHost(t *testing.T) {
	got := Expand("${HOME}/go/bin", Lookup("/home/dev", func(string) string { return "" }))
	if runtime.GOOS == "windows" {
		if got != `\home\dev\go\bin` {
			t.Errorf("Expand = %q, want Windows separators", got)
		}
		return
	}
	if got != "/home/dev/go/bin" {
		t.Errorf("Expand = %q", got)
	}
}
