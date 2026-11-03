package shell

// GNOME's GlobalShortcuts portal can bind an accelerator without ever
// delivering its activation on some GNOME versions. The Client therefore offers
// GNOME's own custom keybindings, which run `kitsune-client toggle` and
// `kitsune-client cancel` through the External trigger socket. This file keeps
// the offer, its two intents, and the binding sync after a hotkey save in one
// place.

// gnomeShortcutsView renders the offer or its installed state.
func (s *Shell) gnomeShortcutsView() GnomeShortcutsView {
	if s.opts.GnomeShortcuts == nil {
		return GnomeShortcutsView{}
	}
	catalog := s.catalog()
	view := GnomeShortcutsView{
		Visible: true,
		Title:   catalog.T("gnome.title"),
		Help:    catalog.T("gnome.help"),
		Add:     catalog.T("gnome.add"),
		Remove:  catalog.T("gnome.remove"),
	}
	status, err := s.opts.GnomeShortcuts.Status()
	if err != nil {
		view.Error = catalog.T("gnome.error", err.Error())
		return view
	}
	view.Installed = status.Installed
	if status.Installed {
		view.Help = catalog.T("gnome.installed", status.Toggle.Accelerator, status.Cancel.Accelerator)
		view.Add = catalog.T("gnome.update")
	}
	return view
}

// installGnomeShortcuts creates or updates the two GNOME keybindings and moves
// the Client to the External trigger, so the portal does not grab the same keys.
func (s *Shell) installGnomeShortcuts() {
	if s.opts.GnomeShortcuts == nil || s.opts.Settings == nil {
		s.pushMessages(nil, []string{s.catalog().T("error.gnome.unavailable")})
		return
	}
	cfg := s.opts.Settings.Config()
	// Release the portal's grab first: GNOME custom keybindings can only grab
	// an accelerator that nothing else holds, and it does not retry a failed
	// grab when the portal later lets go.
	s.save(map[string]any{"hotkey_backend": "external"})
	// Closing the portal session is not enough: GNOME keeps the assignments in
	// its global-shortcuts store and re-grabs them for the whole session, so
	// Ctrl+Shift+Space would still go to the dead portal session and the new
	// custom keybinding would never fire.
	if err := s.opts.GnomeShortcuts.ReleasePortal(); err != nil {
		s.log.Debug("gnome: releasing the portal shortcuts: %v", err)
	}
	if err := s.opts.GnomeShortcuts.Apply(cfg.Hotkey, cfg.CancelHotkey); err != nil {
		s.save(map[string]any{"hotkey_backend": "auto"})
		s.pushMessages(nil, []string{s.catalog().T("error.gnome.apply", err.Error())})
		s.pushSettings()
		s.pushWizard()
		return
	}
	s.pushMessages([]string{s.catalog().T("notice.gnome.added")}, nil)
	s.pushSettings()
	s.pushWizard()
}

// removeGnomeShortcuts deletes the two GNOME keybindings and keeps the Client
// off the portal, so removing the shortcuts stops the keys immediately.
func (s *Shell) removeGnomeShortcuts() {
	if s.opts.GnomeShortcuts == nil || s.opts.Settings == nil {
		s.pushMessages(nil, []string{s.catalog().T("error.gnome.unavailable")})
		return
	}
	if err := s.opts.GnomeShortcuts.Remove(); err != nil {
		s.pushMessages(nil, []string{s.catalog().T("gnome.error", err.Error())})
		s.pushSettings()
		return
	}
	// The portal must not keep its old accelerators either, or removing the
	// custom bindings would leave the keys answering a portal session that no
	// longer exists.
	if err := s.opts.GnomeShortcuts.ReleasePortal(); err != nil {
		s.log.Debug("gnome: releasing the portal shortcuts: %v", err)
	}
	// Switching back to "auto" would let the GlobalShortcuts portal grab the
	// same accelerators again, so the removal would look like it did nothing
	// until the Client restarted. Stay on the External trigger: the Dictation
	// form's backend selector is how a user returns to the portal.
	s.save(map[string]any{"hotkey_backend": "external"})
	s.pushMessages([]string{s.catalog().T("notice.gnome.removed")}, nil)
	s.pushSettings()
	s.pushWizard()
}

// syncGnomeShortcuts rewrites an installed pair after the hotkeys change. It
// returns a translated warning, or "" when there is nothing to do or the
// rewrite succeeded.
func (s *Shell) syncGnomeShortcuts() string {
	if s.opts.GnomeShortcuts == nil || s.opts.Settings == nil {
		return ""
	}
	status, err := s.opts.GnomeShortcuts.Status()
	if err != nil || !status.Installed {
		return ""
	}
	cfg := s.opts.Settings.Config()
	if err := s.opts.GnomeShortcuts.Apply(cfg.Hotkey, cfg.CancelHotkey); err != nil {
		return s.catalog().T("gnome.error", err.Error())
	}
	return ""
}
