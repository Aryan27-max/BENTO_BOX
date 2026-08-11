package installer

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Version resolvers ask a vendor's own release metadata which build is current
// and where it lives. This is how Bento honours "install the latest stable
// release" without a single hardcoded version number in Go source or in the
// catalog: the vendor is the authority, and Bento asks them at install time.

// resolverEndpoints holds the official metadata URLs. They are fields rather
// than constants so tests can point them at a local server.
type resolverEndpoints struct {
	GoDownloads     string
	NodeIndex       string
	NodeDistBase    string
	FlutterReleases string
}

func defaultEndpoints() resolverEndpoints {
	return resolverEndpoints{
		GoDownloads:     "https://go.dev/dl/?mode=json",
		NodeIndex:       "https://nodejs.org/dist/index.json",
		NodeDistBase:    "https://nodejs.org/dist",
		FlutterReleases: "https://storage.googleapis.com/flutter_infra_release/releases",
	}
}

// resolve dispatches to the named resolver.
func (i *Installer) resolve(ctx context.Context, name string) (Download, error) {
	switch name {
	case "go_stable":
		return i.resolveGo(ctx)
	case "node_lts":
		return i.resolveNode(ctx)
	case "flutter_stable":
		return i.resolveFlutter(ctx)
	default:
		return Download{}, fmt.Errorf("unknown version resolver %q", name)
	}
}

func (i *Installer) getJSON(ctx context.Context, url string, into any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "bento-box")

	response, err := i.httpClient().Do(request)
	if err != nil {
		return fmt.Errorf("fetching %s: %w", url, err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("fetching %s: server returned %s", url, response.Status)
	}
	if err := json.NewDecoder(response.Body).Decode(into); err != nil {
		return fmt.Errorf("parsing %s: %w", url, err)
	}
	return nil
}

// --- Go -------------------------------------------------------------------

type goRelease struct {
	Version string `json:"version"`
	Stable  bool   `json:"stable"`
	Files   []struct {
		Filename string `json:"filename"`
		OS       string `json:"os"`
		Arch     string `json:"arch"`
		Kind     string `json:"kind"`
		SHA256   string `json:"sha256"`
	} `json:"files"`
}

// resolveGo picks the current stable Go release for this platform from
// go.dev's release index, which publishes a SHA-256 for every artefact.
func (i *Installer) resolveGo(ctx context.Context) (Download, error) {
	var releases []goRelease
	if err := i.getJSON(ctx, i.Endpoints.GoDownloads, &releases); err != nil {
		return Download{}, err
	}

	wantArch := map[string]string{"amd64": "amd64", "arm64": "arm64"}[i.Arch]
	if wantArch == "" {
		return Download{}, fmt.Errorf("no official Go build for %s", i.Arch)
	}

	for _, release := range releases {
		if !release.Stable {
			continue
		}
		for _, file := range release.Files {
			if file.OS != i.OS || file.Arch != wantArch || file.Kind != "archive" {
				continue
			}
			return Download{
				URL:      "https://go.dev/dl/" + file.Filename,
				SHA256:   file.SHA256,
				Version:  strings.TrimPrefix(release.Version, "go"),
				Filename: file.Filename,
			}, nil
		}
	}
	return Download{}, fmt.Errorf("go.dev lists no stable archive for %s/%s", i.OS, i.Arch)
}

// --- Node.js --------------------------------------------------------------

type nodeRelease struct {
	Version string `json:"version"`
	// LTS is false for non-LTS releases and the codename string otherwise,
	// which is why it has to be decoded as a bare interface.
	LTS   any      `json:"lts"`
	Files []string `json:"files"`
}

// resolveNode picks the newest Node.js LTS release. Bento targets LTS rather
// than Current because "latest stable" for a runtime a team depends on means
// the supported line, not the newest one.
func (i *Installer) resolveNode(ctx context.Context) (Download, error) {
	var releases []nodeRelease
	if err := i.getJSON(ctx, i.Endpoints.NodeIndex, &releases); err != nil {
		return Download{}, err
	}

	platform, extension, err := nodeArtefact(i.OS, i.Arch)
	if err != nil {
		return Download{}, err
	}

	for _, release := range releases {
		// The index is newest-first, so the first LTS entry is the current one.
		if codename, ok := release.LTS.(string); !ok || codename == "" {
			continue
		}
		filename := fmt.Sprintf("node-%s-%s%s", release.Version, platform, extension)
		url := fmt.Sprintf("%s/%s/%s", i.Endpoints.NodeDistBase, release.Version, filename)

		download := Download{
			URL:      url,
			Version:  strings.TrimPrefix(release.Version, "v"),
			Filename: filename,
		}
		// Node publishes one checksum file per release; fetching it turns an
		// unverified download into a verified one.
		if sum, err := i.nodeChecksum(ctx, release.Version, filename); err == nil {
			download.SHA256 = sum
		} else {
			i.log.Printf("could not fetch Node checksums for %s: %v", release.Version, err)
		}
		return download, nil
	}
	return Download{}, fmt.Errorf("nodejs.org lists no LTS release for %s/%s", i.OS, i.Arch)
}

func nodeArtefact(osName, arch string) (platform, extension string, err error) {
	archName := map[string]string{"amd64": "x64", "arm64": "arm64"}[arch]
	if archName == "" {
		return "", "", fmt.Errorf("no official Node.js build for %s", arch)
	}
	switch osName {
	case "windows":
		return "win-" + archName, ".zip", nil
	case "linux":
		return "linux-" + archName, ".tar.gz", nil
	case "darwin":
		return "darwin-" + archName, ".tar.gz", nil
	default:
		return "", "", fmt.Errorf("no official Node.js build for %s", osName)
	}
}

// nodeChecksum reads the SHASUMS256.txt published alongside every release.
func (i *Installer) nodeChecksum(ctx context.Context, version, filename string) (string, error) {
	url := fmt.Sprintf("%s/%s/SHASUMS256.txt", i.Endpoints.NodeDistBase, version)

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	response, err := i.httpClient().Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("server returned %s", response.Status)
	}

	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == filename {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("%s is not listed in the checksum file", filename)
}

// --- Flutter --------------------------------------------------------------

type flutterManifest struct {
	BaseURL        string `json:"base_url"`
	CurrentRelease struct {
		Stable string `json:"stable"`
	} `json:"current_release"`
	Releases []struct {
		Hash        string `json:"hash"`
		Channel     string `json:"channel"`
		Version     string `json:"version"`
		Archive     string `json:"archive"`
		SHA256      string `json:"sha256"`
		DartSDKArch string `json:"dart_sdk_arch"`
	} `json:"releases"`
}

// resolveFlutter reads Google's release manifest and takes the archive the
// manifest itself names as the current stable release.
func (i *Installer) resolveFlutter(ctx context.Context) (Download, error) {
	platform := map[string]string{"windows": "windows", "linux": "linux", "darwin": "macos"}[i.OS]
	if platform == "" {
		return Download{}, fmt.Errorf("no official Flutter build for %s", i.OS)
	}

	var manifest flutterManifest
	url := fmt.Sprintf("%s/releases_%s.json", i.Endpoints.FlutterReleases, platform)
	if err := i.getJSON(ctx, url, &manifest); err != nil {
		return Download{}, err
	}

	current := manifest.CurrentRelease.Stable
	if current == "" {
		return Download{}, fmt.Errorf("Flutter's manifest names no current stable release")
	}

	// The manifest lists one entry per architecture for the same release
	// hash, distinguished by dart_sdk_arch. An entry with no architecture
	// field predates the arm64 split and is x64.
	for _, release := range manifest.Releases {
		if release.Hash != current || release.Channel != "stable" {
			continue
		}
		arch := release.DartSDKArch
		if arch == "" {
			arch = "x64"
		}
		if arch != map[string]string{"amd64": "x64", "arm64": "arm64"}[i.Arch] {
			continue
		}
		return Download{
			URL:     strings.TrimSuffix(manifest.BaseURL, "/") + "/" + release.Archive,
			SHA256:  release.SHA256,
			Version: release.Version,
		}, nil
	}
	return Download{}, fmt.Errorf("Flutter's manifest has no stable %s build for %s", platform, i.Arch)
}
