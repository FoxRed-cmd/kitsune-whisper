package wizard

import (
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/i18n"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/servers"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/trust"
)

// Option is one translated choice a wizard step offers.
type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Data is the shell-supplied input the wizard view is computed from. It is a
// snapshot of the existing services; the wizard never performs I/O beyond its
// own progress file.
type Data struct {
	Catalog        i18n.Catalog
	Version        string
	DeviceOptions  []Option
	SelectedDevice string
	DeviceError    string
	Hotkey         string
	Trigger        string
	Servers        servers.View
	Server         trust.Status
	Running        bool
	Phase          string
	RuntimeError   string
}

// Presence is what the Client knows about the active model.
type Presence struct {
	// Fresh reports that no wizard progress file existed at Open.
	Fresh bool
	// ModelKnown reports that the Server supplied enough information to judge.
	ModelKnown bool
	// ModelActive reports that an installed, non-failed model is selected.
	ModelActive bool
}

// ModelPresence reads the model state from the catalog, the cached lifecycle,
// or the live /status body, in that order. Cold and stopped models still count
// as active: the Server loads a cold model on demand, and a soft-stop has a
// start action.
func ModelPresence(data Data) Presence {
	models := data.Servers.Models
	if models != nil {
		name := activeModelFromModels(models)
		if name == "" {
			return Presence{ModelKnown: true}
		}
		state := models.State
		if models.ActivePath != "" {
			return Presence{ModelKnown: true, ModelActive: state != "failed"}
		}
		for _, model := range models.Models {
			if model.Name != name {
				continue
			}
			if !model.Installed {
				return Presence{ModelKnown: true}
			}
			return Presence{ModelKnown: true, ModelActive: state != "failed"}
		}
		return Presence{ModelKnown: true}
	}
	if lifecycle := data.Servers.Lifecycle; lifecycle != nil {
		return Presence{
			ModelKnown:  true,
			ModelActive: lifecycle.Model != "" && lifecycle.ModelState != "failed",
		}
	}
	if data.Server.State == trust.StateOnline {
		return Presence{
			ModelKnown:  true,
			ModelActive: data.Server.Model != "" && data.Server.ModelState != "failed",
		}
	}
	return Presence{}
}

// ActiveModel returns the selected model name and its lifecycle state.
func ActiveModel(data Data) (name, state string) {
	if models := data.Servers.Models; models != nil {
		name = activeModelFromModels(models)
		return name, models.State
	}
	if lifecycle := data.Servers.Lifecycle; lifecycle != nil {
		return lifecycle.Model, lifecycle.ModelState
	}
	if data.Server.State == trust.StateOnline {
		return data.Server.Model, data.Server.ModelState
	}
	return "", ""
}

func activeModelFromModels(models *servers.ModelsView) string {
	if models == nil {
		return ""
	}
	if models.ActivePath != "" {
		return models.ActivePath
	}
	return models.ActiveModel
}

// HardwarePlan is the CUDA/CPU recommendation for the hardware step.
type HardwarePlan struct {
	// Known reports that the Server supplied device information.
	Known bool
	// Available reports that CUDA was detected, even if the Server is pinned
	// to CPU.
	Available bool
	// DeviceCount is the number of CUDA devices.
	DeviceCount int
	// Resolved is the device the Server currently uses: cuda or cpu.
	Resolved string
	// Recommend is auto or cpu.
	Recommend string
	// Pinned reports CUDA is available but the Server resolves to CPU.
	Pinned bool
}

// PlanHardware derives the device choice from the authenticated /status gpu
// block, falling back to the model catalog's resolved device when an older
// Server omits gpu.
func PlanHardware(data Data) HardwarePlan {
	plan := HardwarePlan{Recommend: "auto"}
	if models := data.Servers.Models; models != nil && models.Device != "" {
		plan.Known = true
		plan.Resolved = models.Device
	}
	if plan.Resolved == "" && data.Server.Device != "" {
		plan.Known = true
		plan.Resolved = data.Server.Device
	}
	if gpu := data.Server.GPU; gpu != nil {
		plan.Known = true
		plan.Available = gpu.Available
		plan.DeviceCount = gpu.DeviceCount
		if gpu.Device != "" {
			plan.Resolved = gpu.Device
		}
	} else if plan.Resolved != "" {
		plan.Available = plan.Resolved == "cuda"
	}
	if plan.Known && !plan.Available {
		plan.Recommend = "cpu"
	}
	plan.Pinned = plan.Available && plan.Resolved != "" && plan.Resolved != "cuda"
	return plan
}

// RecommendModel chooses the model the wizard suggests. It prefers the Server's
// recommendation, then the catalog's recommended row, then the fixed
// per-device preference, then the first catalog entry.
func RecommendModel(models *servers.ModelsView) string {
	if models == nil {
		return ""
	}
	if models.Recommended != "" {
		return models.Recommended
	}
	for _, model := range models.Models {
		if model.Recommended {
			return model.Name
		}
	}
	preferred := "base"
	if models.Device == "cuda" {
		preferred = "medium"
	}
	if hasModel(models.Models, preferred) {
		return preferred
	}
	if hasModel(models.Models, "small") {
		return "small"
	}
	if len(models.Models) > 0 {
		return models.Models[0].Name
	}
	return ""
}

func hasModel(models []servers.Model, name string) bool {
	for _, model := range models {
		if model.Name == name {
			return true
		}
	}
	return false
}

// Reason is one missing prerequisite for the final try-it step. Key and Args
// are translated by the view; Step is where the user can fix it.
type Reason struct {
	Key  string
	Args []any
	Step Step
}

// TryReadiness lists everything that must be fixed before a real Dictation
// cycle can run. An empty result means the wizard may send the trigger.
func TryReadiness(data Data) []Reason {
	var reasons []Reason
	presence := ModelPresence(data)
	name := profileName(data.Servers)
	switch {
	case data.Servers.ActiveID == "":
		reasons = append(reasons, Reason{Key: "wizard.try.missing.profile", Step: StepServer})
	case data.Server.State != trust.StateOnline:
		key := "wizard.try.missing.server"
		switch data.Server.State {
		case trust.StateUnauthenticated:
			key = "wizard.try.missing.unauthenticated"
		case trust.StateUnpinned:
			key = "wizard.try.missing.unpinned"
		case trust.StateMismatch:
			key = "wizard.try.missing.mismatch"
		}
		reasons = append(reasons, Reason{
			Key:  key,
			Args: []any{name},
			Step: StepServer,
		})
	default:
		_, state := ActiveModel(data)
		switch {
		case state == "failed":
			reasons = append(reasons, Reason{
				Key:  "wizard.try.missing.modelFailed",
				Args: []any{name},
				Step: StepModel,
			})
		case presence.ModelKnown && !presence.ModelActive:
			reasons = append(reasons, Reason{
				Key:  "wizard.try.missing.model",
				Args: []any{name},
				Step: StepModel,
			})
		case presence.ModelActive && state == "stopped":
			reasons = append(reasons, Reason{
				Key:  "wizard.try.missing.stopped",
				Args: []any{name},
				Step: StepModel,
			})
		}
	}
	if !data.Running {
		reasons = append(reasons, Reason{Key: "wizard.try.missing.runtime"})
	}
	if data.DeviceError != "" {
		reasons = append(reasons, Reason{
			Key:  "wizard.try.missing.mic",
			Args: []any{data.DeviceError},
			Step: StepMicrophone,
		})
	}
	return reasons
}

func profileName(view servers.View) string {
	for _, profile := range view.Profiles {
		if profile.ID == view.ActiveID {
			if profile.Name != "" {
				return profile.Name
			}
			return profile.ID
		}
	}
	return view.ActiveID
}
