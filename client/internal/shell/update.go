package shell

import (
	"context"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/update"
)

// Updater checks GitHub Releases, stages a verified update set, and reuses the
// staged copy after a restart. *update.Service implements it.
type Updater interface {
	Check(ctx context.Context) (update.Info, error)
	Download(ctx context.Context, info update.Info, progress func(update.Progress)) (update.Stage, error)
	Staged(tag string) (update.Stage, bool)
	VerifyStaged(ctx context.Context, stage update.Stage) error
	ClearStaged(tag string) error
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
	ReadyMessage        string `json:"readyMessage"`
	DownloadProgress    string `json:"downloadProgress"`
	DownloadingFile     string `json:"downloadingFile"`
	Cancel              string `json:"cancel"`
	Retry               string `json:"retry"`
	UpToDateMessage     string `json:"upToDate"`
	NotInstalledMessage string `json:"notInstalled"`
	Checking            string `json:"checking"`
	DownloadingMessage  string `json:"downloadingMessage"`
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
	// Busy reports an in-flight release check.
	Busy bool `json:"busy"`
	// Downloading reports an in-flight staged download.
	Downloading bool `json:"downloading"`
	// Ready reports a complete, verified set waiting for installation.
	Ready bool `json:"ready"`
	// ReceivedBytes and TotalBytes are the aggregate download progress; Total
	// is 0 when the size is unknown.
	ReceivedBytes int64 `json:"receivedBytes"`
	TotalBytes    int64 `json:"totalBytes"`
	// CurrentFile is the artifact being downloaded now, or "".
	CurrentFile string `json:"currentFile,omitempty"`
	// Cancelable reports that the download can be cancelled.
	Cancelable bool `json:"cancelable"`
	// Available reports a newer release.
	Available bool `json:"available"`
	// Error is the last check or download failure, or "".
	Error string `json:"error,omitempty"`
}

// SnapshotUpdate is the compact update state the Tray renders.
type SnapshotUpdate struct {
	Available   bool   `json:"available"`
	Latest      string `json:"latest,omitempty"`
	Busy        bool   `json:"busy,omitempty"`
	Downloading bool   `json:"downloading,omitempty"`
	Ready       bool   `json:"ready,omitempty"`
	Received    int64  `json:"received,omitempty"`
	Total       int64  `json:"total,omitempty"`
	File        string `json:"file,omitempty"`
	Error       string `json:"error,omitempty"`
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
		ReadyMessage:        catalog.T("update.ready"),
		DownloadProgress:    catalog.T("update.progress"),
		DownloadingFile:     catalog.T("update.downloadingFile"),
		Cancel:              catalog.T("update.cancel"),
		Retry:               catalog.T("update.retry"),
		UpToDateMessage:     catalog.T("update.upToDate"),
		NotInstalledMessage: catalog.T("update.notInstalled"),
		Checking:            catalog.T("update.checking"),
		DownloadingMessage:  catalog.T("update.downloading"),
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
	info := s.updateInfo
	checked := s.updateChecked
	busy := s.updateBusy
	downloadIng := s.updateDownloading
	ready := s.updateReady
	progress := s.updateProgress
	updateErr := s.updateError
	s.mu.Unlock()
	return UpdateView{
		UpdateLabels:   s.updateLabels(),
		CurrentVersion: s.opts.Version,
		LatestVersion:  info.Latest,
		ReleaseURL:     info.URL,
		CheckEnabled:   s.updatesCheckEnabled(),
		Checked:        checked,
		Installable:    info.Installable,
		Busy:           busy,
		Downloading:    downloadIng,
		Ready:          ready,
		ReceivedBytes:  progress.Received,
		TotalBytes:     progress.Total,
		CurrentFile:    progress.File,
		Cancelable:     downloadIng,
		Available:      info.Available,
		Error:          updateErr,
	}
}

// snapshotUpdateLocked is the compact state the Tray renders. s.mu must be held.
func (s *Shell) snapshotUpdateLocked() SnapshotUpdate {
	return SnapshotUpdate{
		Available:   s.updateInfo.Available,
		Latest:      s.updateInfo.Latest,
		Busy:        s.updateBusy,
		Downloading: s.updateDownloading,
		Ready:       s.updateReady,
		Received:    s.updateProgress.Received,
		Total:       s.updateProgress.Total,
		File:        s.updateProgress.File,
		Error:       s.updateError,
	}
}

// checkUpdates asks GitHub Releases for the latest version. A failure is
// logged; a manual check also raises the translated message. A successful
// automatic check also starts the background download. A manual check never
// downloads on its own.
func (s *Shell) checkUpdates(ctx context.Context, manual bool) {
	if s.opts.Updater == nil {
		if manual {
			s.pushMessages(nil, []string{s.catalog().T("error.update.unavailable")})
		}
		return
	}
	s.mu.Lock()
	if s.updateBusy || s.updateDownloading {
		s.mu.Unlock()
		return
	}
	s.updateBusy = true
	s.mu.Unlock()
	s.pushSettings()

	checkCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	info, err := s.opts.Updater.Check(checkCtx)
	// Disabling automatic checks cancels an in-flight check. That is not a
	// failure: it leaves the card and the message area as they were.
	cancelled := ctx.Err() != nil

	var (
		stage    update.Stage
		staged   bool
		staleTag string
	)
	if !cancelled && err == nil && info.Available && info.Installable {
		stage, staged = s.opts.Updater.Staged(info.Tag)
	}

	s.mu.Lock()
	s.updateBusy = false
	if !cancelled {
		s.updateChecked = true
		if err != nil {
			s.updateError = err.Error()
		} else {
			if s.updateStage.Tag != "" && s.updateStage.Tag != info.Tag {
				staleTag = s.updateStage.Tag
			}
			s.updateInfo = info
			s.updateError = ""
			s.updateReady = staged
			s.updateStage = stage
			s.updateProgress = update.Progress{}
		}
	}
	s.mu.Unlock()
	if staleTag != "" {
		_ = s.opts.Updater.ClearStaged(staleTag)
	}
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
	if !manual && info.Available && info.Installable && !staged {
		s.downloadUpdate(ctx, info, false)
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

// runAutoUpdates runs the automatic release check and the background download
// that follows it. It waits for the startup delay when the setting is on,
// reacts to the toggle being saved, and cancels an in-flight automatic check or
// download when the setting is turned off. There are no periodic checks.
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

// updateAction starts the manual staged download. The update action keeps its
// install-update name so the Settings page and Tray stay compatible; until
// #109 lands, a completed download only shows readiness and does not install.
func (s *Shell) updateAction() {
	if s.opts.Updater == nil {
		s.pushMessages(nil, []string{s.catalog().T("error.update.unavailable")})
		return
	}
	s.mu.Lock()
	if s.updateBusy || s.updateDownloading {
		s.mu.Unlock()
		return
	}
	info := s.updateInfo
	ready := s.updateReady
	s.mu.Unlock()
	if ready {
		// Installation is a later step; a click on a ready set leaves it ready.
		return
	}
	if !info.Available || !info.Installable {
		s.pushMessages(nil, []string{s.catalog().T("error.update.none")})
		return
	}
	go s.downloadUpdate(context.Background(), info, true)
}

// retryUpdate retries a failed background or manual download.
func (s *Shell) retryUpdate() {
	s.mu.Lock()
	s.updateError = ""
	s.mu.Unlock()
	s.updateAction()
}

// cancelUpdate stops an in-flight download. The service removes the partial
// files; the card returns to "available".
func (s *Shell) cancelUpdate() {
	s.mu.Lock()
	cancel := s.updateCancel
	downloading := s.updateDownloading
	s.mu.Unlock()
	if downloading && cancel != nil {
		cancel()
	}
}

// downloadUpdate stages the release set. Automatic downloads receive the
// scheduler's context so turning the toggle off cancels them; a manual download
// receives an independent context and is unaffected by the toggle.
func (s *Shell) downloadUpdate(ctx context.Context, info update.Info, manual bool) {
	if s.opts.Updater == nil {
		return
	}
	downloadCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	s.mu.Lock()
	if s.updateBusy || s.updateDownloading {
		s.mu.Unlock()
		return
	}
	s.updateDownloading = true
	s.updateReady = false
	s.updateAutoDownload = !manual
	s.updateCancel = cancel
	s.updateProgress = update.Progress{}
	s.updateError = ""
	s.mu.Unlock()
	s.pushSettings()
	s.update(s.opts.Runtime.Status())

	var lastPush time.Time
	report := func(progress update.Progress) {
		s.mu.Lock()
		s.updateProgress = progress
		now := time.Now()
		push := now.Sub(lastPush) >= 100*time.Millisecond
		if push {
			lastPush = now
		}
		s.mu.Unlock()
		if !push {
			return
		}
		s.pushSettings()
		s.update(s.opts.Runtime.Status())
	}

	stage, err := s.opts.Updater.Download(downloadCtx, info, report)
	cancelled := downloadCtx.Err() != nil

	s.mu.Lock()
	s.updateDownloading = false
	s.updateAutoDownload = false
	s.updateCancel = nil
	switch {
	case cancelled:
		s.updateReady = false
		s.updateStage = update.Stage{}
		s.updateProgress = update.Progress{}
		s.updateError = ""
	case err != nil:
		s.updateReady = false
		s.updateStage = update.Stage{}
		s.updateProgress = update.Progress{}
		s.updateError = err.Error()
	default:
		s.updateReady = true
		s.updateStage = stage
		s.updateError = ""
	}
	s.mu.Unlock()

	if err != nil && !cancelled {
		// A background download failure stays in the card and the log; it never
		// pops over the window. A manual failure uses the same card.
		s.log.Warning("update: download failed: %v", err)
	}
	// Always push the final state, even when the throttled progress callback
	// skipped the last sample.
	s.pushSettings()
	s.update(s.opts.Runtime.Status())
}
