package servers_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/servers"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/settings"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/trust"
	"github.com/zalando/go-keyring"
)

type fakeKeyring struct {
	values map[string]string
}

func newFakeKeyring() *fakeKeyring {
	return &fakeKeyring{values: map[string]string{}}
}

func (f *fakeKeyring) Get(service, user string) (string, error) {
	value, ok := f.values[service+"\x00"+user]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return value, nil
}

func (f *fakeKeyring) Set(service, user, secret string) error {
	f.values[service+"\x00"+user] = secret
	return nil
}

func (f *fakeKeyring) Delete(service, user string) error {
	delete(f.values, service+"\x00"+user)
	return nil
}

type fakeServer struct {
	*httptest.Server
	code        string
	token       string
	accepted    string
	fingerprint string
	devices     []map[string]any
	revoked     []string
	names       []string
	codeCalls   int
}

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	fake := &fakeServer{code: "ABCD2345", token: "ktn_remote", accepted: "ktn_remote"}
	fake.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/pair" {
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["code"] != fake.code {
				writeError(w, http.StatusUnauthorized, "pairing_code_wrong", "the Pairing code is not valid")
				return
			}
			if body["name"] == "" {
				t.Errorf("pair name is empty")
			}
			fake.names = append(fake.names, body["name"])
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"device": map[string]any{"id": "dev-2", "name": body["name"]},
				"token":  fake.token,
			})
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+fake.accepted {
			writeError(w, http.StatusUnauthorized, "unauthorized", "the Device token is invalid or has been revoked")
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/admin/devices":
			_ = json.NewEncoder(w).Encode(map[string]any{"devices": fake.devices})
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/admin/devices/"):
			id := strings.TrimPrefix(r.URL.Path, "/admin/devices/")
			if id != "dev-2" {
				writeError(w, http.StatusNotFound, "not_found", "no Device has that id")
				return
			}
			fake.revoked = append(fake.revoked, id)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == "/admin/pairing-code":
			fake.codeCalls++
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":       "NEW23456",
				"expires_at": 1234.0,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/admin/exposure":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			expose, _ := body["expose"].(bool)
			confirm, _ := body["confirm"].(bool)
			if expose && !confirm {
				writeError(w, http.StatusBadRequest, "confirmation_required", "confirm the change")
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"expose_to_network": expose,
				"bind_host":         map[bool]string{true: "0.0.0.0", false: "127.0.0.1"}[expose],
				"changed":           true,
				"restart_required":  []string{"server.expose_to_network"},
			})
		default:
			writeError(w, http.StatusNotFound, "not_found", "not found")
		}
	}))
	t.Cleanup(fake.Close)
	fake.fingerprint = trust.CertificateFingerprint(fake.Certificate())
	return fake
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}

func newService(t *testing.T) (*servers.Service, *settings.Store, *trust.TokenStore) {
	t.Helper()
	store, err := settings.Open(settings.Options{
		Path: t.TempDir() + "/kitsune.yaml",
		Env:  map[string]string{},
	})
	if err != nil {
		t.Fatalf("open settings: %v", err)
	}
	tokens := trust.NewTokenStoreWithKeyring(newFakeKeyring(), t.TempDir())
	return servers.New(servers.Options{Store: store, Tokens: tokens}), store, tokens
}

func TestAddRemotePairsPinsAndActivatesTheProfile(t *testing.T) {
	fake := newFakeServer(t)
	service, store, tokens := newService(t)

	profile, err := service.AddRemote(context.Background(), fake.URL, "abcd-2345", "Desktop")
	if err != nil {
		t.Fatalf("add remote: %v", err)
	}

	if !profile.Active {
		t.Fatalf("profile = %+v", profile)
	}
	if profile.Name == "Desktop" {
		t.Fatalf("the Device name replaced the profile name: %+v", profile)
	}
	if len(fake.names) != 1 || fake.names[0] != "Desktop" {
		t.Fatalf("Device names sent to the Server = %v", fake.names)
	}
	if profile.Fingerprint != fake.fingerprint {
		t.Fatalf("fingerprint = %q, want %q", profile.Fingerprint, fake.fingerprint)
	}
	snapshot := store.Snapshot()
	if snapshot.Config.ActiveServer != profile.ID {
		t.Fatalf("active = %q, want %q", snapshot.Config.ActiveServer, profile.ID)
	}
	if token, err := tokens.Load(profile.ID); err != nil || token != fake.token {
		t.Fatalf("token = %q, %v", token, err)
	}
	raw, err := json.Marshal(snapshot.Config.Servers)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), fake.token) {
		t.Fatalf("the Device token reached the configuration: %s", raw)
	}
}

func TestAddRemoteKeepsWorkingAcrossSettingsReloads(t *testing.T) {
	fake := newFakeServer(t)
	service, store, tokens := newService(t)
	profile, err := service.AddRemote(context.Background(), fake.URL, "ABCD2345", "Desktop")
	if err != nil {
		t.Fatalf("add remote: %v", err)
	}

	reopened, err := settings.Open(settings.Options{
		Path: store.Snapshot().Path,
		Env:  map[string]string{},
	})
	if err != nil {
		t.Fatalf("reopen settings: %v", err)
	}
	restarted := servers.New(servers.Options{Store: reopened, Tokens: tokens})
	view := restarted.View()
	if view.ActiveID != profile.ID {
		t.Fatalf("active = %q, want %q", view.ActiveID, profile.ID)
	}
	if len(view.Profiles) != 2 {
		t.Fatalf("profiles = %+v", view.Profiles)
	}
	if _, err := restarted.LoadDevices(context.Background(), profile.ID); err != nil {
		t.Fatalf("load devices after reload: %v", err)
	}
}

func TestAddRemoteAssumesHTTPSAndStripsATrailingSlash(t *testing.T) {
	fake := newFakeServer(t)
	service, store, _ := newService(t)
	address := strings.TrimPrefix(fake.URL, "https://")

	profile, err := service.AddRemote(context.Background(), address+"/", "ABCD2345", "")
	if err != nil {
		t.Fatalf("add remote: %v", err)
	}
	if profile.URL != fake.URL {
		t.Fatalf("url = %q, want %q", profile.URL, fake.URL)
	}
	if profile.Name != strings.TrimPrefix(fake.URL, "https://") {
		t.Fatalf("default name = %q", profile.Name)
	}
	if store.Snapshot().Config.ActiveServer != profile.ID {
		t.Fatalf("profile was not active")
	}
}

func TestAddRemoteRejectsAWrongCodeWithoutSavingAnything(t *testing.T) {
	fake := newFakeServer(t)
	service, store, _ := newService(t)

	_, err := service.AddRemote(context.Background(), fake.URL, "ZZZZ2345", "Desktop")
	var failure *trust.PairingHTTPError
	if !errors.As(err, &failure) {
		t.Fatalf("err = %v, want PairingHTTPError", err)
	}
	if failure.Code != "pairing_code_wrong" {
		t.Fatalf("failure = %+v", failure)
	}
	if len(store.Snapshot().Config.Servers) != 1 {
		t.Fatalf("a rejected pair added a profile: %+v", store.Snapshot().Config.Servers)
	}
}

func TestRepairUpdatesAnExistingProfileInsteadOfDuplicatingIt(t *testing.T) {
	fake := newFakeServer(t)
	service, store, _ := newService(t)
	first, err := service.AddRemote(context.Background(), fake.URL, "ABCD2345", "Desktop")
	if err != nil {
		t.Fatalf("first pair: %v", err)
	}

	second, err := service.AddRemote(context.Background(), fake.URL, "ABCD2345", "Desktop again")
	if err != nil {
		t.Fatalf("second pair: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("id = %q, want %q", second.ID, first.ID)
	}
	if len(store.Snapshot().Config.Servers) != 2 {
		t.Fatalf("profiles = %+v", store.Snapshot().Config.Servers)
	}
	if second.Name != first.Name {
		t.Fatalf("profile name = %q, want %q", second.Name, first.Name)
	}
	if len(fake.names) != 2 || fake.names[1] != "Desktop again" {
		t.Fatalf("Device names sent to the Server = %v", fake.names)
	}
}

func TestLoadDevicesAndRevokeUseTheAdminAPI(t *testing.T) {
	fake := newFakeServer(t)
	fake.devices = []map[string]any{
		{"id": "dev-1", "name": "Owner laptop", "created_at": 100.0, "last_seen": 200.0},
		{"id": "dev-2", "name": "Laptop", "created_at": 110.0, "last_seen": nil},
	}
	service, _, _ := newService(t)
	profile, err := service.AddRemote(context.Background(), fake.URL, "ABCD2345", "Desktop")
	if err != nil {
		t.Fatalf("pair: %v", err)
	}

	devices, err := service.LoadDevices(context.Background(), profile.ID)
	if err != nil {
		t.Fatalf("list devices: %v", err)
	}
	if len(devices) != 2 || devices[0].Name != "Laptop" {
		t.Fatalf("devices = %+v", devices)
	}
	if devices[0].LastSeen != nil {
		t.Fatalf("Laptop last_seen = %v, want nil", devices[0].LastSeen)
	}
	if view := service.View(); len(view.Devices) != 2 || view.DevicesError != "" {
		t.Fatalf("view = %+v", view)
	}

	if err := service.RevokeDevice(context.Background(), profile.ID, "dev-2"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if len(fake.revoked) != 1 || fake.revoked[0] != "dev-2" {
		t.Fatalf("revoked = %+v", fake.revoked)
	}
}

func TestARevokedTokenSurfacesAsAnActionablePairAgainError(t *testing.T) {
	fake := newFakeServer(t)
	service, _, tokens := newService(t)
	profile, err := service.AddRemote(context.Background(), fake.URL, "ABCD2345", "Desktop")
	if err != nil {
		t.Fatalf("pair: %v", err)
	}
	fake.accepted = "ktn_valid"
	if err := tokens.Save(profile.ID, "ktn_revoked"); err != nil {
		t.Fatalf("save: %v", err)
	}

	_, err = service.LoadDevices(context.Background(), profile.ID)

	var adminErr *servers.AdminError
	if !errors.As(err, &adminErr) {
		t.Fatalf("err = %v, want AdminError", err)
	}
	if adminErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d", adminErr.StatusCode)
	}
	if !strings.Contains(adminErr.Error(), "pair") {
		t.Fatalf("message = %q", adminErr.Error())
	}
	if view := service.View(); !strings.Contains(view.DevicesError, "pair") {
		t.Fatalf("view error = %q", view.DevicesError)
	}
}

func TestSetExposureSendsTheConfirmationAndCachesTheResult(t *testing.T) {
	fake := newFakeServer(t)
	service, _, _ := newService(t)
	profile, err := service.AddRemote(context.Background(), fake.URL, "ABCD2345", "Desktop")
	if err != nil {
		t.Fatalf("pair: %v", err)
	}

	result, err := service.SetExposure(context.Background(), profile.ID, true)
	if err != nil {
		t.Fatalf("expose: %v", err)
	}
	if !result.Expose || result.BindHost != "0.0.0.0" {
		t.Fatalf("result = %+v", result)
	}
	if view := service.View(); view.Exposure == nil || !view.Exposure.Expose {
		t.Fatalf("view = %+v", view)
	}
}

func TestGenerateCodeCachesTheResult(t *testing.T) {
	fake := newFakeServer(t)
	service, _, _ := newService(t)
	profile, err := service.AddRemote(context.Background(), fake.URL, "ABCD2345", "Desktop")
	if err != nil {
		t.Fatalf("pair: %v", err)
	}

	code, err := service.GenerateCode(context.Background(), profile.ID)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if code.Code != "NEW23456" || fake.codeCalls != 1 {
		t.Fatalf("code = %+v, calls = %d", code, fake.codeCalls)
	}
	if view := service.View(); view.PairingCode == nil || view.PairingCode.Code != "NEW23456" {
		t.Fatalf("view = %+v", view)
	}
}

func TestAdminCallsRequireAToken(t *testing.T) {
	service, _, _ := newService(t)
	_, err := service.LoadDevices(context.Background(), "local")
	if !errors.Is(err, servers.ErrNoToken) {
		t.Fatalf("err = %v, want ErrNoToken", err)
	}
}
