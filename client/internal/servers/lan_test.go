package servers_test

import (
	"context"
	"testing"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/config"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/servers"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/settings"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/trust"
)

// lanService is a Servers service whose LAN address and health probe are fixed,
// so the rendered View never depends on the test host's interfaces.
func lanService(t *testing.T, status trust.Status, lanAddress string) (*servers.Service, *settings.Store) {
	t.Helper()
	store, err := settings.Open(settings.Options{
		Path: t.TempDir() + "/kitsune.yaml",
		Env:  map[string]string{},
	})
	if err != nil {
		t.Fatalf("open settings: %v", err)
	}
	tokens := trust.NewTokenStoreWithKeyring(newFakeKeyring(), t.TempDir())
	service := servers.New(servers.Options{
		Store:      store,
		Tokens:     tokens,
		LANAddress: func() string { return lanAddress },
		Check: func(context.Context, config.ServerProfile, string, time.Duration) trust.Status {
			return status
		},
	})
	return service, store
}

func exposedStatus(port int) trust.Status {
	return trust.Status{
		State: trust.StateOnline,
		Network: &trust.NetworkStatus{
			ExposeToNetwork: true, BindHost: "0.0.0.0", Port: port,
		},
	}
}

func TestViewShowsTheLANConnectAddressForTheLocalServer(t *testing.T) {
	service, _ := lanService(t, exposedStatus(8765), "192.168.3.22")
	service.CheckHealth(context.Background(), config.LocalServerID)

	view := service.View()
	if view.ConnectAddress != "192.168.3.22:8765" {
		t.Fatalf("connect address = %q, want the local Server's LAN address", view.ConnectAddress)
	}
}

func TestViewUsesTheLivePortForTheLANConnectAddress(t *testing.T) {
	service, _ := lanService(t, exposedStatus(9443), "192.168.3.22")
	service.CheckHealth(context.Background(), config.LocalServerID)

	if got := service.View().ConnectAddress; got != "192.168.3.22:9443" {
		t.Fatalf("connect address = %q, want the live listener's port", got)
	}
}

func TestViewFallsBackToTheProfilePortWithoutALiveListener(t *testing.T) {
	service, _ := lanService(t, trust.Status{
		State:   trust.StateOnline,
		Network: &trust.NetworkStatus{ExposeToNetwork: true, BindHost: "0.0.0.0"},
	}, "192.168.3.22")
	service.CheckHealth(context.Background(), config.LocalServerID)

	if got := service.View().ConnectAddress; got != "192.168.3.22:8765" {
		t.Fatalf("connect address = %q, want the profile's port", got)
	}
}

func TestViewHidesTheConnectAddressWhileLANExposureIsOff(t *testing.T) {
	service, _ := lanService(t, trust.Status{
		State:   trust.StateOnline,
		Network: &trust.NetworkStatus{BindHost: "127.0.0.1", Port: 8765},
	}, "192.168.3.22")
	service.CheckHealth(context.Background(), config.LocalServerID)

	if got := service.View().ConnectAddress; got != "" {
		t.Fatalf("connect address = %q, want none while LAN exposure is off", got)
	}
}

func TestViewHidesTheConnectAddressForARemoteServer(t *testing.T) {
	service, store := lanService(t, exposedStatus(8765), "192.168.3.22")
	if _, err := store.Save(map[string]any{
		"servers": []any{
			map[string]any{
				"id": "remote", "name": "Remote", "url": "https://192.168.3.50:8765",
			},
		},
		"active_server": "remote",
	}); err != nil {
		t.Fatalf("save remote profile: %v", err)
	}
	service.CheckHealth(context.Background(), "remote")

	if got := service.View().ConnectAddress; got != "" {
		t.Fatalf("connect address = %q, want none for a Remote Server", got)
	}
}

func TestViewDoesNotLeakAnotherProfilesExposureToggle(t *testing.T) {
	fake := newFakeServer(t)
	service, _ := lanService(t, trust.Status{State: trust.StateUnknown}, "192.168.3.22")
	remote, err := service.AddRemote(context.Background(), fake.URL, "ABCD2345", "Desktop")
	if err != nil {
		t.Fatalf("pair: %v", err)
	}
	if _, err := service.SetExposure(context.Background(), remote.ID, true); err != nil {
		t.Fatalf("expose: %v", err)
	}
	if _, err := service.SetActive(config.LocalServerID); err != nil {
		t.Fatalf("activate local: %v", err)
	}

	if got := service.View().ConnectAddress; got != "" {
		t.Fatalf("connect address = %q, want none for a profile that was never exposed", got)
	}
}

func TestViewHidesTheConnectAddressWithoutALANAddress(t *testing.T) {
	service, _ := lanService(t, exposedStatus(8765), "")
	service.CheckHealth(context.Background(), config.LocalServerID)

	if got := service.View().ConnectAddress; got != "" {
		t.Fatalf("connect address = %q, want none without a local-network address", got)
	}
}
