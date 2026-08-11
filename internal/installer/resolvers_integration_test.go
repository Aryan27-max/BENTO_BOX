//go:build integration

// These tests talk to the real vendor metadata endpoints. They are excluded
// from `go test ./...` because a unit test suite must never depend on the
// network; run them deliberately with:
//
//	go test -tags integration ./internal/installer/...
//
// Their job is to catch the failure mode that mocked tests cannot: a vendor
// changing the shape of their release metadata underneath us.
package installer

import (
	"context"
	"strings"
	"testing"
	"time"
)

func integrationInstaller(t *testing.T, osName, arch string) *Installer {
	t.Helper()
	installer := testInstaller(t)
	installer.OS = osName
	installer.Arch = arch
	return installer
}

func TestLiveGoReleaseMetadata(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	for _, platform := range [][2]string{
		{"linux", "amd64"}, {"linux", "arm64"},
		{"darwin", "amd64"}, {"darwin", "arm64"},
		{"windows", "amd64"}, {"windows", "arm64"},
	} {
		installer := integrationInstaller(t, platform[0], platform[1])

		download, err := installer.resolve(ctx, "go_stable")
		if err != nil {
			t.Errorf("go_stable on %s/%s: %v", platform[0], platform[1], err)
			continue
		}
		if download.Version == "" {
			t.Errorf("go_stable on %s/%s returned no version", platform[0], platform[1])
		}
		if download.SHA256 == "" {
			t.Errorf("go_stable on %s/%s returned no checksum", platform[0], platform[1])
		}
		if !strings.HasPrefix(download.URL, "https://") {
			t.Errorf("go_stable on %s/%s returned %q", platform[0], platform[1], download.URL)
		}
		t.Logf("go %s for %s/%s → %s", download.Version, platform[0], platform[1], download.URL)
	}
}

func TestLiveNodeReleaseMetadata(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	for _, platform := range [][2]string{
		{"linux", "amd64"}, {"darwin", "arm64"}, {"windows", "amd64"},
	} {
		installer := integrationInstaller(t, platform[0], platform[1])

		download, err := installer.resolve(ctx, "node_lts")
		if err != nil {
			t.Errorf("node_lts on %s/%s: %v", platform[0], platform[1], err)
			continue
		}
		if download.Version == "" || download.SHA256 == "" {
			t.Errorf("node_lts on %s/%s returned %+v, want a version and a checksum",
				platform[0], platform[1], download)
		}
		t.Logf("node %s for %s/%s → %s", download.Version, platform[0], platform[1], download.URL)
	}
}

func TestLiveFlutterReleaseMetadata(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	for _, platform := range [][2]string{
		{"linux", "amd64"}, {"darwin", "arm64"}, {"windows", "amd64"},
	} {
		installer := integrationInstaller(t, platform[0], platform[1])

		download, err := installer.resolve(ctx, "flutter_stable")
		if err != nil {
			t.Errorf("flutter_stable on %s/%s: %v", platform[0], platform[1], err)
			continue
		}
		if download.Version == "" || download.URL == "" {
			t.Errorf("flutter_stable on %s/%s returned %+v", platform[0], platform[1], download)
		}
		t.Logf("flutter %s for %s/%s → %s", download.Version, platform[0], platform[1], download.URL)
	}
}
