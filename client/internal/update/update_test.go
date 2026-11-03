package update_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/update"
)

type fixture struct {
	server    *httptest.Server
	installer string
	checksum  string
	dir       string
	requests  []string
}

func newFixture(t *testing.T, installerName, installerBody string, checksumOverride string) *fixture {
	tag := "v0.2.0"
	t.Helper()
	f := &fixture{installer: installerBody, dir: t.TempDir()}
	digest := sha256.Sum256([]byte(installerBody))
	f.checksum = hex.EncodeToString(digest[:])
	if checksumOverride != "" {
		f.checksum = checksumOverride
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/FoxRed-cmd/kitsune-whisper/releases/latest", func(rw http.ResponseWriter, req *http.Request) {
		f.requests = append(f.requests, req.URL.Path)
		release := map[string]any{
			"tag_name": tag,
			"html_url": "https://example.invalid/releases/" + tag,
			"body":     "Notes",
			"assets": []map[string]any{
				{
					"name":                 installerName,
					"browser_download_url": f.server.URL + "/download/" + tag + "/" + installerName,
				},
				{
					"name":                 update.ChecksumsAsset,
					"browser_download_url": f.server.URL + "/download/" + tag + "/" + update.ChecksumsAsset,
				},
			},
		}
		_ = json.NewEncoder(rw).Encode(release)
	})
	mux.HandleFunc("/download/", func(rw http.ResponseWriter, req *http.Request) {
		f.requests = append(f.requests, req.URL.Path)
		name := filepath.Base(req.URL.Path)
		if name == update.ChecksumsAsset {
			fmt.Fprintf(rw, "%s  %s\n", f.checksum, installerName)
			return
		}
		if name != installerName {
			http.NotFound(rw, req)
			return
		}
		_, _ = rw.Write([]byte(installerBody))
	})
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func service(t *testing.T, f *fixture, opts update.Options) *update.Service {
	t.Helper()
	opts.APIBase = f.server.URL
	opts.Client = f.server.Client()
	opts.DownloadDir = f.dir
	return update.New(opts)
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
}

func TestCheckSelectsTheWindowsSetup(t *testing.T) {
	f := newFixture(t, "kitsune-whisper_windows_amd64_setup.exe", "setup", "")
	svc := service(t, f, update.Options{Current: "0.1.0", Platform: "windows", Arch: "amd64"})

	info, err := svc.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Asset != "kitsune-whisper_windows_amd64_setup.exe" || !info.Installable {
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
