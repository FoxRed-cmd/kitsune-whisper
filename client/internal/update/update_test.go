package update_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/components"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/update"
)

type fixtureOptions struct {
	tag              string
	installerName    string
	installerBody    string
	checksumOverride string
	omitVersion      bool
	unknownSizes     bool
	blockAsset       string
}

type fixture struct {
	server       *httptest.Server
	tag          string
	installer    string
	checksum     string
	dir          string
	checksums    string
	bodies       map[string]string
	sizes        map[string]int64
	omitVersion  bool
	unknownSizes bool
	blockAsset   string
	blockStarted chan struct{}

	mu       sync.Mutex
	requests []string
}

func newFixture(t *testing.T, installerName, installerBody, checksumOverride string) *fixture {
	t.Helper()
	return newFixtureWith(t, fixtureOptions{
		tag:              "v0.2.0",
		installerName:    installerName,
		installerBody:    installerBody,
		checksumOverride: checksumOverride,
	})
}

func newFixtureWith(t *testing.T, opts fixtureOptions) *fixture {
	t.Helper()
	if opts.tag == "" {
		opts.tag = "v0.2.0"
	}
	f := &fixture{
		dir:          t.TempDir(),
		tag:          opts.tag,
		installer:    opts.installerBody,
		bodies:       map[string]string{},
		sizes:        map[string]int64{},
		omitVersion:  opts.omitVersion,
		unknownSizes: opts.unknownSizes,
		blockAsset:   opts.blockAsset,
		blockStarted: make(chan struct{}),
	}
	clientAsset, serverAsset, err := update.ComponentAssets(platformFor(opts.installerName), "amd64")
	if err != nil {
		t.Fatal(err)
	}
	f.bodies[opts.installerName] = opts.installerBody
	f.bodies[update.SignatureAsset] = "signature"
	f.bodies[clientAsset] = "client archive"
	f.bodies[serverAsset] = "server archive"
	if !opts.omitVersion {
		f.bodies[update.VersionAsset] = strings.TrimPrefix(opts.tag, "v") + "\n"
	}
	f.checksums = f.buildChecksums(opts.installerName, opts.checksumOverride)
	f.checksum = sha256Hex(opts.installerBody)
	if opts.checksumOverride != "" {
		f.checksum = opts.checksumOverride
	}
	for name, body := range f.bodies {
		f.sizes[name] = int64(len(body))
	}
	f.sizes[update.ChecksumsAsset] = int64(len(f.checksums))

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/FoxRed-cmd/kitsune-whisper/releases/latest", func(rw http.ResponseWriter, req *http.Request) {
		f.record(req.URL.Path)
		order := f.assetOrder()
		assets := make([]map[string]any, 0, len(order))
		for _, name := range order {
			asset := map[string]any{
				"name":                 name,
				"browser_download_url": f.server.URL + "/download/" + f.tag + "/" + name,
			}
			if !f.unknownSizes {
				asset["size"] = f.sizes[name]
			}
			assets = append(assets, asset)
		}
		release := map[string]any{
			"tag_name": f.tag,
			"html_url": "https://example.invalid/releases/" + f.tag,
			"body":     "Notes",
			"assets":   assets,
		}
		_ = json.NewEncoder(rw).Encode(release)
	})
	mux.HandleFunc("/download/", func(rw http.ResponseWriter, req *http.Request) {
		f.record(req.URL.Path)
		name := filepath.Base(req.URL.Path)
		if name == update.ChecksumsAsset {
			_, _ = rw.Write([]byte(f.checksums))
			return
		}
		body, ok := f.bodies[name]
		if !ok {
			http.NotFound(rw, req)
			return
		}
		if f.blockAsset == name {
			half := len(body) / 2
			if half == 0 && len(body) > 0 {
				half = 1
			}
			_, _ = rw.Write([]byte(body[:half]))
			if flusher, ok := rw.(http.Flusher); ok {
				flusher.Flush()
			}
			select {
			case <-f.blockStarted:
			default:
				close(f.blockStarted)
			}
			<-req.Context().Done()
			return
		}
		_, _ = rw.Write([]byte(body))
	})
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func platformFor(installerName string) string {
	if installerName == "install.ps1" {
		return "windows"
	}
	return "linux"
}

func (f *fixture) buildChecksums(installerName, override string) string {
	var out strings.Builder
	for _, name := range f.assetOrder() {
		if name == update.ChecksumsAsset || name == update.SignatureAsset {
			continue
		}
		digest := sha256Hex(f.bodies[name])
		if name == installerName && override != "" {
			digest = override
		}
		fmt.Fprintf(&out, "%s  %s\n", digest, name)
	}
	return out.String()
}

func (f *fixture) assetOrder() []string {
	order := []string{f.installerName(), update.ChecksumsAsset, update.SignatureAsset, update.VersionAsset}
	clientAsset, serverAsset, err := update.ComponentAssets(platformFor(f.installerName()), "amd64")
	if err == nil {
		order = append(order, clientAsset, serverAsset)
	}
	names := make([]string, 0, len(order))
	for _, name := range order {
		if name == update.VersionAsset && f.omitVersion {
			continue
		}
		if _, ok := f.bodies[name]; ok || name == update.ChecksumsAsset {
			names = append(names, name)
		}
	}
	return names
}

func (f *fixture) installerName() string {
	for name := range f.bodies {
		if name == "install.sh" || name == "install.ps1" {
			return name
		}
	}
	return ""
}

func (f *fixture) record(path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, path)
}

func (f *fixture) requested(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, path := range f.requests {
		if filepath.Base(path) == name {
			return true
		}
	}
	return false
}

func (f *fixture) servedBytes() int64 {
	var total int64
	for _, size := range f.sizes {
		total += size
	}
	return total
}

func sha256Hex(body string) string {
	digest := sha256.Sum256([]byte(body))
	return hex.EncodeToString(digest[:])
}

func service(t *testing.T, f *fixture, opts update.Options) *update.Service {
	t.Helper()
	opts.APIBase = f.server.URL
	opts.Client = f.server.Client()
	opts.DownloadDir = f.dir
	return update.New(opts)
}

func check(t *testing.T, svc *update.Service) update.Info {
	t.Helper()
	info, err := svc.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func timeoutAfter(t *testing.T) <-chan time.Time {
	t.Helper()
	return time.After(2 * time.Second)
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestCheckReportsTheAvailableLinuxInstaller(t *testing.T) {
	f := newFixture(t, "install.sh", "#!/bin/sh\nexit 0\n", "")
	svc := service(t, f, update.Options{Current: "v0.1.4", Platform: "linux", Arch: "amd64"})

	info, err := svc.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !info.Available || info.Latest != "0.2.0" || info.Tag != "v0.2.0" {
		t.Fatalf("info = %+v", info)
	}
	if info.Asset != "install.sh" || !info.Installable {
		t.Fatalf("linux asset = %+v", info)
	}
	if info.AssetURL == "" || info.ChecksumsURL == "" {
		t.Fatal("download URLs were not captured")
	}
	if len(info.Assets) == 0 {
		t.Fatal("the release asset list was not captured")
	}
}

func TestCheckSelectsTheWindowsInstaller(t *testing.T) {
	f := newFixture(t, "install.ps1", "install", "")
	svc := service(t, f, update.Options{Current: "0.1.0", Platform: "windows", Arch: "amd64"})

	info, err := svc.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Asset != "install.ps1" || !info.Installable {
		t.Fatalf("windows asset = %+v", info)
	}
}

func TestCheckDoesNotOfferANewerReleaseToALocalDevBuild(t *testing.T) {
	f := newFixture(t, "install.sh", "x", "")
	svc := service(t, f, update.Options{Current: "dev", Platform: "linux", Arch: "amd64"})

	info, err := svc.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Available {
		t.Fatal("a dev build must not claim an update is available")
	}
}

func TestUnsupportedPlatformChecksButCannotInstall(t *testing.T) {
	f := newFixture(t, "install.sh", "x", "")
	svc := service(t, f, update.Options{Current: "0.1.0", Platform: "darwin", Arch: "arm64"})

	info, err := svc.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Installable {
		t.Fatal("darwin has no published installer")
	}
	if _, err := svc.Install(context.Background(), info); err == nil {
		t.Fatal("Install accepted an unsupported platform")
	}
}

func TestInstallPinsTheWindowsInstallerToTheCheckedRelease(t *testing.T) {
	f := newFixture(t, "install.ps1", "install", "")
	var launchedPath string
	var launchedArgs []string
	var launchedEnv []string
	svc := service(t, f, update.Options{
		Current: "0.1.0", Platform: "windows", Arch: "amd64",
		Run: func(path string, args []string, env []string) error {
			launchedPath, launchedArgs, launchedEnv = path, args, env
			return nil
		},
	})
	info, err := svc.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	path, err := svc.Install(context.Background(), info)
	if err != nil {
		t.Fatal(err)
	}
	if path != launchedPath || filepath.Base(path) != "install.ps1" {
		t.Fatalf("launched %q, returned %q", launchedPath, path)
	}
	if len(launchedArgs) != 2 || launchedArgs[0] != "-Version" || launchedArgs[1] != "v0.2.0" {
		t.Fatalf("args = %v", launchedArgs)
	}
	if len(launchedEnv) != 0 {
		t.Fatalf("env = %v", launchedEnv)
	}
}

func TestInstallVerifiesChecksumBeforeLaunching(t *testing.T) {
	f := newFixture(t, "install.sh", "#!/bin/sh\nexit 0\n", "")
	var launchedPath string
	var launchedArgs []string
	var launchedEnv []string
	svc := service(t, f, update.Options{
		Current: "0.1.0", Platform: "linux", Arch: "amd64",
		Run: func(path string, args []string, env []string) error {
			launchedPath, launchedArgs, launchedEnv = path, args, env
			return nil
		},
	})
	info, err := svc.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	path, err := svc.Install(context.Background(), info)
	if err != nil {
		t.Fatal(err)
	}
	if path != launchedPath {
		t.Fatalf("launched %q, returned %q", launchedPath, path)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != f.installer {
		t.Fatalf("installer body = %q", body)
	}
	if len(launchedArgs) != 0 {
		t.Fatalf("args = %v", launchedArgs)
	}
	if len(launchedEnv) != 1 || launchedEnv[0] != "KITSUNE_VERSION=v0.2.0" {
		t.Fatalf("env = %v", launchedEnv)
	}
}

func TestInstallRefusesAChecksumMismatchAndDoesNotLaunch(t *testing.T) {
	f := newFixture(t, "install.sh", "#!/bin/sh\nexit 0\n", strings.Repeat("0", 64))
	launched := false
	svc := service(t, f, update.Options{
		Current: "0.1.0", Platform: "linux", Arch: "amd64",
		Run: func(string, []string, []string) error {
			launched = true
			return nil
		},
	})
	info, err := svc.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Install(context.Background(), info); err == nil {
		t.Fatal("Install accepted a checksum mismatch")
	}
	if launched {
		t.Fatal("the installer was launched before the checksum passed")
	}
	if _, err := os.Stat(filepath.Join(f.dir, "install.sh")); err == nil {
		t.Fatal("the rejected installer was left on disk")
	}
}

func TestInstallRefusesAnAssetTheReleaseDidNotList(t *testing.T) {
	f := newFixture(t, "install.sh", "x", "")
	svc := service(t, f, update.Options{Current: "0.1.0", Platform: "linux", Arch: "amd64"})
	info, err := svc.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	info.AssetURL = ""
	if _, err := svc.Install(context.Background(), info); err == nil {
		t.Fatal("Install accepted an Info without a URL")
	}
}

func TestCheckDoesNotOfferAnInstallerWithoutReleaseChecksums(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/repos/FoxRed-cmd/kitsune-whisper/releases/latest" {
			http.NotFound(rw, req)
			return
		}
		_ = json.NewEncoder(rw).Encode(map[string]any{
			"tag_name": "v0.2.0",
			"html_url": "https://example.invalid/releases/v0.2.0",
			"assets": []map[string]any{
				{
					"name":                 "install.sh",
					"browser_download_url": server.URL + "/download/v0.2.0/install.sh",
				},
			},
		})
	}))
	defer server.Close()
	svc := update.New(update.Options{
		Current: "0.1.0", Platform: "linux", Arch: "amd64",
		APIBase: server.URL, Client: server.Client(), DownloadDir: t.TempDir(),
	})

	info, err := svc.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Installable {
		t.Fatal("a release without checksums must not offer an installer")
	}
	if info.ChecksumsURL != "" {
		t.Fatalf("checksums URL was invented: %q", info.ChecksumsURL)
	}
}

func TestComponentAssetsNamesTheArchives(t *testing.T) {
	cases := []struct {
		platform, arch string
		client, server string
	}{
		{"linux", "amd64", "kitsune-client_linux_amd64.tar.gz", "kitsune-server_linux_amd64.tar.gz"},
		{"windows", "amd64", "kitsune-client_windows_amd64.zip", "kitsune-server_windows_amd64.tar.gz"},
	}
	for _, test := range cases {
		client, server, err := update.ComponentAssets(test.platform, test.arch)
		if err != nil {
			t.Fatalf("%s/%s: %v", test.platform, test.arch, err)
		}
		if client != test.client || server != test.server {
			t.Fatalf("%s/%s = %q, %q", test.platform, test.arch, client, server)
		}
	}
	if _, _, err := update.ComponentAssets("darwin", "arm64"); err == nil {
		t.Fatal("darwin has no published component archives")
	}
}

func TestDownloadStagesBothInstalledComponents(t *testing.T) {
	f := newFixture(t, "install.sh", "#!/bin/sh\nexit 0\n", "")
	svc := service(t, f, update.Options{
		Current: "v0.1.0", Platform: "linux", Arch: "amd64",
		Components: components.Set{Client: true, Server: true},
	})
	info := check(t, svc)

	stage, err := svc.Download(context.Background(), info, nil)
	if err != nil {
		t.Fatal(err)
	}
	clientAsset, serverAsset, _ := update.ComponentAssets("linux", "amd64")
	for _, want := range []string{
		"install.sh", update.ChecksumsAsset, update.SignatureAsset, update.VersionAsset, clientAsset, serverAsset,
	} {
		if !contains(stage.Files, want) {
			t.Errorf("stage files = %v, missing %q", stage.Files, want)
		}
	}
	if !stage.HasClient() || !stage.HasServer() {
		t.Fatalf("stage components = %+v", stage.Components)
	}
	if stage.InstallerPath() == "" {
		t.Fatal("the stage has no installer path")
	}
	if stage.Tag != info.Tag || stage.Dir == "" {
		t.Fatalf("stage = %+v", stage)
	}
	if !f.requested(serverAsset) {
		t.Fatal("the server archive was not downloaded")
	}
}

func TestDownloadClientOnlyOmitsTheServerArchive(t *testing.T) {
	f := newFixture(t, "install.sh", "#!/bin/sh\nexit 0\n", "")
	svc := service(t, f, update.Options{
		Current: "v0.1.0", Platform: "linux", Arch: "amd64",
		Components: components.Set{Client: true},
	})
	info := check(t, svc)

	stage, err := svc.Download(context.Background(), info, nil)
	if err != nil {
		t.Fatal(err)
	}
	clientAsset, serverAsset, _ := update.ComponentAssets("linux", "amd64")
	if !contains(stage.Files, clientAsset) {
		t.Fatalf("stage files = %v, missing the client archive", stage.Files)
	}
	if contains(stage.Files, serverAsset) || stage.HasServer() {
		t.Fatalf("client-only stage contains the server: %+v", stage)
	}
	if f.requested(serverAsset) {
		t.Fatal("the server archive was downloaded for a client-only install")
	}
	for _, want := range []string{"install.sh", update.ChecksumsAsset, update.SignatureAsset, update.VersionAsset} {
		if !contains(stage.Files, want) {
			t.Errorf("stage files = %v, missing %q", stage.Files, want)
		}
	}
}

func TestDownloadRejectsAChecksumMismatchAndLeavesNoStage(t *testing.T) {
	f := newFixture(t, "install.sh", "#!/bin/sh\nexit 0\n", strings.Repeat("0", 64))
	svc := service(t, f, update.Options{
		Current: "v0.1.0", Platform: "linux", Arch: "amd64",
		Components: components.Set{Client: true, Server: true},
	})
	info := check(t, svc)

	if _, err := svc.Download(context.Background(), info, nil); err == nil {
		t.Fatal("Download accepted a checksum mismatch")
	}
	if _, ok := svc.Staged(info.Tag); ok {
		t.Fatal("a rejected set was reported as staged")
	}
	entries, err := os.ReadDir(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("a rejected download left files behind: %v", entries)
	}
}

func TestDownloadCancellationRemovesTheTempStage(t *testing.T) {
	clientAsset, _, _ := update.ComponentAssets("linux", "amd64")
	f := newFixtureWith(t, fixtureOptions{
		tag: "v0.2.0", installerName: "install.sh", installerBody: "#!/bin/sh\nexit 0\n",
		blockAsset: clientAsset,
	})
	svc := service(t, f, update.Options{
		Current: "v0.1.0", Platform: "linux", Arch: "amd64",
		Components: components.Set{Client: true, Server: true},
	})
	info := check(t, svc)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := svc.Download(ctx, info, nil)
		done <- err
	}()
	select {
	case <-f.blockStarted:
	case <-timeoutAfter(t):
		t.Fatal("the download never reached the blocked asset")
	}
	cancel()
	err := <-done
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if _, ok := svc.Staged(info.Tag); ok {
		t.Fatal("a cancelled download was reported as staged")
	}
	entries, readErr := os.ReadDir(f.dir)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("a cancelled download left files behind: %v", entries)
	}
}

func TestDownloadReportsCumulativeProgress(t *testing.T) {
	f := newFixture(t, "install.sh", "#!/bin/sh\nexit 0\n", "")
	svc := service(t, f, update.Options{
		Current: "v0.1.0", Platform: "linux", Arch: "amd64",
		Components: components.Set{Client: true, Server: true},
	})
	info := check(t, svc)

	var mu sync.Mutex
	events := []update.Progress{}
	_, err := svc.Download(context.Background(), info, func(progress update.Progress) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, progress)
	})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(events) == 0 {
		t.Fatal("no progress was reported")
	}
	var previous int64
	files := map[string]bool{}
	for _, event := range events {
		if event.Received < previous {
			t.Fatalf("progress went backwards: %d then %d", previous, event.Received)
		}
		previous = event.Received
		files[event.File] = true
	}
	want := f.servedBytes()
	last := events[len(events)-1]
	if last.Received != want || last.Total != want {
		t.Fatalf("final progress = %+v, want received and total %d", last, want)
	}
	for _, name := range f.assetOrder() {
		if !files[name] {
			t.Errorf("progress never named %q", name)
		}
	}
}

func TestDownloadUnknownSizesReportAnIndeterminateTotal(t *testing.T) {
	f := newFixtureWith(t, fixtureOptions{
		tag: "v0.2.0", installerName: "install.sh", installerBody: "#!/bin/sh\nexit 0\n",
		unknownSizes: true,
	})
	svc := service(t, f, update.Options{
		Current: "v0.1.0", Platform: "linux", Arch: "amd64",
		Components: components.Set{Client: true},
	})
	info := check(t, svc)

	var last update.Progress
	_, err := svc.Download(context.Background(), info, func(progress update.Progress) {
		last = progress
	})
	if err != nil {
		t.Fatal(err)
	}
	if last.Total != 0 {
		t.Fatalf("total = %d, want an indeterminate 0", last.Total)
	}
	if last.Received == 0 {
		t.Fatal("no bytes were reported")
	}
}

func TestDownloadSynthesizesVersionWhenTheReleaseDoesNotListIt(t *testing.T) {
	f := newFixtureWith(t, fixtureOptions{
		tag: "v0.2.0", installerName: "install.sh", installerBody: "#!/bin/sh\nexit 0\n",
		omitVersion: true,
	})
	svc := service(t, f, update.Options{
		Current: "v0.1.0", Platform: "linux", Arch: "amd64",
		Components: components.Set{Client: true},
	})
	info := check(t, svc)

	stage, err := svc.Download(context.Background(), info, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(stage.Files, update.VersionAsset) {
		t.Fatalf("stage files = %v, missing VERSION", stage.Files)
	}
	body, err := os.ReadFile(filepath.Join(stage.Dir, update.VersionAsset))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(body)) != "0.2.0" {
		t.Fatalf("VERSION = %q", body)
	}
	if err := svc.VerifyStaged(context.Background(), stage); err != nil {
		t.Fatalf("a synthesized VERSION failed verification: %v", err)
	}
}

func TestStagedReportsOnlyCompleteSets(t *testing.T) {
	f := newFixture(t, "install.sh", "#!/bin/sh\nexit 0\n", "")
	svc := service(t, f, update.Options{
		Current: "v0.1.0", Platform: "linux", Arch: "amd64",
		Components: components.Set{Client: true, Server: true},
	})
	info := check(t, svc)
	stage, err := svc.Download(context.Background(), info, nil)
	if err != nil {
		t.Fatal(err)
	}

	if staged, ok := svc.Staged(info.Tag); !ok || staged.Dir != stage.Dir {
		t.Fatalf("Staged = %+v, %t", staged, ok)
	}
	if _, ok := svc.Staged("v9.9.9"); ok {
		t.Fatal("Staged accepted a different tag")
	}

	clientAsset, _, _ := update.ComponentAssets("linux", "amd64")
	if err := os.Remove(filepath.Join(stage.Dir, clientAsset)); err != nil {
		t.Fatal(err)
	}
	if _, ok := svc.Staged(info.Tag); ok {
		t.Fatal("Staged accepted a set with a missing file")
	}
}

func TestVerifyStagedAcceptsAndRejectsFiles(t *testing.T) {
	f := newFixture(t, "install.sh", "#!/bin/sh\nexit 0\n", "")
	svc := service(t, f, update.Options{
		Current: "v0.1.0", Platform: "linux", Arch: "amd64",
		Components: components.Set{Client: true, Server: true},
	})
	info := check(t, svc)
	stage, err := svc.Download(context.Background(), info, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.VerifyStaged(context.Background(), stage); err != nil {
		t.Fatalf("VerifyStaged rejected a good set: %v", err)
	}

	clientAsset, _, _ := update.ComponentAssets("linux", "amd64")
	path := filepath.Join(stage.Dir, clientAsset)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(body, 'x'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := svc.VerifyStaged(context.Background(), stage); err == nil {
		t.Fatal("VerifyStaged accepted a tampered file")
	}
}

func TestAFailedRedownloadKeepsThePreviousCompleteStage(t *testing.T) {
	f := newFixture(t, "install.sh", "#!/bin/sh\nexit 0\n", "")
	svc := service(t, f, update.Options{
		Current: "v0.1.0", Platform: "linux", Arch: "amd64",
		Components: components.Set{Client: true},
	})
	info := check(t, svc)
	stage, err := svc.Download(context.Background(), info, nil)
	if err != nil {
		t.Fatal(err)
	}

	broken := newFixture(t, "install.sh", "#!/bin/sh\nexit 0\n", strings.Repeat("0", 64))
	brokenSvc := update.New(update.Options{
		Current: "v0.1.0", Platform: "linux", Arch: "amd64",
		Components: components.Set{Client: true},
		APIBase:    broken.server.URL, Client: broken.server.Client(), DownloadDir: f.dir,
	})
	brokenInfo := check(t, brokenSvc)
	if _, err := brokenSvc.Download(context.Background(), brokenInfo, nil); err == nil {
		t.Fatal("the broken re-download succeeded")
	}
	staged, ok := brokenSvc.Staged(info.Tag)
	if !ok || staged.Dir != stage.Dir {
		t.Fatalf("the previous stage did not survive: %+v, %t", staged, ok)
	}
	if err := brokenSvc.VerifyStaged(context.Background(), staged); err != nil {
		t.Fatalf("the previous stage was damaged: %v", err)
	}
}

func TestDownloadingANewerTagPrunesTheOldStage(t *testing.T) {
	f := newFixture(t, "install.sh", "#!/bin/sh\nexit 0\n", "")
	svc := service(t, f, update.Options{
		Current: "v0.1.0", Platform: "linux", Arch: "amd64",
		Components: components.Set{Client: true},
	})
	info := check(t, svc)
	oldStage, err := svc.Download(context.Background(), info, nil)
	if err != nil {
		t.Fatal(err)
	}

	newer := newFixtureWith(t, fixtureOptions{
		tag: "v0.3.0", installerName: "install.sh", installerBody: "#!/bin/sh\nexit 0\n",
	})
	newerSvc := update.New(update.Options{
		Current: "v0.1.0", Platform: "linux", Arch: "amd64",
		Components: components.Set{Client: true},
		APIBase:    newer.server.URL, Client: newer.server.Client(), DownloadDir: f.dir,
	})
	newerInfo := check(t, newerSvc)
	newStage, err := newerSvc.Download(context.Background(), newerInfo, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldStage.Dir); !os.IsNotExist(err) {
		t.Fatalf("the old stage survived: %v", err)
	}
	if _, ok := newerSvc.Staged(newStage.Tag); !ok {
		t.Fatal("the new stage was not kept")
	}
}

func TestClearStagedRemovesTheDirectory(t *testing.T) {
	f := newFixture(t, "install.sh", "#!/bin/sh\nexit 0\n", "")
	svc := service(t, f, update.Options{
		Current: "v0.1.0", Platform: "linux", Arch: "amd64",
		Components: components.Set{Client: true},
	})
	info := check(t, svc)
	stage, err := svc.Download(context.Background(), info, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ClearStaged(info.Tag); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stage.Dir); !os.IsNotExist(err) {
		t.Fatalf("ClearStaged left the directory: %v", err)
	}
	if _, ok := svc.Staged(info.Tag); ok {
		t.Fatal("Staged found a cleared set")
	}
}
