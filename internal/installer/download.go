package installer

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Aryan27-max/bento-box/internal/command"
)

// Download represents a resolved artefact: where to get it, what it should
// hash to, and what version it claims to be.
type Download struct {
	URL string
	// SHA256 is the vendor-published checksum. When it is present Bento
	// verifies the download and refuses to use a file that does not match.
	SHA256 string
	// Version is what the vendor's metadata says this artefact is, used for
	// the log and for reporting before the tool itself can be run.
	Version string
	// Filename overrides the name derived from the URL.
	Filename string
}

// fetch downloads an artefact over HTTPS into dir and verifies its checksum.
// A download whose checksum does not match is deleted rather than used: a
// corrupted or substituted archive is exactly the thing worth refusing.
func (i *Installer) fetch(ctx context.Context, download Download, dir string) (string, error) {
	if !strings.HasPrefix(download.URL, "https://") {
		return "", fmt.Errorf("refusing to download over an insecure transport: %s", download.URL)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}

	name := download.Filename
	if name == "" {
		name = filenameFromURL(download.URL)
	}
	target := filepath.Join(dir, name)

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, download.URL, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", "bento-box")

	response, err := i.httpClient().Do(request)
	if err != nil {
		return "", fmt.Errorf("downloading %s: %w", download.URL, err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("downloading %s: server returned %s", download.URL, response.Status)
	}

	file, err := os.Create(target)
	if err != nil {
		return "", fmt.Errorf("creating %s: %w", target, err)
	}

	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(file, hasher), response.Body)
	closeErr := file.Close()
	if err != nil {
		os.Remove(target)
		return "", fmt.Errorf("downloading %s: %w", download.URL, err)
	}
	if closeErr != nil {
		os.Remove(target)
		return "", closeErr
	}

	sum := hex.EncodeToString(hasher.Sum(nil))
	if download.SHA256 != "" && !strings.EqualFold(sum, download.SHA256) {
		os.Remove(target)
		return "", fmt.Errorf("checksum mismatch for %s: expected %s, got %s", download.URL, download.SHA256, sum)
	}

	i.log.Printf("downloaded %s (%d bytes, sha256 %s)", download.URL, written, sum)
	if download.SHA256 == "" {
		i.log.Printf("  note: %s publishes no checksum for this artefact", download.URL)
	}
	return target, nil
}

func (i *Installer) httpClient() *http.Client {
	if i.HTTPClient != nil {
		return i.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Minute}
}

func filenameFromURL(url string) string {
	trimmed := url
	if index := strings.IndexAny(trimmed, "?#"); index >= 0 {
		trimmed = trimmed[:index]
	}
	if index := strings.LastIndex(trimmed, "/"); index >= 0 && index < len(trimmed)-1 {
		return trimmed[index+1:]
	}
	return "download"
}

// extract unpacks an archive into dir and returns the directory that actually
// holds the payload. Vendors ship archives with a single top-level directory
// (go/, node-v22.14.0-linux-x64/, flutter/), so the useful root is one level
// down; collapsing it here keeps PATH entries predictable.
func (i *Installer) extract(ctx context.Context, archivePath, dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}

	var err error
	switch {
	case strings.HasSuffix(archivePath, ".zip"):
		err = extractZip(archivePath, dir)
	case strings.HasSuffix(archivePath, ".tar.gz"), strings.HasSuffix(archivePath, ".tgz"):
		err = extractTarGz(archivePath, dir)
	case strings.HasSuffix(archivePath, ".tar.xz"):
		// Go has no xz decompressor in its standard library, and pulling one
		// in for a handful of archives is not worth the dependency; every
		// Linux and macOS system that ships an xz archive also ships tar.
		err = i.extractWithTar(ctx, archivePath, dir)
	default:
		return "", fmt.Errorf("unsupported archive format: %s", filepath.Base(archivePath))
	}
	if err != nil {
		return "", err
	}

	return collapseSingleRoot(dir), nil
}

func (i *Installer) extractWithTar(ctx context.Context, archivePath, dir string) error {
	if !command.Available(i.Runner, "tar") {
		return fmt.Errorf("extracting %s needs tar, which is not installed", filepath.Base(archivePath))
	}
	result, err := i.Runner.Run(ctx, command.Spec{
		Name: "tar", Args: []string{"-xJf", archivePath, "-C", dir},
	})
	if err != nil {
		return err
	}
	if !result.Success() {
		return fmt.Errorf("tar exited with code %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	return nil
}

// collapseSingleRoot returns the inner directory when an archive unpacked to
// exactly one directory and nothing else.
func collapseSingleRoot(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || !entries[0].IsDir() {
		return dir
	}
	return filepath.Join(dir, entries[0].Name())
}

func extractZip(archivePath, dir string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("opening %s: %w", filepath.Base(archivePath), err)
	}
	defer reader.Close()

	for _, entry := range reader.File {
		target, err := safeJoin(dir, entry.Name)
		if err != nil {
			return err
		}

		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}

		source, err := entry.Open()
		if err != nil {
			return err
		}
		err = writeFile(target, source, entry.Mode())
		source.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func extractTarGz(archivePath, dir string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("opening %s: %w", filepath.Base(archivePath), err)
	}
	defer file.Close()

	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("reading %s: %w", filepath.Base(archivePath), err)
	}
	defer gzipReader.Close()

	reader := tar.NewReader(gzipReader)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading %s: %w", filepath.Base(archivePath), err)
		}

		target, err := safeJoin(dir, header.Name)
		if err != nil {
			return err
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := writeFile(target, reader, os.FileMode(header.Mode)); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			// A pre-existing link would make the archive fail to unpack on a
			// re-run, which has to stay idempotent.
			os.Remove(target)
			if err := os.Symlink(header.Linkname, target); err != nil {
				// Windows needs a privilege for symlinks; a missing link is
				// not worth aborting a whole toolchain install over.
				continue
			}
		}
	}
}

// safeJoin refuses archive entries that would escape the extraction directory.
// A "../../.bashrc" entry in a downloaded archive is a path traversal attack,
// and Bento unpacks archives into the user's home.
func safeJoin(dir, name string) (string, error) {
	cleaned := filepath.Clean(filepath.FromSlash(name))
	if filepath.IsAbs(cleaned) || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) || cleaned == ".." {
		return "", fmt.Errorf("archive entry %q would escape the extraction directory", name)
	}
	target := filepath.Join(dir, cleaned)

	relative, err := filepath.Rel(dir, target)
	if err != nil || strings.HasPrefix(relative, "..") {
		return "", fmt.Errorf("archive entry %q would escape the extraction directory", name)
	}
	return target, nil
}

func writeFile(path string, source io.Reader, mode os.FileMode) error {
	if mode == 0 {
		mode = 0o644
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode.Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(file, source); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}
