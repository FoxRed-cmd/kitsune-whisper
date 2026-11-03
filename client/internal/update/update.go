// Package update checks GitHub Releases for a newer kitsune-whisper build,
// downloads the checksummed installer on request, verifies its SHA-256, and
// launches it.
//
// The package performs no work at construction time. Check is one unauthenticated
// GitHub API request; Install downloads only the asset this platform can run.
// The HTTP client, release base URL, download directory, and process launcher
// are injectable so tests never touch the network or start an installer.
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/version"
)

const (
	// DefaultRepository is the GitHub owner/name the Client checks.
	DefaultRepository = "FoxRed-cmd/kitsune-whisper"
	// ChecksumsAsset is the release file that names every artifact's SHA-256.
	ChecksumsAsset = "checksums.txt"
	// DownloadLimit bounds one installer download. The Server bundle and the
	// Windows setup are tens of megabytes; this leaves room without allowing a
	// hostile response to fill the disk.
	DownloadLimit = 512 << 20
	// ChecksumLimit bounds checksums.txt.
	ChecksumLimit = 1 << 20
)

// ErrUnsupportedPlatform reports a host with no published installer. The
// Client still shows the available version and its release page.
var ErrUnsupportedPlatform = errors.New("update: no installer is published for this platform")

// Info is the result of one release check.
type Info struct {
	// Current is the running Client version.
	Current string `json:"current"`
	// Latest is the newest release tag, without its leading "v".
	Latest string `json:"latest"`
	// Tag is the raw release tag, e.g. "v0.2.0".
	Tag string `json:"tag"`
	// Available reports that Latest is newer than Current.
	Available bool `json:"available"`
	// URL is the release page.
	URL string `json:"url"`
	// Notes is the release body, shown in Settings.
	Notes string `json:"notes,omitempty"`
	// Asset is the installer filename this platform would download.
	Asset string `json:"asset,omitempty"`
	// Installable reports that an installer asset exists for this platform.
	Installable bool `json:"installable"`
	// AssetURL is the release asset's download URL. It is not rendered and is
	// only used by Install.
	AssetURL string `json:"-"`
	// ChecksumsURL is the release checksums file's download URL.
	ChecksumsURL string `json:"-"`
}

// Runner launches an installer. Production starts the process detached;
// tests record the call.
type Runner func(path string, args []string, env []string) error

// Downloader fetches one URL into w. Nil uses the Service's HTTP client.
type Downloader func(ctx context.Context, url string, w io.Writer) error

// Options wires a Service.
type Options struct {
	// Repository is owner/name on GitHub; "" uses DefaultRepository.
	Repository string
	// Current is the running Client version.
	Current string
	// Platform and Arch default to runtime.GOOS and runtime.GOARCH.
	Platform string
	Arch     string
	// Client is the HTTP client. Nil uses a 30-second-timeout client.
	Client *http.Client
	// APIBase is the GitHub API origin; "" uses https://api.github.com.
	APIBase string
	// DownloadDir holds the verified installer; "" uses the OS temporary
	// directory.
	DownloadDir string
	// Run launches the verified installer. Nil uses startDetached.
	Run Runner
	// Download fetches an asset. Nil uses Client.
	Download Downloader
}

// Service checks and installs updates.
type Service struct {
	opts Options
}

// New builds a Service.
func New(opts Options) *Service {
	if opts.Repository == "" {
		opts.Repository = DefaultRepository
	}
	if opts.Platform == "" {
		opts.Platform = runtime.GOOS
	}
	if opts.Arch == "" {
		opts.Arch = runtime.GOARCH
	}
	if opts.Client == nil {
		opts.Client = &http.Client{Timeout: 30 * time.Second}
	}
	if opts.APIBase == "" {
		opts.APIBase = "https://api.github.com"
	}
	return &Service{opts: opts}
}

// release is the part of the GitHub release body the Client uses.
type release struct {
	TagName string  `json:"tag_name"`
	HTMLURL string  `json:"html_url"`
	Body    string  `json:"body"`
	Assets  []asset `json:"assets"`
}

type asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

// Check reads the latest release and compares it with the running version.
// An unparseable local version (a "dev" build) still reports the latest tag but
// never claims that an update is available.
func (s *Service) Check(ctx context.Context) (Info, error) {
	endpoint := strings.TrimRight(s.opts.APIBase, "/") + "/repos/" + s.opts.Repository + "/releases/latest"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Info{}, fmt.Errorf("update: build the release request: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "kitsune-whisper/"+s.opts.Current)
	response, err := s.opts.Client.Do(request)
	if err != nil {
		return Info{}, fmt.Errorf("update: check GitHub Releases: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return Info{}, fmt.Errorf("update: read the release response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return Info{}, fmt.Errorf(
			"update: GitHub returned HTTP %d: %s",
			response.StatusCode,
			strings.TrimSpace(string(data)),
		)
	}
	var published release
	if err := json.Unmarshal(data, &published); err != nil {
		return Info{}, fmt.Errorf("update: decode the release response: %w", err)
	}
	if strings.TrimSpace(published.TagName) == "" {
		return Info{}, errors.New("update: the latest release has no tag")
	}
	info := Info{
		Current: s.opts.Current,
		Latest:  strings.TrimPrefix(strings.TrimPrefix(published.TagName, "v"), "V"),
		Tag:     published.TagName,
		URL:     published.HTMLURL,
		Notes:   published.Body,
	}
	info.Available = version.Newer(published.TagName, s.opts.Current)
	name, err := InstallerAsset(s.opts.Platform, s.opts.Arch)
	if err == nil {
		for _, candidate := range published.Assets {
			if candidate.Name == name {
				info.Asset = name
				info.AssetURL = candidate.URL
				info.Installable = true
			}
			if candidate.Name == ChecksumsAsset {
				info.ChecksumsURL = candidate.URL
			}
		}
	}
	// Without the release's own checksums there is nothing to verify the
	// installer against, so it is not offered for installation.
	info.Installable = info.Installable && info.ChecksumsURL != ""
	return info, nil
}

// InstallerAsset is the release asset this platform can install. Windows runs
// the per-user setup; Linux runs the same install.sh the release publishes.
func InstallerAsset(platform, arch string) (string, error) {
	switch platform {
	case "windows":
		if arch == "amd64" {
			return "kitsune-whisper_windows_amd64_setup.exe", nil
		}
	case "linux":
		if arch == "amd64" {
			return "install.sh", nil
		}
	}
	return "", fmt.Errorf("%w: %s/%s", ErrUnsupportedPlatform, platform, arch)
}

// Install downloads the release's installer, verifies it against checksums.txt,
// and launches it. The returned path is the verified file.
func (s *Service) Install(ctx context.Context, info Info) (string, error) {
	if !info.Installable || info.Asset == "" {
		return "", ErrUnsupportedPlatform
	}
	if info.Tag == "" {
		return "", errors.New("update: the release tag is missing")
	}
	if info.AssetURL == "" || info.ChecksumsURL == "" {
		return "", errors.New("update: the release has no installer or checksums URL; check again")
	}
	checksums, err := s.fetchChecksums(ctx, info.ChecksumsURL)
	if err != nil {
		return "", err
	}
	expected, ok := checksums[info.Asset]
	if !ok {
		return "", fmt.Errorf("update: %s has no checksum in %s", info.Asset, ChecksumsAsset)
	}
	if err := os.MkdirAll(s.downloadDir(), 0o700); err != nil {
		return "", fmt.Errorf("update: create %s: %w", s.downloadDir(), err)
	}
	if err := os.Chmod(s.downloadDir(), 0o700); err != nil {
		return "", fmt.Errorf("update: secure %s: %w", s.downloadDir(), err)
	}
	path := filepath.Join(s.downloadDir(), info.Asset)
	if err := s.downloadVerified(ctx, info.AssetURL, path, expected); err != nil {
		return "", err
	}
	if s.opts.Platform != "windows" {
		if err := os.Chmod(path, 0o700); err != nil {
			return "", fmt.Errorf("update: make %s executable: %w", path, err)
		}
	}
	run := s.opts.Run
	if run == nil {
		run = startDetached
	}
	if err := run(path, nil, launchEnv(s.opts.Platform, info.Tag)); err != nil {
		return "", fmt.Errorf("update: launch %s: %w", path, err)
	}
	return path, nil
}

// fetchChecksums downloads checksums.txt and parses "digest  name" lines.
func (s *Service) fetchChecksums(ctx context.Context, url string) (map[string]string, error) {
	var buffer strings.Builder
	err := s.download(ctx, url, &limitedWriter{w: &buffer, left: ChecksumLimit})
	if err != nil {
		return nil, fmt.Errorf("update: download %s: %w", ChecksumsAsset, err)
	}
	checksums := map[string]string{}
	for _, line := range strings.Split(buffer.String(), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[len(fields)-1], "*")
		checksums[name] = strings.ToLower(fields[0])
	}
	if len(checksums) == 0 {
		return nil, fmt.Errorf("update: %s lists no checksums", ChecksumsAsset)
	}
	return checksums, nil
}

// downloadVerified streams one asset to path and rejects a checksum mismatch.
func (s *Service) downloadVerified(ctx context.Context, url, path, expected string) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".kitsune-update-*")
	if err != nil {
		return fmt.Errorf("update: create %s: %w", path, err)
	}
	name := temp.Name()
	defer func() {
		_ = temp.Close()
		_ = os.Remove(name)
	}()
	hash := sha256.New()
	writer := io.MultiWriter(temp, hash)
	if err := s.download(ctx, url, writer); err != nil {
		return fmt.Errorf("update: download %s: %w", url, err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("update: sync %s: %w", name, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("update: close %s: %w", name, err)
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf(
			"update: checksum mismatch for %s: expected %s, got %s",
			filepath.Base(path),
			expected,
			actual,
		)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("update: replace %s: %w", path, err)
	}
	return nil
}

func (s *Service) download(ctx context.Context, url string, w io.Writer) error {
	if s.opts.Download != nil {
		return s.opts.Download(ctx, url, w)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "kitsune-whisper/"+s.opts.Current)
	response, err := s.opts.Client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 512))
		return fmt.Errorf("HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(data)))
	}
	if response.ContentLength > DownloadLimit {
		return fmt.Errorf("the download is %d bytes, over the %d-byte limit", response.ContentLength, DownloadLimit)
	}
	_, err = io.Copy(&limitedWriter{w: w, left: DownloadLimit}, response.Body)
	return err
}

func (s *Service) downloadDir() string {
	if s.opts.DownloadDir != "" {
		return s.opts.DownloadDir
	}
	// A per-user cache directory cannot be pre-created or swapped by another
	// local user between the checksum check and the launch.
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "kitsune-whisper", "updates")
}

// launchEnv pins the Linux installer to the release that was checked. The
// Windows setup already carries its version and shows its own progress; it
// stops the running Client and Server before replacing their files.
func launchEnv(platform, tag string) []string {
	if platform == "windows" {
		return nil
	}
	return []string{"KITSUNE_VERSION=" + tag}
}

func startDetached(path string, args []string, env []string) error {
	// Implemented per platform in run_*.go.
	return start(path, args, env)
}

type limitedWriter struct {
	w    io.Writer
	left int64
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > l.left {
		return 0, fmt.Errorf("response is larger than %d bytes", l.left)
	}
	l.left -= int64(len(p))
	return l.w.Write(p)
}
