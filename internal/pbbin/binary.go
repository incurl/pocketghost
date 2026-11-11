package pbbin

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/incurl/pocketghost/internal/logging"
)

// Platform identifies the target OS/arch for a binary.
type Platform struct {
	OS   string
	Arch string
}

// DetectPlatform returns the current host platform.
func DetectPlatform() Platform {
	arch := runtime.GOARCH
	switch arch {
	case "x86_64":
		arch = "amd64"
	}
	return Platform{OS: runtime.GOOS, Arch: arch}
}

// Suffix is the release asset suffix, e.g. `linux_amd64`.
func (p Platform) Suffix() string { return p.OS + "_" + p.Arch }

// BinaryName is the executable name inside the release archive.
func (p Platform) BinaryName() string {
	if p.OS == "windows" {
		return "pocketbase.exe"
	}
	return "pocketbase"
}

// AssetName is the GitHub release asset name.
func (p Platform) AssetName(version string) string {
	return fmt.Sprintf("pocketbase_%s_%s.zip", version, p.Suffix())
}

// Service manages `<root>/<version>/<os_arch>/pocketbase`.
type Service struct {
	Root       string
	Platform   Platform
	DownloadTo string
	Logger     *logging.Logger
	Client     *http.Client
}

// New creates a binary service rooted at root.
func New(root string, logger *logging.Logger) *Service {
	if logger == nil {
		logger = logging.Discard()
	}
	return &Service{
		Root:       root,
		Platform:   DetectPlatform(),
		DownloadTo: "https://github.com/pocketbase/pocketbase/releases/download",
		Logger:     logger,
		Client:     &http.Client{Timeout: 10 * time.Minute},
	}
}

// BinaryPath is the cached path for a version.
func (s *Service) BinaryPath(version string) string {
	return filepath.Join(s.Root, version, s.Platform.Suffix(), s.Platform.BinaryName())
}

// Versions lists the cached versions, newest first.
func (s *Service) Versions() []string {
	entries, err := os.ReadDir(s.Root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := ParseVersion(e.Name()); err != nil {
			continue
		}
		if _, err := os.Stat(s.BinaryPath(e.Name())); err != nil {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := ParseVersion(out[i])
		b, _ := ParseVersion(out[j])
		return a.Compare(b) > 0
	})
	return out
}

// Resolve returns the highest cached version satisfying rng.
func (s *Service) Resolve(rng string) (string, error) {
	v, ok := MaxSatisfying(s.Versions(), rng)
	if !ok {
		return "", fmt.Errorf("pbbin: no cached PocketBase version satisfies %q (run `pocketghost pocketbase update`)", rng)
	}
	return v, nil
}

// Download fetches and extracts one PocketBase release.
func (s *Service) Download(ctx context.Context, version string) (string, error) {
	if _, err := ParseVersion(version); err != nil {
		return "", err
	}
	asset := s.Platform.AssetName(version)
	url := fmt.Sprintf("%s/v%s/%s", strings.TrimSuffix(s.DownloadTo, "/"), version, asset)

	s.Logger.Infof("downloading PocketBase %s (%s)", version, s.Platform.Suffix())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := s.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("pbbin: download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("pbbin: download %s: status %d", url, resp.StatusCode)
	}

	tmpDir, err := os.MkdirTemp(s.Root, ".tmp-"+version+"-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)

	zipPath := filepath.Join(tmpDir, asset)
	f, err := os.Create(zipPath)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), resp.Body); err != nil {
		_ = f.Close()
		return "", err
	}
	_ = f.Close()

	sum := hex.EncodeToString(h.Sum(nil))
	if want, ok := s.manifestChecksum(version); ok && !strings.EqualFold(want, sum) {
		return "", fmt.Errorf("pbbin: checksum mismatch for %s: expected %s, got %s", asset, want, sum)
	}

	destDir := filepath.Join(s.Root, version, s.Platform.Suffix())
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", err
	}
	if err := extractBinary(zipPath, destDir, s.Platform.BinaryName()); err != nil {
		return "", err
	}

	binPath := s.BinaryPath(version)
	if _, err := os.Stat(binPath); err != nil {
		return "", fmt.Errorf("pbbin: extracted binary missing at %s", binPath)
	}
	if s.Platform.OS != "windows" {
		if err := os.Chmod(binPath, 0o755); err != nil {
			return "", err
		}
	}
	s.Logger.Infof("PocketBase %s ready at %s (sha256 %s)", version, binPath, sum)
	return binPath, nil
}

// manifestChecksum looks for an optional `versions.json` manifest with sha256
// values, mirroring the legacy manifest.ts behaviour.
func (s *Service) manifestChecksum(version string) (string, bool) {
	raw, err := os.ReadFile(filepath.Join(s.Root, "versions.json"))
	if err != nil {
		return "", false
	}
	var manifest struct {
		Versions []struct {
			Version string `json:"version"`
			SHA256  string `json:"sha256"`
		} `json:"versions"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return "", false
	}
	for _, v := range manifest.Versions {
		if v.Version == version && v.SHA256 != "" {
			return v.SHA256, true
		}
	}
	return "", false
}

func extractBinary(zipPath, destDir, binaryName string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("pbbin: open zip: %w", err)
	}
	defer zr.Close()

	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if filepath.Base(f.Name) != binaryName {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer rc.Close()

		outPath := filepath.Join(destDir, binaryName)
		out, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, rc); err != nil {
			_ = out.Close()
			return err
		}
		return out.Close()
	}
	return fmt.Errorf("pbbin: %s not found in archive", binaryName)
}

// EnsureBinary returns the cached binary path, downloading it when missing.
func (s *Service) EnsureBinary(ctx context.Context, version string) (string, error) {
	path := s.BinaryPath(version)
	if st, err := os.Stat(path); err == nil && !st.IsDir() {
		return path, nil
	}
	return s.Download(ctx, version)
}
