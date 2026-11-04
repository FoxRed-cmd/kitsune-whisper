package components

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/process"
)

const (
	// ServerUnit is the systemd user unit the Server bundle registers.
	ServerUnit = "kitsune-server.service"
	// ServerTask is the per-user Task Scheduler task the Server bundle
	// registers.
	ServerTask = "kitsune-server"
	// BundleManifest is the file at the Server bundle root that records what
	// was installed there.
	BundleManifest = "bundle.json"
	// configDirName is the per-user directory under the OS config home that
	// holds the cached Windows task definition, next to kitsune.yaml.
	configDirName = "kitsune-whisper"
)

// Runner executes one supervisor command and returns its combined output.
type Runner func(name string, args ...string) (string, error)

// Environment is the side-effect surface Detect reads. Tests pass a temporary
// directory and a fake runner, so detection never touches a real session.
type Environment struct {
	// GOOS selects the platform ("linux", "windows", ...).
	GOOS string
	// ConfigHome is the XDG config root (~/.config or %APPDATA%), where the
	// systemd user unit and the cached Windows task definition live.
	ConfigHome string
	// ServerRoot is the per-user bundle root that holds bundle.json.
	ServerRoot string
	// Executable is the path to the installed Client binary.
	Executable string
	// Run executes systemctl or schtasks. It may be nil; a registration that
	// needs a command then counts as absent.
	Run Runner
}

// Detect reports which Components are installed.
//
// The Client is present when its binary exists. The Local Server is present
// only when both its bundle manifest and its supervisor registration exist: a
// bundle without a registration cannot be started by the Client, and a
// registration without a bundle has nothing to run.
func Detect(env Environment) Set {
	set := Set{Client: isFile(env.Executable)}
	switch env.GOOS {
	case "linux":
		set.Server = isFile(serverManifest(env)) && isFile(serverUnitPath(env.ConfigHome))
	case "windows":
		set.Server = isFile(serverManifest(env)) && windowsTaskRegistered(env)
	}
	return set
}

// Default resolves the production Environment for this process. It only reads
// environment variables and the executable path; nothing is created.
func Default() Environment {
	goos := runtime.GOOS
	home, _ := os.UserHomeDir()
	serverRoot := os.Getenv("KITSUNE_SERVER_ROOT")
	if serverRoot == "" {
		if goos == "windows" {
			local := os.Getenv("LOCALAPPDATA")
			if local == "" {
				local = filepath.Join(home, "AppData", "Local")
			}
			serverRoot = filepath.Join(local, configDirName, "Server")
		} else {
			serverRoot = filepath.Join(home, ".local", "lib", configDirName, "server")
		}
	}
	executable, _ := os.Executable()
	return Environment{
		GOOS:       goos,
		ConfigHome: ConfigHome(goos),
		ServerRoot: serverRoot,
		Executable: executable,
		Run:        runCommand,
	}
}

// ConfigHome resolves the per-user configuration root for goos.
func ConfigHome(goos string) string {
	if goos == "windows" {
		if base := os.Getenv("APPDATA"); base != "" {
			return base
		}
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, "AppData", "Roaming")
		}
		return "."
	}
	if base := os.Getenv("XDG_CONFIG_HOME"); base != "" {
		return base
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".config")
	}
	return "."
}

func serverManifest(env Environment) string {
	return filepath.Join(env.ServerRoot, BundleManifest)
}

func serverUnitPath(configHome string) string {
	return filepath.Join(configHome, "systemd", "user", ServerUnit)
}

func serverTaskPath(configHome string) string {
	return filepath.Join(configHome, configDirName, ServerTask+"-task.xml")
}

// windowsTaskRegistered mirrors the Server supervisor's status check: the
// cached definition is not registration, so the task must also answer a query.
func windowsTaskRegistered(env Environment) bool {
	if env.Run == nil || !isFile(serverTaskPath(env.ConfigHome)) {
		return false
	}
	_, err := env.Run("schtasks", "/Query", "/TN", ServerTask, "/FO", "LIST")
	return err == nil
}

// isFile reports whether path names an existing regular file.
func isFile(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func runCommand(name string, args ...string) (string, error) {
	out, err := process.Command(name, args...).CombinedOutput()
	return string(out), err
}
