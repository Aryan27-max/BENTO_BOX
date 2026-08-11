package installer

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Aryan27-max/bento-box/internal/command"
	"github.com/Aryan27-max/bento-box/internal/logging"
)

func testInstaller(t *testing.T) *Installer {
	t.Helper()
	return &Installer{
		Runner:    command.NewMock(),
		OS:        "linux",
		Arch:      "amd64",
		Home:      t.TempDir(),
		Endpoints: defaultEndpoints(),
		Progress:  noProgress{},
		log:       logging.NewWriter(discardWriter{}),
		failed:    map[string]string{},
	}
}

func sha256Of(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestFetchVerifiesChecksum(t *testing.T) {
	payload := []byte("the contents of an official release archive")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(payload)
	}))
	defer server.Close()

	installer := testInstaller(t)
	installer.HTTPClient = server.Client()
	dir := t.TempDir()

	path, err := installer.fetch(context.Background(), Download{
		URL: server.URL + "/tool.tar.gz", SHA256: sha256Of(payload),
	}, dir)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the download: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Error("the downloaded file does not match what the server sent")
	}
}

// TestFetchRefusesAChecksumMismatch is the supply-chain guarantee: a
// substituted or corrupted archive is deleted, not used.
func TestFetchRefusesAChecksumMismatch(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("this is not the archive the vendor published"))
	}))
	defer server.Close()

	installer := testInstaller(t)
	installer.HTTPClient = server.Client()
	dir := t.TempDir()

	_, err := installer.fetch(context.Background(), Download{
		URL:    server.URL + "/tool.tar.gz",
		SHA256: sha256Of([]byte("the archive the vendor actually published")),
	}, dir)

	if err == nil {
		t.Fatal("a checksum mismatch must be an error")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("error = %q, want a checksum mismatch", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("the mismatched download was left on disk: %v", entries)
	}
}

func TestFetchRefusesPlainHTTP(t *testing.T) {
	installer := testInstaller(t)
	_, err := installer.fetch(context.Background(), Download{URL: "http://example.com/tool.tar.gz"}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "insecure") {
		t.Errorf("error = %v, want a refusal to use plain HTTP", err)
	}
}

func TestFetchReportsServerErrors(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer server.Close()

	installer := testInstaller(t)
	installer.HTTPClient = server.Client()

	if _, err := installer.fetch(context.Background(), Download{URL: server.URL + "/missing"}, t.TempDir()); err == nil {
		t.Error("a 404 must be reported as an error")
	}
}

func TestExtractTarGzCollapsesTheSingleRoot(t *testing.T) {
	// Vendors ship "go/bin/go" inside the archive; Bento unwraps that so the
	// PATH entry is predictable.
	archive := buildTarGz(t, map[string]string{
		"go/bin/go":        "#!/bin/sh\n",
		"go/VERSION":       "go1.26.4",
		"go/src/README.md": "hello",
	})

	installer := testInstaller(t)
	target := t.TempDir()
	root, err := installer.extract(context.Background(), archive, target)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}

	if filepath.Base(root) != "go" {
		t.Errorf("root = %q, want the archive's single top-level directory", root)
	}
	if _, err := os.Stat(filepath.Join(root, "bin", "go")); err != nil {
		t.Errorf("the payload was not unpacked: %v", err)
	}
}

func TestExtractZip(t *testing.T) {
	archive := buildZip(t, map[string]string{
		"node-v22.14.0-win-x64/node.exe": "binary",
		"node-v22.14.0-win-x64/npm.cmd":  "script",
	})

	installer := testInstaller(t)
	root, err := installer.extract(context.Background(), archive, t.TempDir())
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "node.exe")); err != nil {
		t.Errorf("the payload was not unpacked: %v", err)
	}
}

func TestExtractRejectsUnknownFormats(t *testing.T) {
	file := filepath.Join(t.TempDir(), "thing.rar")
	if err := os.WriteFile(file, []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := testInstaller(t).extract(context.Background(), file, t.TempDir()); err == nil {
		t.Error("an unsupported archive format must be reported, not ignored")
	}
}

// TestSafeJoinBlocksPathTraversal: Bento unpacks archives inside the user's
// home, so an entry named "../../.bashrc" must never be written.
func TestSafeJoinBlocksPathTraversal(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"../escape.txt",
		"../../.bashrc",
		"good/../../../etc/passwd",
	} {
		if _, err := safeJoin(dir, name); err == nil {
			t.Errorf("safeJoin(%q) should have been refused", name)
		}
	}

	if _, err := safeJoin(dir, "go/bin/go"); err != nil {
		t.Errorf("safeJoin refused a legitimate entry: %v", err)
	}
}

func TestExtractRefusesATraversingArchive(t *testing.T) {
	archive := buildTarGz(t, map[string]string{"../escaped.txt": "gotcha"})

	if _, err := testInstaller(t).extract(context.Background(), archive, t.TempDir()); err == nil {
		t.Error("an archive escaping its directory must be refused")
	}
}

func TestFilenameFromURL(t *testing.T) {
	cases := map[string]string{
		"https://go.dev/dl/go1.26.4.linux-amd64.tar.gz":                    "go1.26.4.linux-amd64.tar.gz",
		"https://update.code.visualstudio.com/latest/linux-deb-x64/stable": "stable",
		"https://example.com/thing.zip?token=abc":                          "thing.zip",
	}
	for url, want := range cases {
		if got := filenameFromURL(url); got != want {
			t.Errorf("filenameFromURL(%q) = %q, want %q", url, got, want)
		}
	}
}

// --- Vendor version resolvers --------------------------------------------

func TestResolveGoPicksTheCurrentStableBuild(t *testing.T) {
	const index = `[
	  {"version":"go1.26.4","stable":true,"files":[
	    {"filename":"go1.26.4.linux-amd64.tar.gz","os":"linux","arch":"amd64","kind":"archive","sha256":"aaa"},
	    {"filename":"go1.26.4.darwin-arm64.tar.gz","os":"darwin","arch":"arm64","kind":"archive","sha256":"bbb"},
	    {"filename":"go1.26.4.linux-amd64.msi","os":"linux","arch":"amd64","kind":"installer","sha256":"ccc"}
	  ]},
	  {"version":"go1.27rc1","stable":false,"files":[
	    {"filename":"go1.27rc1.linux-amd64.tar.gz","os":"linux","arch":"amd64","kind":"archive","sha256":"ddd"}
	  ]}
	]`

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(index))
	}))
	defer server.Close()

	installer := testInstaller(t)
	installer.HTTPClient = server.Client()
	installer.Endpoints.GoDownloads = server.URL

	download, err := installer.resolve(context.Background(), "go_stable")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if download.Version != "1.26.4" {
		t.Errorf("version = %q, want 1.26.4", download.Version)
	}
	if !strings.HasSuffix(download.URL, "go1.26.4.linux-amd64.tar.gz") {
		t.Errorf("url = %q, want the linux/amd64 archive", download.URL)
	}
	if download.SHA256 != "aaa" {
		t.Errorf("sha256 = %q, want the checksum go.dev published", download.SHA256)
	}
}

// TestResolveGoIgnoresPreReleases pins the version policy: latest *stable*,
// never a release candidate.
func TestResolveGoIgnoresPreReleases(t *testing.T) {
	const index = `[
	  {"version":"go1.27rc1","stable":false,"files":[
	    {"filename":"go1.27rc1.linux-amd64.tar.gz","os":"linux","arch":"amd64","kind":"archive","sha256":"ddd"}]},
	  {"version":"go1.26.4","stable":true,"files":[
	    {"filename":"go1.26.4.linux-amd64.tar.gz","os":"linux","arch":"amd64","kind":"archive","sha256":"aaa"}]}
	]`

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(index))
	}))
	defer server.Close()

	installer := testInstaller(t)
	installer.HTTPClient = server.Client()
	installer.Endpoints.GoDownloads = server.URL

	download, err := installer.resolve(context.Background(), "go_stable")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if strings.Contains(download.URL, "rc1") {
		t.Errorf("resolver picked a release candidate: %s", download.URL)
	}
}

func TestResolveGoReportsUnsupportedPlatforms(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`[{"version":"go1.26.4","stable":true,"files":[
		  {"filename":"go1.26.4.linux-amd64.tar.gz","os":"linux","arch":"amd64","kind":"archive","sha256":"aaa"}]}]`))
	}))
	defer server.Close()

	installer := testInstaller(t)
	installer.HTTPClient = server.Client()
	installer.Endpoints.GoDownloads = server.URL
	installer.OS = "darwin"

	if _, err := installer.resolve(context.Background(), "go_stable"); err == nil {
		t.Error("a platform with no published build must be an error, not a wrong download")
	}
}

func TestResolveNodePicksTheCurrentLTS(t *testing.T) {
	const index = `[
	  {"version":"v23.7.0","lts":false,"files":["linux-x64"]},
	  {"version":"v22.14.0","lts":"Jod","files":["linux-x64","win-x64"]},
	  {"version":"v20.18.2","lts":"Iron","files":["linux-x64"]}
	]`

	mux := http.NewServeMux()
	mux.HandleFunc("/index.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(index))
	})
	mux.HandleFunc("/v22.14.0/SHASUMS256.txt", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("deadbeef  node-v22.14.0-linux-x64.tar.gz\nother  node-v22.14.0-win-x64.zip\n"))
	})
	server := httptest.NewTLSServer(mux)
	defer server.Close()

	installer := testInstaller(t)
	installer.HTTPClient = server.Client()
	installer.Endpoints.NodeIndex = server.URL + "/index.json"
	installer.Endpoints.NodeDistBase = server.URL

	download, err := installer.resolve(context.Background(), "node_lts")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// v23 is newer but is not an LTS line, so it must not be chosen.
	if download.Version != "22.14.0" {
		t.Errorf("version = %q, want the newest LTS 22.14.0", download.Version)
	}
	if !strings.HasSuffix(download.URL, "node-v22.14.0-linux-x64.tar.gz") {
		t.Errorf("url = %q", download.URL)
	}
	if download.SHA256 != "deadbeef" {
		t.Errorf("sha256 = %q, want the checksum from SHASUMS256.txt", download.SHA256)
	}
}

func TestNodeArtefactNames(t *testing.T) {
	cases := []struct {
		os, arch, platform, extension string
	}{
		{"linux", "amd64", "linux-x64", ".tar.gz"},
		{"linux", "arm64", "linux-arm64", ".tar.gz"},
		{"darwin", "arm64", "darwin-arm64", ".tar.gz"},
		{"windows", "amd64", "win-x64", ".zip"},
	}
	for _, tc := range cases {
		platform, extension, err := nodeArtefact(tc.os, tc.arch)
		if err != nil {
			t.Fatalf("nodeArtefact(%s,%s): %v", tc.os, tc.arch, err)
		}
		if platform != tc.platform || extension != tc.extension {
			t.Errorf("nodeArtefact(%s,%s) = %s%s, want %s%s", tc.os, tc.arch, platform, extension, tc.platform, tc.extension)
		}
	}
	if _, _, err := nodeArtefact("plan9", "amd64"); err == nil {
		t.Error("an unsupported OS should be an error")
	}
}

func TestResolveFlutterUsesTheManifestsCurrentStable(t *testing.T) {
	const manifest = `{
	  "base_url": "https://storage.googleapis.com/flutter_infra_release/releases",
	  "current_release": {"stable": "hash-stable", "beta": "hash-beta"},
	  "releases": [
	    {"hash":"hash-beta","channel":"beta","version":"3.29.0-beta","archive":"beta/linux/flutter_beta.tar.xz","sha256":"bbb","dart_sdk_arch":"x64"},
	    {"hash":"hash-stable","channel":"stable","version":"3.27.2","archive":"stable/linux/flutter_linux_3.27.2-stable.tar.xz","sha256":"aaa","dart_sdk_arch":"x64"},
	    {"hash":"hash-stable","channel":"stable","version":"3.27.2","archive":"stable/linux/flutter_linux_arm64_3.27.2-stable.tar.xz","sha256":"ccc","dart_sdk_arch":"arm64"}
	  ]
	}`

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if !strings.HasSuffix(request.URL.Path, "releases_linux.json") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Write([]byte(manifest))
	}))
	defer server.Close()

	installer := testInstaller(t)
	installer.HTTPClient = server.Client()
	installer.Endpoints.FlutterReleases = server.URL

	download, err := installer.resolve(context.Background(), "flutter_stable")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if download.Version != "3.27.2" {
		t.Errorf("version = %q, want the current stable 3.27.2", download.Version)
	}
	if download.SHA256 != "aaa" {
		t.Errorf("sha256 = %q, want the amd64 build's checksum", download.SHA256)
	}
	if strings.Contains(download.URL, "arm64") {
		t.Errorf("url = %q, want the amd64 archive", download.URL)
	}

	// The same manifest must yield the arm64 archive on an arm64 machine.
	installer.Arch = "arm64"
	download, err = installer.resolve(context.Background(), "flutter_stable")
	if err != nil {
		t.Fatalf("resolve arm64: %v", err)
	}
	if download.SHA256 != "ccc" {
		t.Errorf("arm64 sha256 = %q, want ccc", download.SHA256)
	}
}

func TestUnknownResolverIsAnError(t *testing.T) {
	if _, err := testInstaller(t).resolve(context.Background(), "wishful_thinking"); err == nil {
		t.Error("an unknown resolver must be an error")
	}
}

// --- helpers --------------------------------------------------------------

func buildTarGz(t *testing.T, files map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "archive.tar.gz")

	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)

	for name, content := range files {
		header := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func buildZip(t *testing.T, files map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "archive.zip")

	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	writer := zip.NewWriter(file)
	for name, content := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
