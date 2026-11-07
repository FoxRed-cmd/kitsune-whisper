// Package update checks GitHub Releases for a newer kitsune-whisper build and
// stages a verified set of artifacts for the installed Components. The
// installer is still available through Install, but the Settings flow downloads
// a complete set first and only installs from it on a later ticket.
//
// The package performs no work at construction time. Check is one unauthenticated
// GitHub API request; Download streams every selected artifact, verifies its
// SHA-256 against the release checksums, and keeps the set in a per-tag
// directory. The HTTP client, release base URL, download directory, and process
// launcher are injectable so tests never touch the network or start an installer.
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
	"sort"
	"strings"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/components"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/version"
)

const (
	// DefaultRepository is the GitHub owner/name the Client checks.
	DefaultRepository = "FoxRed-cmd/kitsune-whisper"
	// ChecksumsAsset is the release file that names every artifact's SHA-256.
	ChecksumsAsset = "checksums.txt"
	// SignatureAsset is the detached signature over ChecksumsAsset.
	SignatureAsset = "checksums.txt.sig"
	// VersionAsset is the release's VERSION file.
	VersionAsset = "VERSION"
	// DownloadLimit bounds one artifact download. The Server bundle and the
	// Windows Client are tens of megabytes; this leaves room without allowing a
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
	// Assets is the full release asset list, so Download can find the
	// signature, VERSION, and the Component archives as well as the installer.
	Assets []Asset `json:"-"`
}

// Asset is one file attached to the release.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

// Stage is a complete, checksum-verified update set for one release tag.
type Stage struct {
	Tag        string         `json:"tag"`
	Dir        string         `json:"dir"`
	Components components.Set `json:"components"`
	Files      []string       `json:"files"`
}

// HasClient reports whether the staged set covers the Client.
func (s Stage) HasClient() bool { return s.Components.Client }

// HasServer reports whether the staged set covers the Local Server.
func (s Stage) HasServer() bool { return s.Components.Server }

// InstallerPath returns the staged installer for either platform, or "" when
// the manifest lists none.
func (s Stage) InstallerPath() string {
	for _, name := range s.Files {
		if name == "install.sh" || name == "install.ps1" {
			return filepath.Join(s.Dir, name)
		}
	}
	return ""
}

// Progress is one aggregate download sample. Received and Total are summed over
// every file in the set; Total is 0 when any selected file's size is unknown.
// File is the artifact currently being written.
type Progress struct {
	File     string
	Received int64
	Total    int64
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
	// Components is the set installed on this machine. It selects the
	// Component archives to stage; the active Server profile is irrelevant.
	// The zero value means Client-only.
	Components components.Set
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
	if !opts.Components.Any() {
		opts.Components = components.Set{Client: true}
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
	info.Assets = make([]Asset, 0, len(published.Assets))
	for _, candidate := range published.Assets {
		info.Assets = append(info.Assets, Asset(candidate))
	}
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
// the same install.ps1 the release publishes; Linux runs install.sh.
func InstallerAsset(platform, arch string) (string, error) {
	switch platform {
	case "windows":
		if arch == "amd64" {
			return "install.ps1", nil
		}
	case "linux":
		if arch == "amd64" {
			return "install.sh", nil
		}
	}
	return "", fmt.Errorf("%w: %s/%s", ErrUnsupportedPlatform, platform, arch)
}

// ComponentAssets names the Client and Local Server archives for a platform.
// The Local Server bundle is a tar.gz on both platforms.
func ComponentAssets(platform, arch string) (client, server string, err error) {
	switch platform {
	case "windows":
		if arch == "amd64" {
			return "kitsune-client_windows_amd64.zip", "kitsune-server_windows_amd64.tar.gz", nil
		}
	case "linux":
		if arch == "amd64" {
			return "kitsune-client_linux_amd64.tar.gz", "kitsune-server_linux_amd64.tar.gz", nil
		}
	}
	return "", "", fmt.Errorf("%w: %s/%s", ErrUnsupportedPlatform, platform, arch)
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
	var args []string
	if s.opts.Platform == "windows" {
		// The updater downloaded a script, not an executable: PowerShell runs
		// it pinned to the release that was just verified.
		args = []string{"-Version", info.Tag}
	}
	if err := run(path, args, launchEnv(s.opts.Platform, info.Tag)); err != nil {
		return "", fmt.Errorf("update: launch %s: %w", path, err)
	}
	return path, nil
}

// downloadItem is one artifact Download will fetch into the stage.
type downloadItem struct {
	name       string
	url        string
	size       int64
	verify     bool
	synthesize bool
}

// Download stages every artifact needed to install info's release on this
// machine: the installer, checksums.txt, its signature, VERSION, and the
// archives of the installed Components. Each file is checked against the
// release checksums while it streams. Nothing is installed.
func (s *Service) Download(ctx context.Context, info Info, progress func(Progress)) (Stage, error) {
	if !info.Available || !info.Installable {
		return Stage{}, errors.New("update: no installable release is available to download")
	}
	if !validTag(info.Tag) {
		return Stage{}, errors.New("update: the release tag is missing or invalid")
	}
	if info.Asset == "" || info.AssetURL == "" || info.ChecksumsURL == "" {
		return Stage{}, errors.New("update: the release has no installer or checksums URL; check again")
	}
	clientAsset, serverAsset, err := ComponentAssets(s.opts.Platform, s.opts.Arch)
	if err != nil {
		return Stage{}, err
	}

	byName := make(map[string]Asset, len(info.Assets))
	for _, asset := range info.Assets {
		byName[asset.Name] = asset
	}
	lookup := func(name, fallbackURL string) (Asset, bool) {
		if asset, ok := byName[name]; ok && asset.URL != "" {
			return asset, true
		}
		if fallbackURL == "" {
			return Asset{}, false
		}
		return Asset{Name: name, URL: fallbackURL}, true
	}

	set := s.opts.Components
	if !set.Any() {
		set = components.Set{Client: true}
	}

	items := make([]downloadItem, 0, 6)
	add := func(name, fallbackURL string, verify bool) error {
		asset, ok := lookup(name, fallbackURL)
		if !ok {
			return fmt.Errorf("update: the release does not list %s", name)
		}
		items = append(items, downloadItem{name: name, url: asset.URL, size: asset.Size, verify: verify})
		return nil
	}

	// checksums.txt must arrive first: every other verified file is checked
	// against it as it streams.
	if err := add(ChecksumsAsset, info.ChecksumsURL, false); err != nil {
		return Stage{}, err
	}
	if err := add(info.Asset, info.AssetURL, true); err != nil {
		return Stage{}, err
	}
	if err := add(SignatureAsset, "", false); err != nil {
		return Stage{}, err
	}
	versionBody := strings.TrimPrefix(strings.TrimPrefix(info.Latest, "v"), "V") + "\n"
	if _, ok := byName[VersionAsset]; ok {
		if err := add(VersionAsset, "", true); err != nil {
			return Stage{}, err
		}
	} else {
		items = append(items, downloadItem{
			name: VersionAsset, size: int64(len(versionBody)), verify: false, synthesize: true,
		})
	}
	if err := add(clientAsset, "", true); err != nil {
		return Stage{}, err
	}
	if set.Server {
		if err := add(serverAsset, "", true); err != nil {
			return Stage{}, err
		}
	}

	root := s.downloadDir()
	if err := os.MkdirAll(root, 0o700); err != nil {
		return Stage{}, fmt.Errorf("update: create %s: %w", root, err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return Stage{}, fmt.Errorf("update: secure %s: %w", root, err)
	}
	tempDir, err := os.MkdirTemp(root, info.Tag+".tmp-*")
	if err != nil {
		return Stage{}, fmt.Errorf("update: create a temporary stage: %w", err)
	}
	// A failed or cancelled download must never leave partial files behind. A
	// complete stage for the same tag is replaced only after the whole new set
	// has been verified.
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(tempDir)
		}
	}()

	state := &progressState{progress: progress}
	for _, item := range items {
		if item.size > 0 {
			state.total += item.size
		} else {
			state.unknown = true
		}
	}
	if state.unknown {
		state.total = 0
	}
	if progress != nil {
		progress(Progress{Total: state.total})
	}

	var checksums map[string]string
	for _, item := range items {
		path := filepath.Join(tempDir, item.name)
		if item.synthesize {
			if err := os.WriteFile(path, []byte(versionBody), 0o600); err != nil {
				return Stage{}, fmt.Errorf("update: write %s: %w", VersionAsset, err)
			}
			state.advance(item.name, int64(len(versionBody)))
			continue
		}
		if item.name == ChecksumsAsset {
			if err := s.downloadStageFile(ctx, item.url, path, "", state); err != nil {
				return Stage{}, err
			}
			parsed, err := readChecksums(path)
			if err != nil {
				return Stage{}, err
			}
			checksums = parsed
			continue
		}
		expected := ""
		if item.verify {
			found, ok := checksums[item.name]
			if !ok {
				return Stage{}, fmt.Errorf("update: %s has no checksum in %s", item.name, ChecksumsAsset)
			}
			expected = found
		}
		if err := s.downloadStageFile(ctx, item.url, path, expected, state); err != nil {
			return Stage{}, err
		}
	}

	if s.opts.Platform != "windows" {
		installer := filepath.Join(tempDir, info.Asset)
		if err := os.Chmod(installer, 0o700); err != nil {
			return Stage{}, fmt.Errorf("update: make %s executable: %w", installer, err)
		}
	}

	files := make([]string, 0, len(items))
	for _, item := range items {
		files = append(files, item.name)
	}
	sort.Strings(files)
	finalDir := s.stageDir(info.Tag)
	stage := Stage{Tag: info.Tag, Dir: finalDir, Components: set, Files: files}
	if err := writeStageManifest(filepath.Join(tempDir, stageManifest), stage); err != nil {
		return Stage{}, err
	}
	// Move an existing set aside instead of deleting it first, so a failed
	// publish restores the complete set that was already there.
	backup := ""
	if _, err := os.Stat(finalDir); err == nil {
		backup = tempDir + ".previous"
		if err := os.Rename(finalDir, backup); err != nil {
			return Stage{}, fmt.Errorf("update: replace the stage: %w", err)
		}
	}
	if err := os.Rename(tempDir, finalDir); err != nil {
		if backup != "" {
			_ = os.Rename(backup, finalDir)
		}
		return Stage{}, fmt.Errorf("update: publish the stage: %w", err)
	}
	complete = true
	if backup != "" {
		_ = os.RemoveAll(backup)
	}
	s.pruneStages(info.Tag)
	return stage, nil
}

// progressState accumulates aggregate bytes across the selected files.
type progressState struct {
	total    int64
	received int64
	unknown  bool
	file     string
	progress func(Progress)
}

func (p *progressState) writer(file string, w io.Writer) io.Writer {
	return writerFunc(func(chunk []byte) (int, error) {
		n, err := w.Write(chunk)
		p.advance(file, int64(n))
		return n, err
	})
}

func (p *progressState) advance(file string, n int64) {
	p.file = file
	p.received += n
	if p.progress == nil {
		return
	}
	total := p.total
	if p.unknown {
		total = 0
	}
	p.progress(Progress{File: file, Received: p.received, Total: total})
}

type writerFunc func([]byte) (int, error)

func (w writerFunc) Write(p []byte) (int, error) { return w(p) }

// downloadStageFile streams one artifact into the temporary stage and verifies
// its SHA-256 when expected is non-empty.
func (s *Service) downloadStageFile(ctx context.Context, url, path, expected string, state *progressState) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".part-*")
	if err != nil {
		return fmt.Errorf("update: create %s: %w", filepath.Base(path), err)
	}
	name := temp.Name()
	defer func() {
		_ = temp.Close()
		_ = os.Remove(name)
	}()
	hash := sha256.New()
	writer := state.writer(filepath.Base(path), io.MultiWriter(temp, hash))
	if err := s.download(ctx, url, writer); err != nil {
		return fmt.Errorf("update: download %s: %w", url, err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("update: sync %s: %w", filepath.Base(path), err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("update: close %s: %w", filepath.Base(path), err)
	}
	if expected != "" {
		actual := hex.EncodeToString(hash.Sum(nil))
		if !strings.EqualFold(actual, expected) {
			return fmt.Errorf(
				"update: checksum mismatch for %s: expected %s, got %s",
				filepath.Base(path),
				expected,
				actual,
			)
		}
	}
	if err := os.Chmod(name, 0o600); err != nil {
		return fmt.Errorf("update: secure %s: %w", filepath.Base(path), err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("update: finish %s: %w", filepath.Base(path), err)
	}
	return nil
}

// stageManifest is the file that marks a directory as a complete staged set.
const stageManifest = "stage.json"

// Staged returns the complete set for tag. A missing, corrupt, mismatched, or
// incomplete directory is reported as absent so the Client downloads again.
func (s *Service) Staged(tag string) (Stage, bool) {
	if !validTag(tag) {
		return Stage{}, false
	}
	dir := s.stageDir(tag)
	data, err := os.ReadFile(filepath.Join(dir, stageManifest))
	if err != nil {
		return Stage{}, false
	}
	var stage Stage
	if err := json.Unmarshal(data, &stage); err != nil {
		return Stage{}, false
	}
	if stage.Tag != tag || len(stage.Files) == 0 {
		return Stage{}, false
	}
	for _, name := range stage.Files {
		if !validFileName(name) {
			return Stage{}, false
		}
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil || !info.Mode().IsRegular() {
			return Stage{}, false
		}
	}
	stage.Dir = dir
	return stage, true
}

// VerifyStaged re-reads checksums.txt from the stage and re-verifies every
// listed file. The signature is verified by the installer, not here.
func (s *Service) VerifyStaged(ctx context.Context, stage Stage) error {
	if stage.Dir == "" || !validTag(stage.Tag) {
		return errors.New("update: the staged set is invalid")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	checksums, err := readChecksums(filepath.Join(stage.Dir, ChecksumsAsset))
	if err != nil {
		return err
	}
	for _, name := range stage.Files {
		if name == ChecksumsAsset || name == SignatureAsset {
			continue
		}
		if !validFileName(name) {
			return fmt.Errorf("update: the staged set lists an invalid file %q", name)
		}
		expected, ok := checksums[name]
		if !ok {
			// VERSION may be synthesized when the release does not list it.
			if name == VersionAsset {
				continue
			}
			return fmt.Errorf("update: %s has no checksum in %s", name, ChecksumsAsset)
		}
		if err := verifyFile(filepath.Join(stage.Dir, name), expected); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return nil
}

// ClearStaged removes a staged set. It is harmless when the set is absent.
func (s *Service) ClearStaged(tag string) error {
	if !validTag(tag) {
		return fmt.Errorf("update: invalid release tag %q", tag)
	}
	return os.RemoveAll(s.stageDir(tag))
}

// pruneStages removes every other staged set under the download root so the
// newest tag replaces an older one.
func (s *Service) pruneStages(keep string) {
	entries, err := os.ReadDir(s.downloadDir())
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == keep {
			continue
		}
		_ = os.RemoveAll(filepath.Join(s.downloadDir(), entry.Name()))
	}
}

func (s *Service) stageDir(tag string) string {
	return filepath.Join(s.downloadDir(), tag)
}

func validTag(tag string) bool {
	if tag == "" || tag == "." || tag == ".." {
		return false
	}
	return tag == filepath.Base(tag) && !strings.ContainsAny(tag, `/\`)
}

func validFileName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	return name == filepath.Base(name) && !strings.ContainsAny(name, `/\`)
}

func writeStageManifest(path string, stage Stage) error {
	data, err := json.MarshalIndent(stage, "", "  ")
	if err != nil {
		return fmt.Errorf("update: encode the stage manifest: %w", err)
	}
	data = append(data, '\n')
	temp, err := os.CreateTemp(filepath.Dir(path), ".stage-*")
	if err != nil {
		return fmt.Errorf("update: create the stage manifest: %w", err)
	}
	name := temp.Name()
	defer func() {
		_ = temp.Close()
		_ = os.Remove(name)
	}()
	if err := temp.Chmod(0o600); err != nil {
		return fmt.Errorf("update: secure the stage manifest: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		return fmt.Errorf("update: write the stage manifest: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("update: sync the stage manifest: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("update: close the stage manifest: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("update: publish the stage manifest: %w", err)
	}
	return nil
}

func readChecksums(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("update: read %s: %w", filepath.Base(path), err)
	}
	return parseChecksums(string(data))
}

func parseChecksums(data string) (map[string]string, error) {
	checksums := map[string]string{}
	for _, line := range strings.Split(data, "\n") {
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

func verifyFile(path, expected string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("update: verify %s: %w", filepath.Base(path), err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, io.LimitReader(file, DownloadLimit+1)); err != nil {
		return fmt.Errorf("update: verify %s: %w", filepath.Base(path), err)
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
	return nil
}

// fetchChecksums downloads checksums.txt and parses "digest  name" lines.
func (s *Service) fetchChecksums(ctx context.Context, url string) (map[string]string, error) {
	var buffer strings.Builder
	err := s.download(ctx, url, &limitedWriter{w: &buffer, left: ChecksumLimit})
	if err != nil {
		return nil, fmt.Errorf("update: download %s: %w", ChecksumsAsset, err)
	}
	return parseChecksums(buffer.String())
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

// launchEnv pins the Linux installer to the release that was checked. Windows
// passes the same tag as an argument because install.ps1 is a script.
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
