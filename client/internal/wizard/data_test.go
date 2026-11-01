package wizard_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/servers"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/trust"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/wizard"
)

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}

func contains(steps []wizard.Step, want wizard.Step) bool {
	for _, step := range steps {
		if step == want {
			return true
		}
	}
	return false
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func serversViewWith(models *servers.ModelsView) servers.View {
	return servers.View{
		ActiveID: "local",
		Profiles: []servers.Profile{{
			ID:       "local",
			Name:     "Local Server",
			URL:      "https://localhost:8765",
			AutoPair: true,
			Active:   true,
		}},
		Models: models,
	}
}

func modelsWith(device, active, state string, rows ...servers.Model) *servers.ModelsView {
	return &servers.ModelsView{
		Device:      device,
		Recommended: "",
		State:       state,
		ActiveModel: active,
		Models:      rows,
	}
}

var structModelView = servers.ModelsView{
	Device:      "cpu",
	Recommended: "base",
	State:       "loaded",
	ActiveModel: "small",
	Models: []servers.Model{
		{Name: "base", Installed: true, Recommended: true},
		{Name: "small", Installed: true},
	},
}

func noModelServers() wizard.Data {
	models := modelsWith("cpu", "", "cold")
	return wizard.Data{
		Servers: serversViewWith(models),
		Server:  trust.Status{State: trust.StateOnline},
	}
}

func TestModelPresence(t *testing.T) {
	cases := []struct {
		name   string
		data   wizard.Data
		known  bool
		active bool
	}{
		{
			name:   "active installed model",
			data:   wizard.Data{Servers: serversViewWith(modelsWith("cpu", "base", "loaded", servers.Model{Name: "base", Installed: true}))},
			known:  true,
			active: true,
		},
		{
			name:  "no active model",
			data:  wizard.Data{Servers: serversViewWith(modelsWith("cpu", "", "cold"))},
			known: true,
		},
		{
			name:  "active row missing",
			data:  wizard.Data{Servers: serversViewWith(modelsWith("cpu", "medium", "loaded"))},
			known: true,
		},
		{
			name:  "active row not installed",
			data:  wizard.Data{Servers: serversViewWith(modelsWith("cpu", "base", "loaded", servers.Model{Name: "base"}))},
			known: true,
		},
		{
			name:  "failed model",
			data:  wizard.Data{Servers: serversViewWith(modelsWith("cpu", "base", "failed", servers.Model{Name: "base", Installed: true}))},
			known: true,
		},
		{
			name:   "cold model is active",
			data:   wizard.Data{Servers: serversViewWith(modelsWith("cpu", "base", "cold", servers.Model{Name: "base", Installed: true}))},
			known:  true,
			active: true,
		},
		{
			name:   "stopped model is active",
			data:   wizard.Data{Servers: serversViewWith(modelsWith("cpu", "base", "stopped", servers.Model{Name: "base", Installed: true}))},
			known:  true,
			active: true,
		},
		{
			name:   "local path is active",
			data:   wizard.Data{Servers: serversViewWith(&servers.ModelsView{ActivePath: "/models/local", State: "loaded"})},
			known:  true,
			active: true,
		},
		{
			name: "lifecycle fallback",
			data: wizard.Data{Servers: servers.View{
				ActiveID: "local",
				Lifecycle: &servers.ServerLifecycle{
					Model: "base", ModelState: "loaded",
				},
			}},
			known:  true,
			active: true,
		},
		{
			name:   "status fallback",
			data:   wizard.Data{Server: trust.Status{State: trust.StateOnline, Model: "base", ModelState: "cold"}},
			known:  true,
			active: true,
		},
		{
			name: "offline and unknown",
			data: wizard.Data{Server: trust.Status{State: trust.StateOffline}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := wizard.ModelPresence(tc.data)
			if got.ModelKnown != tc.known || got.ModelActive != tc.active {
				t.Fatalf("presence = %+v, want known=%v active=%v", got, tc.known, tc.active)
			}
		})
	}
}

func TestHardwarePlan(t *testing.T) {
	cases := []struct {
		name      string
		data      wizard.Data
		known     bool
		available bool
		recommend string
		pinned    bool
		count     int
	}{
		{
			name: "cuda available and resolved",
			data: wizard.Data{Server: trust.Status{GPU: &trust.GPUStatus{
				Available: true, DeviceCount: 2, Device: "cuda",
			}}},
			known: true, available: true, recommend: "auto", count: 2,
		},
		{
			name: "cuda available but pinned to cpu",
			data: wizard.Data{Server: trust.Status{GPU: &trust.GPUStatus{
				Available: true, DeviceCount: 1, Device: "cpu",
			}}},
			known: true, available: true, recommend: "auto", pinned: true, count: 1,
		},
		{
			name: "no cuda",
			data: wizard.Data{Server: trust.Status{GPU: &trust.GPUStatus{
				Available: false, DeviceCount: 0, Device: "cpu",
			}}},
			known: true, recommend: "cpu",
		},
		{
			name: "fallback to the catalog device",
			data: wizard.Data{
				Server:  trust.Status{},
				Servers: serversViewWith(modelsWith("cuda", "", "cold")),
			},
			known: true, available: true, recommend: "auto",
		},
		{
			name:      "unknown server",
			data:      wizard.Data{},
			recommend: "auto",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := wizard.PlanHardware(tc.data)
			if got.Known != tc.known || got.Available != tc.available ||
				got.Recommend != tc.recommend || got.Pinned != tc.pinned ||
				got.DeviceCount != tc.count {
				t.Fatalf("plan = %+v", got)
			}
		})
	}
}

func TestRecommendModel(t *testing.T) {
	cases := []struct {
		name   string
		models *servers.ModelsView
		want   string
	}{
		{
			name:   "server recommendation wins",
			models: &servers.ModelsView{Recommended: "medium", Device: "cuda"},
			want:   "medium",
		},
		{
			name: "recommended row",
			models: &servers.ModelsView{
				Device: "cpu",
				Models: []servers.Model{{Name: "base", Recommended: true}},
			},
			want: "base",
		},
		{
			name:   "no catalog",
			models: &servers.ModelsView{},
			want:   "",
		},
		{
			name: "cuda prefers medium but falls back to small",
			models: &servers.ModelsView{
				Device: "cuda",
				Models: []servers.Model{{Name: "base"}, {Name: "small"}},
			},
			want: "small",
		},
		{
			name: "cpu prefers base",
			models: &servers.ModelsView{
				Device: "cpu",
				Models: []servers.Model{{Name: "small"}, {Name: "base"}},
			},
			want: "base",
		},
		{
			name: "last resort is the first row",
			models: &servers.ModelsView{
				Device: "cuda",
				Models: []servers.Model{{Name: "tiny"}, {Name: "large-v3"}},
			},
			want: "tiny",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := wizard.RecommendModel(tc.models); got != tc.want {
				t.Fatalf("RecommendModel = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTryReadinessExplainsEachMissingPrerequisite(t *testing.T) {
	ready := readyData()
	if reasons := wizard.TryReadiness(ready); len(reasons) != 0 {
		t.Fatalf("ready data has reasons: %+v", reasons)
	}
	cases := []struct {
		name string
		data func() wizard.Data
		key  string
		step wizard.Step
	}{
		{
			name: "no profile",
			data: func() wizard.Data {
				data := ready
				data.Servers.ActiveID = ""
				return data
			},
			key:  "wizard.try.missing.profile",
			step: wizard.StepServer,
		},
		{
			name: "server offline",
			data: func() wizard.Data {
				data := ready
				data.Server.State = trust.StateOffline
				return data
			},
			key:  "wizard.try.missing.server",
			step: wizard.StepServer,
		},
		{
			name: "no active model",
			data: func() wizard.Data {
				data := ready
				data.Servers.Models = modelsWith("cpu", "", "cold")
				return data
			},
			key:  "wizard.try.missing.model",
			step: wizard.StepModel,
		},
		{
			name: "failed model",
			data: func() wizard.Data {
				data := ready
				models := modelsWith("cpu", "base", "failed", servers.Model{Name: "base", Installed: true})
				models.Error = "load failed"
				data.Servers.Models = models
				return data
			},
			key:  "wizard.try.missing.modelFailed",
			step: wizard.StepModel,
		},
		{
			name: "soft-stopped model",
			data: func() wizard.Data {
				data := ready
				data.Servers.Models = modelsWith("cpu", "base", "stopped", servers.Model{Name: "base", Installed: true})
				return data
			},
			key:  "wizard.try.missing.stopped",
			step: wizard.StepModel,
		},
		{
			name: "runtime stopped",
			data: func() wizard.Data {
				data := ready
				data.Running = false
				return data
			},
			key: "wizard.try.missing.runtime",
		},
		{
			name: "microphone unavailable",
			data: func() wizard.Data {
				data := ready
				data.DeviceError = "no audio backend"
				return data
			},
			key:  "wizard.try.missing.mic",
			step: wizard.StepMicrophone,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reasons := wizard.TryReadiness(tc.data())
			if len(reasons) == 0 {
				t.Fatal("no reason was reported")
			}
			if reasons[0].Key != tc.key {
				t.Fatalf("first reason = %q, want %q (%+v)", reasons[0].Key, tc.key, reasons)
			}
			if reasons[0].Step != tc.step {
				t.Fatalf("reason step = %q, want %q", reasons[0].Step, tc.step)
			}
		})
	}
}

func readyData() wizard.Data {
	return wizard.Data{
		Servers: serversViewWith(
			modelsWith("cpu", "base", "loaded", servers.Model{Name: "base", Installed: true})),
		Server:  trust.Status{State: trust.StateOnline, Model: "base", ModelState: "loaded"},
		Running: true,
	}
}

func TestActiveModelReadsTheServerSources(t *testing.T) {
	name, state := wizard.ActiveModel(readyData())
	if name != "base" || state != "loaded" {
		t.Fatalf("active model = %q %q", name, state)
	}
	name, state = wizard.ActiveModel(wizard.Data{
		Server: trust.Status{State: trust.StateOnline, Model: "small", ModelState: "cold"},
	})
	if name != "small" || state != "cold" {
		t.Fatalf("status fallback = %q %q", name, state)
	}
}

func TestWizardStatePathHelper(t *testing.T) {
	// The package itself does no path resolution; FileStore must never write
	// outside the directory it is given.
	path := filepath.Join(t.TempDir(), "nested", "wizard.json")
	store := wizard.NewFileStore(path)
	if err := store.Save(wizard.State{Version: 1, Step: wizard.StepWelcome}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if !fileExists(path) {
		t.Fatal("the state file was not created")
	}
}
