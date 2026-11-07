package shell

import (
	"context"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/update"
)

// Updater checks GitHub Releases and installs the artifact it offers.
// *update.Service implements it.
type Updater interface {
	Check(ctx context.Context) (update.Info, error)
	Install(ctx context.Context, info update.Info) (string, error)
}

// UpdateLabels are the translated strings of the updates screen and Tray item.
type UpdateLabels struct {
	Title               string `json:"title"`
	Help                string `json:"help"`
	AutoCheck           string `json:"autoCheck"`
	AutoCheckHelp       string `json:"autoCheckHelp"`
	Current             string `json:"current"`
	Latest              string `json:"latest"`
	Check               string `json:"check"`
	Download            string `json:"download"`
	AvailableMessage    string `json:"availableMessage"`
	UpToDateMessage     string `json:"upToDate"`
	NotInstalledMessage string `json:"notInstalled"`
	Checking            string `json:"checking"`
	Downloading         string `json:"downloading"`
	LaunchedMessage     string `json:"launched"`
	ReleasePage         string `json:"releasePage"`
	DevBuildMessage     string `json:"devBuild"`
	CheckUpdates        string `json:"checkUpdates"`
	UpdateTo            string `json:"updateTo"`
	Updating            string `json:"updating"`
}

// UpdateView is the Settings window's Updates screen.
type UpdateView struct {
	UpdateLabels
	CurrentVersion string `json:"currentVersion"`
	LatestVersion  string `json:"latestVersion,omitempty"`
	ReleaseURL     string `json:"releaseUrl,omitempty"`
	// CheckEnabled is the automatic release-check setting.
	CheckEnabled bool `json:"checkEnabled"`
	// Checked reports that at least one release check completed.
	Checked bool `json:"checked"`
	// Installable reports that this platform has a downloadable installer.
	Installable bool `json:"installable"`
	// Busy reports an in-flight check or download.
	Busy bool `json:"busy"`
	// Installing distinguishes a download from a plain release check.
	Installing bool `json:"installing"`
	// Launched reports that the verified installer was started.
	Launched bool `json:"launched"`
	// Available reports a newer release.
	Available bool `json:"available"`
	// Error is the last check or install failure, or "".
	Error string `json:"error,omitempty"`
}

// SnapshotUpdate is the compact update state the Tray renders.
type SnapshotUpdate struct {
	Available  bool   `json:"available"`
	Latest     string `json:"latest,omitempty"`
	Busy       bool   `json:"busy,omitempty"`
	Installing bool   `json:"installing,omitempty"`
	Error      string `json:"error,omitempty"`
}

func (s *Shell) updateLabels() UpdateLabels {
	catalog := s.catalog()
	return UpdateLabels{
		Title:               catalog.T("update.title"),
		Help:                catalog.T("update.help"),
		AutoCheck:           catalog.T("update.autoCheck"),
		AutoCheckHelp:       catalog.T("update.autoCheckHelp"),
		Current:             catalog.T("update.current"),
		Latest:              catalog.T("update.latest"),
		Check:               catalog.T("update.check"),
		Download:            catalog.T("update.download"),
		AvailableMessage:    catalog.T("update.available"),
		UpToDateMessage:     catalog.T("update.upToDate"),
		NotInstalledMessage: catalog.T("update.notInstalled"),
		Checking:            catalog.T("update.checking"),
		Downloading:         catalog.T("update.downloading"),
		LaunchedMessage:     catalog.T("update.launched"),
		ReleasePage:         catalog.T("update.releasePage"),
		DevBuildMessage:     catalog.T("update.devBuild"),
		CheckUpdates:        catalog.T("tray.checkUpdates"),
		UpdateTo:            catalog.T("tray.updateTo"),
		Updating:            catalog.T("tray.updating"),
	}
}

func (s *Shell) updateView() UpdateView {
	if s.opts.Updater == nil {
		return UpdateView{}
	}
	s.mu.Lock()
	info, checked, busy, installing, launched, updateErr :=
		s.updateInfo, s.updateChecked, s.updateBusy, s.updateInstalling, s.updateLaunched, s.updateError
	s.mu.Unlock()
	labels := s.updateLabels()
	return UpdateView{
		UpdateLabels:   labels,
		CurrentVersion: s.opts.Version,
		LatestVersion:  info.Latest,
		ReleaseURL:     info.URL,
		CheckEnabled:   s.updatesCheckEnabled(),
		Checked:        checked,
		Installable:    info.Installable,
		Busy:           busy,
		Installing:     installing,
		Launched:       launched,
		Available:      info.Available,
		Error:          updateErr,
	}
}

// snapshotUpdate is the compact state the Tray renders. s.mu must be held.
func (s *Shell) snapshotUpdateLocked() SnapshotUpdate {
	return SnapshotUpdate{
		Available:  s.updateInfo.Available && !s.updateLaunched,
		Latest:     s.updateInfo.Latest,
		Busy:       s.updateBusy,
		Installing: s.updateInstalling,
		Error:      s.updateError,
	}
}

// checkUpdates asks GitHub Releases for the latest version. A failure is
// logged; a manual check also raises the translated message.
func (s *Shell) checkUpdates(ctx context.Context, manual bool) {
	if s.opts.Updater == nil {
		if manual {
			s.pushMessages(nil, []string{s.catalog().T("error.update.unavailable")})
		}
		return
	}
	s.mu.Lock()
	if s.updateBusy {
		s.mu.Unlock()
		return
	}
	s.updateBusy = true
	s.updateInstalling = false
	s.mu.Unlock()
	s.pushSettings()

	checkCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	info, err := s.opts.Updater.Check(checkCtx)
	// Disabling automatic checks cancels an in-flight check. That is not a
	// failure: it leaves the card and the message area as they were.
	cancelled := ctx.Err() != nil

	s.mu.Lock()
	s.updateBusy = false
	if !cancelled {
		s.updateChecked = true
		if err != nil {
			s.updateError = err.Error()
		} else {
			s.updateInfo = info
			s.updateError = ""
		}
	}
	s.mu.Unlock()
	if cancelled {
		return
	}
	s.pushSettings()
	s.update(s.opts.Runtime.Status())
	if err != nil {
		s.log.Debug("update: check failed: %v", err)
		// A background failure stays in the card and the log; only a check the
		// user asked for raises the one message area.
		if manual {
			s.pushMessages(nil, []string{s.catalog().T("error.update.check", err.Error())})
		}
		return
	}
	if manual {
		if info.Available {
			s.pushMessages([]string{s.catalog().T("notice.update.available", info.Latest)}, nil)
		} else {
			s.pushMessages([]string{s.catalog().T("notice.update.current")}, nil)
		}
	}
}

// updatesCheckEnabled reports the client.updates.check setting. Without a
// settings service the Client keeps the built-in default: automatic checks on.
func (s *Shell) updatesCheckEnabled() bool {
	if s.opts.Settings == nil {
		return true
	}
	return s.opts.Settings.Config().Updates.Check
}

// reconcileAutoUpdates wakes the scheduler after a written settings save. The
// buffered channel coalesces unrelated saves, and the scheduler ignores a
// reconcile that does not change the setting.
func (s *Shell) reconcileAutoUpdates() {
	if s.opts.Updater == nil || s.autoUpdateSync == nil {
		return
	}
	select {
	case s.autoUpdateSync <- struct{}{}:
	default:
	}
}

// autoUpdateDelay is the wait before the startup check; 0 means 60 seconds.
func (s *Shell) autoUpdateDelay() time.Duration {
	if s.opts.UpdateCheckDelay > 0 {
		return s.opts.UpdateCheckDelay
	}
	return 60 * time.Second
}

// runAutoUpdates runs the automatic release check. It waits for the startup
// delay when the setting is on, reacts to the toggle being saved, and cancels
// an in-flight automatic check when the setting is turned off. There are no
// periodic checks: the setting allows at most one check per enable.
func (s *Shell) runAutoUpdates(ctx context.Context) {
	enabled := s.updatesCheckEnabled()

	var (
		timer  *time.Timer
		timerC <-chan time.Time
		cancel context.CancelFunc
		done   chan struct{}
	)
	stopTimer := func() {
		if timer == nil {
			return
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer, timerC = nil, nil
	}
	start := func() {
		if cancel != nil {
			return
		}
		checkCtx, stop := context.WithCancel(ctx)
		cancel = stop
		finished := make(chan struct{})
		done = finished
		go func() {
			defer close(finished)
			s.checkUpdates(checkCtx, false)
			stop()
		}()
	}
	if enabled {
		timer = time.NewTimer(s.autoUpdateDelay())
		timerC = timer.C
	}
	for {
		select {
		case <-ctx.Done():
			stopTimer()
			if cancel != nil {
				cancel()
			}
			return
		case <-s.autoUpdateSync:
			want := s.updatesCheckEnabled()
			if want == enabled {
				continue
			}
			enabled = want
			if !enabled {
				stopTimer()
				if cancel != nil {
					cancel()
				}
				continue
			}
			// Turning the toggle on checks immediately, not after the
			// startup delay.
			stopTimer()
			start()
		case <-timerC:
			timer, timerC = nil, nil
			if enabled {
				start()
			}
		case <-done:
			cancel, done = nil, nil
		}
	}
}

// installUpdate downloads and launches the installer for the checked release.
// The Client keeps running until the installer replaces it.
func (s *Shell) installUpdate() {
	if s.opts.Updater == nil {
		s.pushMessages(nil, []string{s.catalog().T("error.update.unavailable")})
		return
	}
	s.mu.Lock()
	if s.updateBusy {
		s.mu.Unlock()
		return
	}
	info := s.updateInfo
	if !info.Available || !info.Installable {
		s.mu.Unlock()
		s.pushMessages(nil, []string{s.catalog().T("error.update.none")})
		return
	}
	s.updateBusy = true
	s.updateInstalling = true
	s.updateError = ""
	s.mu.Unlock()
	s.pushSettings()

	go func() {
		installCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		_, err := s.opts.Updater.Install(installCtx, info)
		s.mu.Lock()
		s.updateBusy = false
		s.updateInstalling = false
		if err != nil {
			s.updateError = err.Error()
		} else {
			s.updateLaunched = true
		}
		s.mu.Unlock()
		if err != nil {
			s.log.Warning("update: install failed: %v", err)
			s.pushSettings()
			s.pushMessages(nil, []string{s.catalog().T("error.update.install", err.Error())})
			return
		}
		s.pushSettings()
		s.pushMessages([]string{s.catalog().T("notice.update.launched")}, nil)
	}()
}
