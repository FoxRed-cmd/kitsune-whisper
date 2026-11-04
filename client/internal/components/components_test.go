package components

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func linuxEnv(root string) Environment {
	return Environment{
		GOOS:       "linux",
		ConfigHome: filepath.Join(root, "config"),
		ServerRoot: filepath.Join(root, "lib", "kitsune-whisper", "server"),
		Executable: filepath.Join(root, "bin", "kitsune-client"),
	}
}

func TestDetectReportsNothingInAnEmptyDirectory(t *testing.T) {
	if got := Detect(linuxEnv(t.TempDir())); got.Any() {
		t.Fatalf("Detect = %+v, want no Components", got)
	}
}

func TestDetectDerivesTheClientFromItsBinary(t *testing.T) {
	env := linuxEnv(t.TempDir())
	writeFile(t, env.Executable)
	if got := Detect(env); !got.Client || got.Server {
		t.Fatalf("Detect = %+v, want the Client only", got)
	}
}

func TestDetectRequiresTheServerBundleAndItsUnit(t *testing.T) {
	env := linuxEnv(t.TempDir())
	writeFile(t, env.Executable)
	writeFile(t, filepath.Join(env.ServerRoot, BundleManifest))
	if got := Detect(env); got.Server {
		t.Fatal("a bundle without a supervisor registration must not count as installed")
	}

	writeFile(t, filepath.Join(env.ConfigHome, "systemd", "user", ServerUnit))
	if got := Detect(env); !got.Client || !got.Server {
		t.Fatalf("Detect = %+v, want both Components", got)
	}

	if err := os.Remove(filepath.Join(env.ServerRoot, BundleManifest)); err != nil {
		t.Fatalf("remove manifest: %v", err)
	}
	if got := Detect(env); got.Server {
		t.Fatal("a registration without a bundle must not count as installed")
	}
}

func TestDetectWindowsRequiresAQueryableTask(t *testing.T) {
	root := t.TempDir()
	env := Environment{
		GOOS:       "windows",
		ConfigHome: root,
		ServerRoot: filepath.Join(root, "Server"),
		Executable: filepath.Join(root, "kitsune-client.exe"),
		Run: func(name string, _ ...string) (string, error) {
			if name == "schtasks" {
				return "", nil
			}
			return "", errors.New("unexpected command")
		},
	}
	writeFile(t, env.Executable)
	writeFile(t, filepath.Join(env.ServerRoot, BundleManifest))
	task := filepath.Join(root, configDirName, ServerTask+"-task.xml")
	writeFile(t, task)
	if got := Detect(env); !got.Server {
		t.Fatalf("Detect = %+v, want the Server", got)
	}

	env.Run = func(string, ...string) (string, error) { return "", errors.New("not registered") }
	if got := Detect(env); got.Server {
		t.Fatal("a cached definition without a registered task must not count")
	}

	env.Run = nil
	if got := Detect(env); got.Server {
		t.Fatal("without a command runner the Windows registration cannot be verified")
	}
}

func TestDetectIgnoresTheServerOnAnUnsupportedPlatform(t *testing.T) {
	env := linuxEnv(t.TempDir())
	env.GOOS = "plan9"
	writeFile(t, env.Executable)
	writeFile(t, filepath.Join(env.ServerRoot, BundleManifest))
	writeFile(t, filepath.Join(env.ConfigHome, "systemd", "user", ServerUnit))
	if got := Detect(env); got.Server {
		t.Fatalf("Detect = %+v, want no Server on an unsupported platform", got)
	}
}

func TestModesOffersOnlyInstalledCombinationsFullestFirst(t *testing.T) {
	cases := []struct {
		name string
		set  Set
		want []Mode
	}{
		{"both", Set{Client: true, Server: true}, []Mode{ModeClientAndServer, ModeClientOnly, ModeServerOnly}},
		{"client only", Set{Client: true}, []Mode{ModeClientOnly}},
		{"server only", Set{Server: true}, []Mode{ModeServerOnly}},
		{"none", Set{}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.set.Modes()
			if len(got) != len(tc.want) {
				t.Fatalf("Modes = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("Modes = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestDefaultModeIsTheFullestInstalledCombination(t *testing.T) {
	cases := map[Set]Mode{
		{Client: true, Server: true}: ModeClientAndServer,
		{Client: true}:               ModeClientOnly,
		{Server: true}:               ModeServerOnly,
		{}:                           ModeOff,
	}
	for set, want := range cases {
		if got := set.DefaultMode(); got != want {
			t.Errorf("%+v.DefaultMode() = %q, want %q", set, got, want)
		}
	}
}

func TestOffIsNotAnOfferedMode(t *testing.T) {
	both := Set{Client: true, Server: true}
	if both.Allows(ModeOff) {
		t.Fatal("off is the switch state, not a combination of Components")
	}
	if ModeOff.Valid() {
		t.Fatal("off must not be accepted as an autostart mode")
	}
	if !ModeClientAndServer.Valid() || !ModeClientOnly.Valid() || !ModeServerOnly.Valid() {
		t.Fatal("every component mode must be valid")
	}
}

func TestModeSetNamesTheComponentsItStarts(t *testing.T) {
	if got := ModeClientAndServer.Set(); got != (Set{Client: true, Server: true}) {
		t.Fatalf("client-server = %+v", got)
	}
	if got := ModeClientOnly.Set(); got != (Set{Client: true}) {
		t.Fatalf("client = %+v", got)
	}
	if got := ModeServerOnly.Set(); got != (Set{Server: true}) {
		t.Fatalf("server = %+v", got)
	}
	if got := ModeOff.Set(); got.Any() {
		t.Fatalf("off = %+v, want no Components", got)
	}
}
