package wizard_test

import (
	"strings"
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/components"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/i18n"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/servers"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/trust"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/wizard"
)

func TestViewShowsStepStatuses(t *testing.T) {
	service := newService(t, tempState(t))
	_ = service.Open()
	_ = service.Next() // at microphone, welcome done
	view := service.View(wizard.Data{Catalog: i18n.For(i18n.English)})
	if view.Step != wizard.StepMicrophone || view.StepNumber != 2 || view.StepCount != 7 {
		t.Fatalf("step = %q %d/%d", view.Step, view.StepNumber, view.StepCount)
	}
	if len(view.Steps) != 7 {
		t.Fatalf("steps = %d", len(view.Steps))
	}
	statuses := map[wizard.Step]wizard.StepStatus{}
	for _, step := range view.Steps {
		statuses[step.ID] = step.Status
		if step.Title == "" {
			t.Fatalf("step %q has no title", step.ID)
		}
	}
	if statuses[wizard.StepWelcome] != wizard.StatusDone {
		t.Fatalf("welcome status = %q", statuses[wizard.StepWelcome])
	}
	if statuses[wizard.StepMicrophone] != wizard.StatusCurrent {
		t.Fatalf("microphone status = %q", statuses[wizard.StepMicrophone])
	}
	if statuses[wizard.StepHotkey] != wizard.StatusPending {
		t.Fatalf("hotkey status = %q", statuses[wizard.StepHotkey])
	}
	_ = service.SkipStep()
	view = service.View(wizard.Data{Catalog: i18n.For(i18n.English)})
	for _, step := range view.Steps {
		if step.ID == wizard.StepMicrophone && step.Status != wizard.StatusSkipped {
			t.Fatalf("skipped microphone status = %q", step.Status)
		}
	}
}

func TestViewShowsASoftStoppedServerAsStopped(t *testing.T) {
	service := newService(t, tempState(t))
	_ = service.Open()
	stopped := trust.Status{State: trust.StateOnline, Stopped: true, ModelState: "stopped"}
	view := service.View(wizard.Data{
		Catalog: i18n.For(i18n.English),
		Server:  stopped,
		Servers: servers.View{Profiles: []servers.Profile{{
			ID: "local", Name: "Local Server", Active: true, Health: stopped,
		}}},
	})
	if view.ServerState != "Stopped" {
		t.Fatalf("wizard server state = %q, want Stopped", view.ServerState)
	}
	if len(view.Server.Profiles) != 1 || view.Server.Profiles[0].Health != "Stopped" {
		t.Fatalf("wizard profile health = %+v", view.Server.Profiles)
	}
}

func TestViewTranslatesEveryWizardString(t *testing.T) {
	for _, language := range []i18n.Lang{i18n.English, i18n.Russian} {
		catalog := i18n.For(language)
		for _, key := range wizard.LabelKeys() {
			if !catalog.Has(key) {
				t.Errorf("%s catalog is missing %q", language, key)
			}
		}
		service := newService(t, tempState(t))
		_ = service.Open()
		view := service.View(wizard.Data{
			Catalog:       catalog,
			Version:       "1.2.3",
			Server:        trust.Status{State: trust.StateOnline},
			Servers:       serversViewWith(modelsWith("cpu", "", "cold")),
			Running:       true,
			DeviceOptions: []wizard.Option{{Value: "", Label: "default"}},
		})
		if view.Labels.Title == "" || view.Labels.Next == "" || view.Labels.Back == "" ||
			view.Labels.Skip == "" || view.Labels.SkipAll == "" || view.Labels.Close == "" {
			t.Fatalf("%s labels are incomplete: %+v", language, view.Labels)
		}
		for _, step := range view.Steps {
			if strings.TrimSpace(step.Title) == "" || step.Title == "wizard.step."+string(step.ID) {
				t.Errorf("%s step %q is not translated: %q", language, step.ID, step.Title)
			}
		}
	}
}

func TestRussianWizardViewUsesRussian(t *testing.T) {
	service := newService(t, tempState(t))
	_ = service.Open()
	view := service.View(wizard.Data{Catalog: i18n.For(i18n.Russian)})
	if view.Labels.Title == "" || !strings.Contains(strings.ToLower(view.Labels.Title), "настрой") {
		t.Fatalf("Russian title = %q", view.Labels.Title)
	}
	if !strings.Contains(view.Progress, "1") || !strings.Contains(view.Progress, "7") {
		t.Fatalf("Russian progress = %q", view.Progress)
	}
}

func TestDeferModelPersistsAndIsShown(t *testing.T) {
	path := tempState(t)
	service := newService(t, path)
	_ = service.Open()
	_ = service.Next()
	_ = service.Next()
	_ = service.Next()
	_ = service.Next()
	if service.Step() != wizard.StepModel {
		t.Fatalf("step = %q, want model", service.Step())
	}
	if err := service.DeferModel("medium"); err != nil {
		t.Fatalf("defer: %v", err)
	}
	if service.Step() != wizard.StepServer {
		t.Fatalf("step = %q, want server", service.Step())
	}
	view := service.View(wizard.Data{Catalog: i18n.For(i18n.English)})
	if view.DeferredModel != "medium" {
		t.Fatalf("deferred model = %q", view.DeferredModel)
	}
	reloaded := newService(t, path)
	if reloaded.State().DeferredModel != "medium" {
		t.Fatalf("reloaded deferred model = %q", reloaded.State().DeferredModel)
	}
}

func TestViewModelRowsTranslateSizeSpeedAndProgress(t *testing.T) {
	models := &servers.ModelsView{
		Device:      "cpu",
		Recommended: "base",
		State:       "cold",
		Models: []servers.Model{
			{
				Name: "base", DiskBytes: 145_000_000, MemoryBytes: 600_000_000,
				Speed: "fast", Quality: "good", Installed: true,
				Download: &servers.ModelDownload{
					Model: "base", State: "downloading", ReceivedBytes: 5_000_000, TotalBytes: 10_000_000,
				},
			},
		},
	}
	service := newService(t, tempState(t))
	_ = service.Open()
	view := service.View(wizard.Data{
		Catalog: i18n.For(i18n.English),
		Servers: serversViewWith(models),
	})
	if !view.Model.Available || view.Model.Recommended != "base" {
		t.Fatalf("model view = %+v", view.Model)
	}
	if len(view.Model.Rows) != 1 {
		t.Fatalf("rows = %d", len(view.Model.Rows))
	}
	row := view.Model.Rows[0]
	if row.Size != "145.0 MB" || row.Speed != "fast" || row.Quality != "good" {
		t.Fatalf("row = %+v", row)
	}
	if row.Download == nil || !row.Download.InProgress || row.Download.Percent != 50 {
		t.Fatalf("download = %+v", row.Download)
	}
	if !strings.Contains(row.Download.Label, "5.0 MB") || !strings.Contains(row.Download.Label, "10.0 MB") {
		t.Fatalf("download label = %q", row.Download.Label)
	}
}

func TestViewTryReasonsAreTranslatedAndActionable(t *testing.T) {
	service := newService(t, tempState(t))
	_ = service.Open()
	view := service.View(wizard.Data{
		Catalog: i18n.For(i18n.English),
		Server:  trust.Status{State: trust.StateOffline},
		Servers: serversViewWith(modelsWith("cpu", "", "cold")),
		Running: true,
	})
	if view.Try.Ready {
		t.Fatal("offline Server reported ready")
	}
	if len(view.Try.Reasons) == 0 {
		t.Fatal("no reasons")
	}
	if view.Try.Reasons[0].Text == "" || view.Try.Reasons[0].Step != wizard.StepServer {
		t.Fatalf("reason = %+v", view.Try.Reasons[0])
	}
}

func TestViewHardwareExplainsPinnedCUDA(t *testing.T) {
	service := newService(t, tempState(t))
	_ = service.Open()
	view := service.View(wizard.Data{
		Catalog: i18n.For(i18n.English),
		Server: trust.Status{GPU: &trust.GPUStatus{
			Available: true, DeviceCount: 1, Device: "cpu",
		}},
	})
	if !view.Hardware.Known || !view.Hardware.Available || !strings.Contains(view.Hardware.PinnedText, "cpu") {
		t.Fatalf("hardware = %+v", view.Hardware)
	}
	if view.Hardware.Recommend != "auto" || view.Hardware.RecommendText == "" {
		t.Fatalf("recommendation = %+v", view.Hardware)
	}
	if len(view.Hardware.Options) != 3 {
		t.Fatalf("options = %+v", view.Hardware.Options)
	}
}

func TestClientOnlyServerViewOffersRemoteOnly(t *testing.T) {
	service := newServiceWith(t, tempState(t), components.Set{Client: true})
	_ = service.Open()
	for i := 0; i < 3; i++ {
		if err := service.Next(); err != nil {
			t.Fatalf("next: %v", err)
		}
	}
	if service.Step() != wizard.StepServer {
		t.Fatalf("step = %q, want server", service.Step())
	}
	view := service.View(wizard.Data{Catalog: i18n.For(i18n.English)})
	if view.Server.LocalAvailable {
		t.Fatal("a Client-only installation offered the Local Server")
	}
}

func TestClientOnlyProgressCountsOnlyAvailableSteps(t *testing.T) {
	service := newServiceWith(t, tempState(t), components.Set{Client: true})
	_ = service.Open()
	for i := 0; i < 3; i++ {
		if err := service.Next(); err != nil {
			t.Fatalf("next: %v", err)
		}
	}
	if service.Step() != wizard.StepServer {
		t.Fatalf("step = %q, want server", service.Step())
	}
	view := service.View(wizard.Data{Catalog: i18n.For(i18n.English)})
	// Welcome, Microphone, Hotkey, Server, Try: Hardware and Model are skipped.
	if view.StepCount != 5 || view.StepNumber != 4 {
		t.Fatalf("progress = %d of %d, want 4 of 5", view.StepNumber, view.StepCount)
	}
	if !strings.Contains(view.Progress, "4") || !strings.Contains(view.Progress, "5") {
		t.Fatalf("progress text = %q", view.Progress)
	}
}

func TestBothComponentsServerViewKeepsTheLocalOption(t *testing.T) {
	service := newServiceWith(t, tempState(t), components.Set{Client: true, Server: true})
	_ = service.Open()
	for i := 0; i < 5; i++ {
		if err := service.Next(); err != nil {
			t.Fatalf("next: %v", err)
		}
	}
	if service.Step() != wizard.StepServer {
		t.Fatalf("step = %q, want server", service.Step())
	}
	view := service.View(wizard.Data{Catalog: i18n.For(i18n.English)})
	if !view.Server.LocalAvailable {
		t.Fatal("a Client and Server installation lost the Local Server option")
	}
}

func TestClientOnlyStepListMarksTheLocalStepsSkipped(t *testing.T) {
	service := newServiceWith(t, tempState(t), components.Set{Client: true})
	_ = service.Open()
	view := service.View(wizard.Data{Catalog: i18n.For(i18n.English)})
	statuses := map[wizard.Step]wizard.StepStatus{}
	for _, step := range view.Steps {
		statuses[step.ID] = step.Status
	}
	for _, step := range []wizard.Step{wizard.StepHardware, wizard.StepModel} {
		if statuses[step] != wizard.StatusSkipped {
			t.Fatalf("%s status = %q, want skipped", step, statuses[step])
		}
	}
	if statuses[wizard.StepServer] != wizard.StatusPending {
		t.Fatalf("server status = %q, want pending", statuses[wizard.StepServer])
	}
}
