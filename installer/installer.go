// Package installer downloads and caches the prebuilt arboocr_demo binary
// from wafik/ArboOCR's GitHub Releases. Kept separate from the root arboocr
// package so "how to get the binary" stays independently readable/testable
// from "how to run it" (engine.go).
package installer

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// repo is the GitHub repository that publishes prebuilt arboocr_demo
// binaries as release assets.
const repo = "wafik/ArboOCR"

// pinnedVersion is the release tag this package downloads. The release
// binary is language-agnostic, so every arboOCR wrapper (Go, PHP, Python,
// Rust) tracks the same tag — the PHP package pins it via composer.json's
// extra.arboocr-version, this package via this constant. Keep them in step
// when bumping.
//
// Bumping this constant also changes the cache directory (see
// EnsureInstalled) — that is deliberate, not incidental.
//
// TODO: bump this to the next arboOCR release once it ships — the one that
// adds model auto-download. v0.2.0 predates that feature, so the binary this
// currently downloads has no --no-download / --models-url / --download-models
// flags and no ARBOOCR_OFFLINE / ARBOOCR_CACHE_DIR / ARBOOCR_MODELS_URL
// handling. Config.NoDownload, Config.ModelsURL and Engine.EnsureModels
// (engine.go) exist already but only do anything against a newer binary
// supplied via Config.BinPath. Bumping this constant is what makes them work
// out of the box; the README's Models section says the same and should be
// re-read at the same time.
const pinnedVersion = "v0.2.0"

// downloadTimeout bounds how long EnsureInstalled waits for the release
// asset to download.
const downloadTimeout = 120 * time.Second

// DetectPlatform returns "windows-x64" or "linux-x64" based on
// runtime.GOOS/runtime.GOARCH (amd64 only), and false if unsupported.
func DetectPlatform() (platform string, ok bool) {
	if runtime.GOARCH != "amd64" {
		return "", false
	}
	switch runtime.GOOS {
	case "windows":
		return "windows-x64", true
	case "linux":
		return "linux-x64", true
	default:
		return "", false
	}
}

// EnsureInstalled makes sure the arboocr_demo binary exists locally,
// downloading it from GitHub Releases if missing, and returns its
// absolute path. binDir == "" means: use the default cache directory,
// filepath.Join(os.UserCacheDir(), "arbo-ocr-go", pinnedVersion, platform).
// Returns an error if the platform is unsupported or the download/extract
// fails.
//
// Callers passing an explicit binDir own its lifecycle: it is used as-is,
// so if you cache per-machine across upgrades, include pinnedVersion in the
// path yourself for the same reason the default layout does (below).
func EnsureInstalled(binDir string) (string, error) {
	platform, ok := DetectPlatform()
	if !ok {
		return "", fmt.Errorf(
			"arboocr: unsupported platform %s/%s; download a release manually from "+
				"https://github.com/%s/releases and pass a binPath explicitly",
			runtime.GOOS, runtime.GOARCH, repo,
		)
	}

	if binDir == "" {
		cacheDir, err := os.UserCacheDir()
		if err != nil {
			return "", fmt.Errorf("arboocr: could not determine user cache directory: %w", err)
		}
		// pinnedVersion is a path segment on purpose — do NOT "tidy" it out.
		// The extracted binary is named arboocr_demo[.exe] in every release,
		// so a version-less cache path is the same path for every version,
		// and the os.Stat short-circuit below then reports a v0.1.0-php1
		// binary as "already installed" forever. Bumping pinnedVersion would
		// be a silent no-op for every user who ever ran an older pin: they
		// keep the stale binary and never see the new release. Versioning the
		// directory makes a bump a cache miss, which is the whole point.
		binDir = filepath.Join(cacheDir, "arbo-ocr-go", pinnedVersion, platform)
	}

	binPath := filepath.Join(binDir, binaryName(platform))

	// Safe precisely because binDir is version-scoped: a hit here means a
	// binary from *this* pinnedVersion, not merely some arboocr_demo.
	if _, err := os.Stat(binPath); err == nil {
		return binPath, nil // already installed
	}

	asset := assetName(platform)
	url := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", repo, pinnedVersion, asset)

	if err := downloadAndExtract(url, binDir, asset); err != nil {
		return "", err
	}

	if platform == "linux-x64" {
		if err := os.Chmod(binPath, 0o755); err != nil {
			return "", fmt.Errorf("arboocr: could not make %s executable: %w", binPath, err)
		}
	}

	return binPath, nil
}

// binaryName returns the platform-specific executable name for a platform
// string as returned by DetectPlatform.
func binaryName(platform string) string {
	if platform == "windows-x64" {
		return "arboocr_demo.exe"
	}
	return "arboocr_demo"
}

// assetName returns the release archive file name for a platform string as
// returned by DetectPlatform.
func assetName(platform string) string {
	if platform == "windows-x64" {
		return "arboocr-windows-x64.zip"
	}
	return "arboocr-linux-x64.tar.gz"
}

// downloadAndExtract fetches url into a temp file, extracts it into
// targetDir (creating targetDir if needed), flattens the archive's single
// top-level subdirectory into targetDir, and removes the temp file.
func downloadAndExtract(url, targetDir, asset string) (err error) {
	if mkErr := os.MkdirAll(targetDir, 0o755); mkErr != nil {
		return fmt.Errorf("arboocr: could not create %s: %w", targetDir, mkErr)
	}

	tmpFile, tmpErr := os.CreateTemp("", "arboocr-dl-")
	if tmpErr != nil {
		return fmt.Errorf("arboocr: could not create temp file for download: %w", tmpErr)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()

	if err = fetchTo(tmpFile, url); err != nil {
		return err
	}

	if strings.HasSuffix(asset, ".zip") {
		if err = extractZip(tmpPath, targetDir); err != nil {
			return err
		}
	} else {
		if err = extractTarGz(tmpPath, targetDir); err != nil {
			return err
		}
	}

	return flattenSingleSubdir(targetDir)
}

// fetchTo downloads url into the (already open) tmpFile, closing it before
// returning regardless of outcome.
func fetchTo(tmpFile *os.File, url string) error {
	defer tmpFile.Close()

	client := &http.Client{Timeout: downloadTimeout}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("arboocr: download failed: %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("arboocr: download failed: %s: HTTP %d", url, resp.StatusCode)
	}

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		return fmt.Errorf("arboocr: could not save download from %s: %w", url, err)
	}

	return nil
}

// extractZip extracts the zip archive at zipPath into targetDir.
func extractZip(zipPath, targetDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("arboocr: could not open downloaded zip: %w", err)
	}
	defer r.Close()

	for _, f := range r.File {
		if err := extractZipEntry(f, targetDir); err != nil {
			return err
		}
	}
	return nil
}

func extractZipEntry(f *zip.File, targetDir string) error {
	destPath, err := safeJoin(targetDir, f.Name)
	if err != nil {
		return err
	}

	if f.FileInfo().IsDir() {
		return os.MkdirAll(destPath, 0o755)
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("arboocr: could not create %s: %w", filepath.Dir(destPath), err)
	}

	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("arboocr: could not read zip entry %s: %w", f.Name, err)
	}
	defer rc.Close()

	out, err := os.OpenFile(destPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("arboocr: could not create %s: %w", destPath, err)
	}
	defer out.Close()

	if _, err := io.Copy(out, rc); err != nil {
		return fmt.Errorf("arboocr: could not extract %s: %w", f.Name, err)
	}
	return nil
}

// extractTarGz extracts the .tar.gz archive at archivePath into targetDir.
func extractTarGz(archivePath, targetDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("arboocr: could not open downloaded archive: %w", err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("arboocr: could not open downloaded archive: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("arboocr: could not read tar entry: %w", err)
		}

		if err := extractTarEntry(tr, hdr, targetDir); err != nil {
			return err
		}
	}
}

func extractTarEntry(tr *tar.Reader, hdr *tar.Header, targetDir string) error {
	destPath, err := safeJoin(targetDir, hdr.Name)
	if err != nil {
		return err
	}

	switch hdr.Typeflag {
	case tar.TypeDir:
		return os.MkdirAll(destPath, 0o755)
	case tar.TypeReg:
		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			return fmt.Errorf("arboocr: could not create %s: %w", filepath.Dir(destPath), err)
		}
		out, err := os.OpenFile(destPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			return fmt.Errorf("arboocr: could not create %s: %w", destPath, err)
		}
		defer out.Close()
		if _, err := io.Copy(out, tr); err != nil {
			return fmt.Errorf("arboocr: could not extract %s: %w", hdr.Name, err)
		}
		return nil
	default:
		// Release archives contain only plain files and directories; skip
		// anything else (symlinks, etc).
		return nil
	}
}

// safeJoin joins name onto baseDir, rejecting archive entries that would
// escape baseDir via ".." path segments ("zip slip").
func safeJoin(baseDir, name string) (string, error) {
	dest := filepath.Join(baseDir, filepath.FromSlash(name))
	base := filepath.Clean(baseDir)
	if dest != base && !strings.HasPrefix(dest, base+string(os.PathSeparator)) {
		return "", fmt.Errorf("arboocr: archive entry escapes target directory: %s", name)
	}
	return dest, nil
}

// flattenSingleSubdir mirrors Installer.php's flattenSingleSubdir: the
// release archives contain one top-level folder (e.g.
// arboocr-windows-x64/...). This moves its contents up into targetDir so
// callers get targetDir/arboocr_demo directly, not
// targetDir/arboocr-windows-x64/arboocr_demo. Only flattens if there's
// exactly one entry and it's a directory.
func flattenSingleSubdir(targetDir string) error {
	entries, err := os.ReadDir(targetDir)
	if err != nil {
		return fmt.Errorf("arboocr: could not read %s: %w", targetDir, err)
	}
	if len(entries) != 1 || !entries[0].IsDir() {
		return nil
	}

	subdir := filepath.Join(targetDir, entries[0].Name())
	items, err := os.ReadDir(subdir)
	if err != nil {
		return fmt.Errorf("arboocr: could not read %s: %w", subdir, err)
	}
	for _, item := range items {
		oldPath := filepath.Join(subdir, item.Name())
		newPath := filepath.Join(targetDir, item.Name())
		if err := os.Rename(oldPath, newPath); err != nil {
			return fmt.Errorf("arboocr: could not move %s to %s: %w", oldPath, newPath, err)
		}
	}
	return os.Remove(subdir)
}
