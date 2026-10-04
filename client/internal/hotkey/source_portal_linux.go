//go:build linux

package hotkey

import (
	"context"
	"fmt"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/cycle"

	"github.com/godbus/dbus/v5"
)

const (
	portalBusName    = "org.freedesktop.portal.Desktop"
	portalIface      = "org.freedesktop.portal.GlobalShortcuts"
	portalObjectPath = dbus.ObjectPath("/org/freedesktop/portal/desktop")

	requestIface = "org.freedesktop.portal.Request"

	sessionIface = "org.freedesktop.portal.Session"

	registryBusName = "org.freedesktop.host.portal.Registry"
	registryIface   = "org.freedesktop.host.portal.Registry"
	registryPath    = dbus.ObjectPath("/org/freedesktop/host/portal/Registry")

	// Shortcut ids reported back by the portal.
	mainShortcutID   = "toggle"
	cancelShortcutID = "cancel"
)

// portalShortcut is one entry of the a(sa{sv}) array BindShortcuts expects.
type portalShortcut struct {
	ID    string
	Props map[string]dbus.Variant
}

// portalSource binds global hotkeys through the xdg-desktop-portal
// GlobalShortcuts interface. The portal shows a one-time approval dialog; a
// denial surfaces as a request error and the Client falls back to the External
// trigger.
type portalSource struct {
	main   string
	cancel string
	mode   Mode
	appID  string
	log    func(string)
}

// portalAvailable reports whether the session bus exposes the GlobalShortcuts
// portal. It is best-effort: any failure, including no session bus, means the
// portal is unavailable.
func portalAvailable() bool {
	conn, err := dbus.SessionBus()
	if err != nil {
		return false
	}
	var version uint32
	err = conn.Object(portalBusName, portalObjectPath).
		Call("org.freedesktop.DBus.Properties.Get", 0, portalIface, "version").
		Store(&version)
	return err == nil && version >= 1
}

// newPortalSource builds a portal-backed Source, converting the configured
// hotkey specs into portal accelerators up front so a bad key fails fast.
func newPortalSource(opts Options) (Source, error) {
	mainTrigger, err := PortalTrigger(opts.Hotkey)
	if err != nil {
		return nil, err
	}
	cancelTrigger, err := PortalTrigger(opts.CancelHotkey)
	if err != nil {
		return nil, err
	}
	appID := opts.AppID
	if appID == "" {
		appID = DefaultAppID
	}
	log := opts.OnLog
	if log == nil {
		log = func(string) {}
	}
	return &portalSource{
		main:   mainTrigger,
		cancel: cancelTrigger,
		mode:   opts.Mode,
		appID:  appID,
		log:    log,
	}, nil
}

// Run creates a portal session, binds both shortcuts, and forwards activation
// edges into triggers until ctx ends. If the portal is unavailable or the user
// denies the request, it degrades to the External trigger (the control socket,
// which runs alongside) rather than stopping the Client.
func (s *portalSource) Run(ctx context.Context, triggers chan<- cycle.Trigger) error {
	// A runPortal error is not fatal: the control socket keeps the External
	// trigger alive, so the Client waits here instead of exiting.
	//nolint:nilerr // portal failure degrades to the external trigger
	if err := s.runPortal(ctx, triggers); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		s.log(fmt.Sprintf("hotkey: GlobalShortcuts portal unavailable, using the external trigger: %v", err))
		<-ctx.Done()
	}
	return nil
}

func (s *portalSource) runPortal(ctx context.Context, triggers chan<- cycle.Trigger) error {
	conn, err := dbus.SessionBus()
	if err != nil {
		return fmt.Errorf("connect to the session bus: %w", err)
	}
	signals := make(chan *dbus.Signal, 16)
	conn.Signal(signals)
	defer conn.RemoveSignal(signals)

	s.registerAppID(conn)

	session, err := s.createSession(ctx, conn, signals)
	if err != nil {
		return err
	}
	defer func() {
		_ = conn.Object(portalBusName, session).Call(sessionIface+".Close", 0).Err
	}()

	if err := s.bindShortcuts(ctx, conn, signals, session); err != nil {
		return err
	}
	s.log(fmt.Sprintf("hotkey: portal session %s bound to %s / %s", session, s.main, s.cancel))
	return s.listen(ctx, conn, signals, session, triggers)
}

// registerAppID associates the D-Bus connection with the app id so the portal
// can name the Client in its dialog. Best-effort: a failure just means the
// portal falls back to identifying the executable.
func (s *portalSource) registerAppID(conn *dbus.Conn) {
	if err := conn.Object(registryBusName, registryPath).
		Call(registryIface+".Register", 0, s.appID, map[string]dbus.Variant{}).Err; err != nil {
		s.log(fmt.Sprintf("hotkey: could not register app id %s: %v", s.appID, err))
	}
}

func (s *portalSource) createSession(ctx context.Context, conn *dbus.Conn, signals chan *dbus.Signal) (dbus.ObjectPath, error) {
	options := map[string]dbus.Variant{
		"handle_token":         dbus.MakeVariant("kitsune"),
		"session_handle_token": dbus.MakeVariant("kitsune"),
	}
	var handle dbus.ObjectPath
	if err := conn.Object(portalBusName, portalObjectPath).
		Call(portalIface+".CreateSession", 0, options).Store(&handle); err != nil {
		return "", fmt.Errorf("create portal session: %w", err)
	}
	results, err := s.awaitResponse(ctx, conn, signals, handle)
	if err != nil {
		return "", err
	}
	raw, ok := results["session_handle"]
	if !ok {
		return "", fmt.Errorf("portal CreateSession response carried no session_handle")
	}
	path, ok := raw.Value().(string)
	if !ok {
		return "", fmt.Errorf("portal session_handle has unexpected type %T", raw.Value())
	}
	return dbus.ObjectPath(path), nil
}

func (s *portalSource) bindShortcuts(ctx context.Context, conn *dbus.Conn, signals chan *dbus.Signal, session dbus.ObjectPath) error {
	shortcuts := []portalShortcut{
		{ID: mainShortcutID, Props: map[string]dbus.Variant{
			"description":       dbus.MakeVariant("Start or stop dictation"),
			"preferred_trigger": dbus.MakeVariant(s.main),
		}},
		{ID: cancelShortcutID, Props: map[string]dbus.Variant{
			"description":       dbus.MakeVariant("Cancel dictation"),
			"preferred_trigger": dbus.MakeVariant(s.cancel),
		}},
	}
	options := map[string]dbus.Variant{"handle_token": dbus.MakeVariant("kitsune_bind")}
	var handle dbus.ObjectPath
	if err := conn.Object(portalBusName, portalObjectPath).
		Call(portalIface+".BindShortcuts", 0, session, shortcuts, "", options).Store(&handle); err != nil {
		return fmt.Errorf("bind portal shortcuts: %w", err)
	}
	if _, err := s.awaitResponse(ctx, conn, signals, handle); err != nil {
		return err
	}
	return nil
}

// awaitResponse blocks for the Response signal of a portal request.
func (s *portalSource) awaitResponse(ctx context.Context, conn *dbus.Conn, signals chan *dbus.Signal, request dbus.ObjectPath) (map[string]dbus.Variant, error) {
	if err := conn.AddMatchSignal(
		dbus.WithMatchObjectPath(request),
		dbus.WithMatchInterface(requestIface),
		dbus.WithMatchMember("Response"),
	); err != nil {
		return nil, fmt.Errorf("subscribe to portal response: %w", err)
	}
	defer func() {
		_ = conn.RemoveMatchSignal(dbus.WithMatchObjectPath(request), dbus.WithMatchInterface(requestIface), dbus.WithMatchMember("Response"))
	}()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case sig := <-signals:
			if sig == nil || sig.Path != request {
				continue
			}
			return parseResponse(sig)
		}
	}
}

func parseResponse(sig *dbus.Signal) (map[string]dbus.Variant, error) {
	if len(sig.Body) < 2 {
		return nil, fmt.Errorf("portal response %s has %d fields", sig.Name, len(sig.Body))
	}
	code, ok := sig.Body[0].(uint32)
	if !ok {
		return nil, fmt.Errorf("portal response code has unexpected type %T", sig.Body[0])
	}
	results, _ := sig.Body[1].(map[string]dbus.Variant)
	if code != 0 {
		return nil, fmt.Errorf("portal request %s was declined (response %d)", sig.Path, code)
	}
	return results, nil
}

func (s *portalSource) listen(ctx context.Context, conn *dbus.Conn, signals chan *dbus.Signal, session dbus.ObjectPath, triggers chan<- cycle.Trigger) error {
	for _, member := range []string{"Activated", "Deactivated"} {
		if err := conn.AddMatchSignal(
			dbus.WithMatchObjectPath(portalObjectPath),
			dbus.WithMatchInterface(portalIface),
			dbus.WithMatchMember(member),
		); err != nil {
			return fmt.Errorf("subscribe to portal %s: %w", member, err)
		}
	}

	reducer := NewReducer(s.mode)
	for {
		select {
		case <-ctx.Done():
			return nil
		case sig := <-signals:
			if sig == nil {
				return nil
			}
			s.handleSignal(ctx, sig, session, reducer, triggers)
		}
	}
}

func (s *portalSource) handleSignal(ctx context.Context, sig *dbus.Signal, session dbus.ObjectPath, reducer *Reducer, triggers chan<- cycle.Trigger) {
	var edge Edge
	switch sig.Name {
	case portalIface + ".Activated":
		edge = Down
	case portalIface + ".Deactivated":
		edge = Up
	default:
		return
	}
	if len(sig.Body) < 2 {
		return
	}
	gotSession, ok := sig.Body[0].(dbus.ObjectPath)
	if !ok || gotSession != session {
		return
	}
	id, ok := sig.Body[1].(string)
	if !ok {
		return
	}
	var slot Slot
	switch id {
	case mainShortcutID:
		slot = Main
	case cancelShortcutID:
		slot = Cancel
	default:
		return
	}
	emit(ctx, triggers, reducer, slot, edge)
}
