package shell

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/components"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/config"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/runtime"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/servers"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/trust"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/update"
)

// Updater checks GitHub Releases, stages a verified update set, installs the
// staged copy through the generated helper, and reuses the staged copy after a
// restart. *update.Service implements it.
type Updater interface {
	Check(ctx context.Context) (update.Info, error)
	Download(ctx context.Context, info update.Info, progress func(update.Progress)) (update.Stage, error)
	Staged(tag string) (update.Stage, bool)
	LatestStaged() (update.Stage, bool)
	VerifyStaged(ctx context.Context, stage update.Stage) error
	ClearStaged(tag string) error
	Install(ctx context.Context, req update.InstallRequest) error
	Result() (update.Result, bool)
	ClearResult() error
}

// UpdateLabels are the translated strings of the updates screen and Tray item.
type UpdateLabels struct {
	Title               string `json:"title"`
	AutoCheck           string `json:"autoCheck"`
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
	InstallingMessage   string `json:"installingMessage"`
	InstalledMessage    string `json:"installedMessage"`
	PartialMessage      string `json:"partialMessage"`
	FailedMessage       string `json:"failedMessage"`
	TimeoutMessage      string `json:"timeoutMessage"`
	PendingMessage      string `json:"pendingMessage"`
	ServerUpdated       string `json:"serverUpdated"`
	ServerRunning       string `json:"serverRunning"`
	ServerStopped       string `json:"serverStopped"`
	Details             string `json:"details"`
	LogPath             string `json:"logPath"`
	LogUnavailable      string `json:"logUnavailable"`
	Dismiss             string `json:"dismiss"`
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
	// Installing reports that the Client is finishing the update install and
	// is about to exit for the helper.
	Installing bool `json:"installing"`
	// Available reports a newer release.
	Available bool `json:"available"`
	// ResultStatus is the persisted outcome after the Client restarted:
	// success, partial, error, timeout, or pending.
	ResultStatus string `json:"resultStatus,omitempty"`
	// ResultTag is the release the result belongs to.
	ResultTag string `json:"resultTag,omitempty"`
	// ResultServerUpdated reports whether the local Server was updated.
	ResultServerUpdated bool `json:"resultServerUpdated,omitempty"`
	// ResultServerWasRunning reports whether the local Server ran before the
	// update and is expected to run again.
	ResultServerWasRunning bool `json:"resultServerWasRunning,omitempty"`
	// ResultServerStarted reports that the local Server was updated and actually
	// started again after the update.
	ResultServerStarted bool `json:"resultServerStarted,omitempty"`
	// ResultMessage is the translated success, partial, or failure text.
	ResultMessage string `json:"resultMessage,omitempty"`
	// ResultFailed marks a persisted error, timeout, partial, or pending
	// outcome that offers details and a retry.
	ResultFailed bool `json:"resultFailed,omitempty"`
	// ResultDetail is the tail of the update log shown under a failure.
	ResultDetail string `json:"resultDetail,omitempty"`
	// ResultLogPath is the helper log shown with a failure.
	ResultLogPath string `json:"resultLogPath,omitempty"`
	// Error is the last check or download failure, or "".
	Error string `json:"error,omitempty"`
}

// SnapshotUpdate is the compact update state the Tray renders.
type SnapshotUpdate struct {
	Available    bool   `json:"available"`
	Latest       string `json:"latest,omitempty"`
	Busy         bool   `json:"busy,omitempty"`
	Downloading  bool   `json:"downloading,omitempty"`
	Installing   bool   `json:"installing,omitempty"`
	Ready        bool   `json:"ready,omitempty"`
	Received     int64  `json:"received,omitempty"`
	Total        int64  `json:"total,omitempty"`
	File         string `json:"file,omitempty"`
	ResultStatus string `json:"resultStatus,omitempty"`
	ResultTag    string `json:"resultTag,omitempty"`
	Error        string `json:"error,omitempty"`
}

func (s *Shell) updateLabels() UpdateLabels {
	catalog := s.catalog()
	return UpdateLabels{
		Title:               catalog.T("update.title"),
		AutoCheck:           catalog.T("update.autoCheck"),
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
		InstallingMessage:   catalog.T("update.installing"),
		InstalledMessage:    catalog.T("update.installed"),
		PartialMessage:      catalog.T("update.partial"),
		FailedMessage:       catalog.T("update.failed"),
		TimeoutMessage:      catalog.T("update.timeout"),
		PendingMessage:      catalog.T("update.pending"),
		ServerUpdated:       catalog.T("update.serverUpdated"),
		ServerRunning:       catalog.T("update.serverRunning"),
		ServerStopped:       catalog.T("update.serverStopped"),
		Details:             catalog.T("update.details"),
		LogPath:             catalog.T("update.logPath"),
		LogUnavailable:      catalog.T("update.logUnavailable"),
		Dismiss:             catalog.T("update.dismiss"),
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
	installing := s.updateInstalling
	ready := s.updateReady
	progress := s.updateProgress
	updateErr := s.updateError
	result, hasResult := s.updateResult, s.updateResultSet
	resultDetail := s.updateResultDetail
	s.mu.Unlock()
	view := UpdateView{
		UpdateLabels:   s.updateLabels(),
		CurrentVersion: s.opts.Version,
		LatestVersion:  info.Latest,
		ReleaseURL:     info.URL,
		CheckEnabled:   s.updatesCheckEnabled(),
		Checked:        checked,
		Installable:    info.Installable,
		Busy:           busy,
		Downloading:    downloadIng,
		Installing:     installing,
		Ready:          ready,
		ReceivedBytes:  progress.Received,
		TotalBytes:     progress.Total,
		CurrentFile:    progress.File,
		Cancelable:     downloadIng,
		Available:      info.Available,
		Error:          updateErr,
	}
	if hasResult {
		view.ResultStatus = result.Status
		view.ResultTag = result.Tag
		view.ResultServerUpdated = result.ServerUpdated
		view.ResultServerWasRunning = result.ServerWasRunning
		view.ResultServerStarted = s.updateResultServerStarted(result)
		view.ResultMessage = s.updateResultMessage(result, view.ResultServerStarted)
		view.ResultFailed = updateResultFailed(result.Status)
		view.ResultDetail = resultDetail
		view.ResultLogPath = result.LogPath
	}
	return view
}

// resultFailure reports whether the persisted outcome is a failure, either
// because the view marks it or because its status is not success. The second
// check keeps a view built by hand (tests, older payloads) rendering details
// and retry.
func (v UpdateView) resultFailure() bool {
	return v.ResultFailed || (v.ResultStatus != "" && v.ResultStatus != update.StatusSuccess)
}

// updateResultFailed reports whether a persisted outcome needs details and a
// retry. Success is the only outcome that does not.
func updateResultFailed(status string) bool {
	return status != "" && status != update.StatusSuccess
}

const (
	// updateDetailBytes bounds the log read before the last lines are kept.
	updateDetailBytes = 32 << 10
	// updateDetailLines is how much of the helper log the card shows.
	updateDetailLines = 50
)

// updateLogTail returns the last few lines of the helper log. A missing or
// unreadable log is empty, not an error: the card still shows the path.
func updateLogTail(path string) string {
	if path == "" {
		return ""
	}
	text, _ := tailLogFile(path, updateDetailBytes)
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	if len(lines) > updateDetailLines {
		lines = lines[len(lines)-updateDetailLines:]
	}
	return strings.Join(lines, "\n")
}

// UpdatePercent clamps an aggregate download's received bytes to a whole
// percentage in [0,100]. An unknown or empty total reports 0.
func UpdatePercent(received, total int64) int64 {
	if total <= 0 {
		return 0
	}
	percent := received * 100 / total
	if percent < 0 {
		return 0
	}
	if percent > 100 {
		return 100
	}
	return percent
}

// updateResultServerStarted reports whether the relaunched Client actually
// started the Local Server again after the update. It stays false when the
// helper was updated but the launch failed, when the Server was stopped before
// the update, and when the installed Autostart mode is Client-only.
func (s *Shell) updateResultServerStarted(result update.Result) bool {
	if !result.ServerWasRunning || !result.ServerUpdated || s.opts.ServerLaunchError != nil {
		return false
	}
	if s.opts.Startup != nil {
		state := s.opts.Startup.Status()
		if !state.Installed.Server || state.Mode == components.ModeClientOnly {
			return false
		}
	}
	return true
}

// updateResultMessage builds the one-line outcome shown in the Updates card.
func (s *Shell) updateResultMessage(result update.Result, serverStarted bool) string {
	catalog := s.catalog()
	tag := strings.TrimPrefix(strings.TrimPrefix(result.Tag, "v"), "V")
	switch result.Status {
	case update.StatusSuccess:
		message := catalog.T("update.installed", tag)
		if result.ServerUpdated {
			message += " " + catalog.T("update.serverUpdated")
			if serverStarted {
				message += " " + catalog.T("update.serverRunning")
			} else {
				message += " " + catalog.T("update.serverStopped")
			}
		}
		return message
	case update.StatusPartial:
		return catalog.T("update.partial", tag)
	case update.StatusTimeout:
		return catalog.T("update.timeout", tag)
	case update.StatusPending:
		return catalog.T("update.pending")
	default:
		return catalog.T("update.failed", tag)
	}
}

// snapshotUpdateLocked is the compact state the Tray renders. s.mu must be held.
func (s *Shell) snapshotUpdateLocked() SnapshotUpdate {
	snapshot := SnapshotUpdate{
		Available:   s.updateInfo.Available,
		Latest:      s.updateInfo.Latest,
		Busy:        s.updateBusy,
		Downloading: s.updateDownloading,
		Installing:  s.updateInstalling,
		Ready:       s.updateReady,
		Received:    s.updateProgress.Received,
		Total:       s.updateProgress.Total,
		File:        s.updateProgress.File,
		Error:       s.updateError,
	}
	if s.updateResultSet {
		snapshot.ResultStatus = s.updateResult.Status
		snapshot.ResultTag = s.updateResult.Tag
	}
	return snapshot
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
	if s.updateBusy || s.updateDownloading || s.updateInstalling {
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
		_, _ = s.downloadUpdate(ctx, info)
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

// updateAction runs "Update to X…": download the staged set when it is not
// ready, verify it again, stop the Local Server when it was running, let the
// current dictation finish, and start the helper. The Client only exits after
// the helper started successfully.
func (s *Shell) updateAction() {
	if s.opts.Updater == nil {
		s.pushMessages(nil, []string{s.catalog().T("error.update.unavailable")})
		return
	}
	s.mu.Lock()
	if s.updateBusy || s.updateDownloading || s.updateInstalling {
		s.mu.Unlock()
		return
	}
	info := s.updateInfo
	s.updateInstalling = true
	s.updateError = ""
	s.mu.Unlock()
	if !info.Available || !info.Installable {
		s.mu.Lock()
		s.updateInstalling = false
		s.mu.Unlock()
		s.pushMessages(nil, []string{s.catalog().T("error.update.none")})
		return
	}
	s.pushSettings()
	s.update(s.opts.Runtime.Status())
	go s.installUpdate(info)
}

// installUpdate is the update action's background worker.
func (s *Shell) installUpdate(info update.Info) {
	fail := func(err error) {
		s.mu.Lock()
		s.updateInstalling = false
		s.updateError = err.Error()
		s.mu.Unlock()
		s.log.Warning("update: install failed: %v", err)
		s.pushSettings()
		s.update(s.opts.Runtime.Status())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	s.mu.Lock()
	ready, stage := s.updateReady, s.updateStage
	s.mu.Unlock()
	if !ready || stage.Tag != info.Tag {
		staged, err := s.downloadUpdate(ctx, info)
		if err != nil {
			// A cancelled download is not an install failure: the card returns
			// to "available" without an error.
			if errors.Is(err, context.Canceled) {
				s.mu.Lock()
				s.updateInstalling = false
				s.updateError = ""
				s.mu.Unlock()
				s.pushSettings()
				s.update(s.opts.Runtime.Status())
				return
			}
			fail(err)
			return
		}
		stage = staged
	}
	if err := s.opts.Updater.VerifyStaged(ctx, stage); err != nil {
		// A corrupt set must not be retried. Drop it so the next attempt checks
		// and downloads a fresh set instead of reusing the same files.
		if clearErr := s.opts.Updater.ClearStaged(stage.Tag); clearErr != nil {
			s.log.Warning("update: clear the corrupt staged set: %v", clearErr)
		}
		s.mu.Lock()
		s.updateReady = false
		s.updateStage = update.Stage{}
		s.updateProgress = update.Progress{}
		s.mu.Unlock()
		fail(err)
		return
	}

	installed := s.installedComponents(stage)
	serverWasRunning := s.stopLocalServerForUpdate(ctx)
	s.waitForUpdateIdle(ctx)

	err := s.opts.Updater.Install(ctx, update.InstallRequest{
		Stage:            stage,
		Components:       installed,
		ServerWasRunning: serverWasRunning,
		ClientPID:        os.Getpid(),
	})
	if err != nil {
		// The helper failed to start, so this Client stays alive. Put the Local
		// Server back if this update stopped it, rather than leaving it offline.
		if serverWasRunning {
			s.restoreLocalServerForUpdate(ctx)
		}
		fail(err)
		return
	}

	s.mu.Lock()
	s.updateInstalling = false
	s.updateReady = false
	s.updateStage = update.Stage{}
	s.updateProgress = update.Progress{}
	s.mu.Unlock()
	s.pushSettings()
	s.update(s.opts.Runtime.Status())
	// The helper is alive and will wait for this process to exit. The shell
	// stops the runtime and the CLI releases the single-instance lock.
	s.requestUpdateInstall()
}

// installedComponents is the set the update must cover. The active Server
// profile has no effect.
func (s *Shell) installedComponents(stage update.Stage) components.Set {
	if s.opts.Startup != nil {
		if installed := s.opts.Startup.Status().Installed; installed.Any() {
			installed.Client = true
			return installed
		}
	}
	if stage.Components.Any() {
		set := stage.Components
		set.Client = true
		return set
	}
	return components.Set{Client: true}
}

// localServerProfile finds the Local Server profile, preferring the stable
// local id and falling back to an auto-pairing loopback profile. It never uses
// the active profile: a Remote Server may be active.
func (s *Shell) localServerProfile() (servers.Profile, bool) {
	if s.opts.Servers == nil {
		return servers.Profile{}, false
	}
	profiles := s.opts.Servers.View().Profiles
	for _, profile := range profiles {
		if profile.ID == config.LocalServerID {
			return profile, true
		}
	}
	for _, profile := range profiles {
		if profile.AutoPair {
			return profile, true
		}
	}
	return servers.Profile{}, false
}

// stopLocalServerForUpdate stops the Local Server before the installer replaces
// its files, even when a Remote Server profile is active. It reports whether
// the Server was running, so the relaunched Client can start it again.
func (s *Shell) stopLocalServerForUpdate(ctx context.Context) bool {
	profile, ok := s.localServerProfile()
	if !ok {
		return false
	}
	status := s.opts.Servers.CheckHealth(ctx, profile.ID)
	running := status.State == trust.StateOnline ||
		status.State == trust.StateUnauthenticated ||
		status.State == trust.StateVersionMismatch
	if running && serverSoftStopped(status) {
		running = false
	}
	if !running {
		return false
	}
	if _, err := s.opts.Servers.Stop(ctx, profile.ID); err != nil {
		s.log.Warning("update: stop the Local Server: %v", err)
	}
	return true
}

// restoreLocalServerForUpdate puts the Local Server back after an update could
// not hand over to the helper. It is best effort: the card still reports the
// original failure.
func (s *Shell) restoreLocalServerForUpdate(ctx context.Context) {
	profile, ok := s.localServerProfile()
	if !ok {
		return
	}
	if _, err := s.opts.Servers.Start(ctx, profile.ID); err != nil {
		s.log.Warning("update: restart the Local Server after a failed install: %v", err)
	}
}

// waitForUpdateIdle lets the current dictation finish before the Client exits.
// It never aborts an utterance; on timeout it logs and continues so an update
// cannot hang forever.
func (s *Shell) waitForUpdateIdle(ctx context.Context) {
	timeout := s.opts.UpdateIdleTimeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	interval := s.opts.UpdateIdlePoll
	if interval <= 0 {
		interval = 200 * time.Millisecond
	}
	deadline := time.Now().Add(timeout)
	timer := time.NewTimer(interval)
	defer timer.Stop()
	for {
		if s.opts.Runtime.Status().Phase == runtime.PhaseIdle {
			return
		}
		if !time.Now().Before(deadline) {
			s.log.Warning("update: the current dictation did not finish within %s; continuing", timeout)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			timer.Reset(interval)
		}
	}
}

// dismissUpdate clears the persisted result after the user read it.
func (s *Shell) dismissUpdate() {
	if s.opts.Updater == nil {
		return
	}
	if err := s.opts.Updater.ClearResult(); err != nil {
		s.log.Warning("update: clear the result: %v", err)
	}
	s.mu.Lock()
	s.updateResult = update.Result{}
	s.updateResultSet = false
	s.updateResultDetail = ""
	s.mu.Unlock()
	s.pushSettings()
	s.update(s.opts.Runtime.Status())
}

// loadUpdateResult reads the helper's persisted outcome on startup. A pending
// marker means the helper never finished; it is shown as an error.
func (s *Shell) loadUpdateResult() {
	if s.opts.Updater == nil {
		return
	}
	result, ok := s.opts.Updater.Result()
	if !ok {
		return
	}
	detail := ""
	if updateResultFailed(result.Status) {
		detail = updateLogTail(result.LogPath)
	}
	s.mu.Lock()
	s.updateResult = result
	s.updateResultSet = true
	s.updateResultDetail = detail
	if result.Status == update.StatusSuccess {
		s.updateReady = false
		s.updateStage = update.Stage{}
	}
	s.mu.Unlock()
	if result.Status == update.StatusSuccess {
		if err := s.opts.Updater.ClearStaged(result.Tag); err != nil {
			s.log.Debug("update: clear the staged set: %v", err)
		}
	}
}

// loadLatestStaged makes a complete staged set visible after a Client restart,
// before any release check. It is skipped when a successful result is already
// on display, and a later check for a different tag replaces or clears it.
func (s *Shell) loadLatestStaged() {
	if s.opts.Updater == nil {
		return
	}
	s.mu.Lock()
	success := s.updateResultSet && s.updateResult.Status == update.StatusSuccess
	s.mu.Unlock()
	if success {
		return
	}
	stage, ok := s.opts.Updater.LatestStaged()
	if !ok {
		return
	}
	latest := strings.TrimPrefix(strings.TrimPrefix(stage.Tag, "v"), "V")
	s.mu.Lock()
	s.updateStage = stage
	s.updateReady = true
	s.updateInfo = update.Info{
		Current:     s.opts.Version,
		Latest:      latest,
		Tag:         stage.Tag,
		Available:   true,
		Installable: true,
	}
	s.mu.Unlock()
}

// updateResultVisible reports whether the startup window must open so the user
// sees an update outcome.
func (s *Shell) updateResultVisible() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.updateResultSet
}

// requestUpdateInstall ends the shell through the update-specific exit path.
func (s *Shell) requestUpdateInstall() {
	s.installOnce.Do(func() { close(s.installCh) })
}

// retryUpdate retries a failed download or a persisted failure. A persisted
// failure is cleared once the retry starts, then the Client checks again and
// downloads and installs in the same action. A download error retries the
// current update action.
func (s *Shell) retryUpdate() {
	if s.opts.Updater == nil {
		s.pushMessages(nil, []string{s.catalog().T("error.update.unavailable")})
		return
	}
	s.mu.Lock()
	if s.updateBusy || s.updateDownloading || s.updateInstalling || s.updateRetrying {
		s.mu.Unlock()
		return
	}
	hasResult := s.updateResultSet
	s.updateError = ""
	if !hasResult {
		s.mu.Unlock()
		s.updateAction()
		return
	}
	s.updateRetrying = true
	s.mu.Unlock()

	// The user acknowledged the old outcome by starting the retry. Clearing it
	// here also stops a stale failure from lingering beside the new attempt.
	if err := s.opts.Updater.ClearResult(); err != nil {
		s.log.Warning("update: clear the result: %v", err)
	}
	s.mu.Lock()
	s.updateResult = update.Result{}
	s.updateResultSet = false
	s.updateResultDetail = ""
	s.mu.Unlock()
	s.pushSettings()
	s.update(s.opts.Runtime.Status())

	go s.retryUpdateFromCheck()
}

// retryUpdateFromCheck re-runs the manual release check behind a retry of a
// persisted failure. A fresh available release proceeds to the normal
// download-and-install action; an up-to-date result keeps the normal notice.
func (s *Shell) retryUpdateFromCheck() {
	defer func() {
		s.mu.Lock()
		s.updateRetrying = false
		s.mu.Unlock()
		s.pushSettings()
		s.update(s.opts.Runtime.Status())
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	s.checkUpdates(ctx, true)

	s.mu.Lock()
	info := s.updateInfo
	checked := s.updateChecked
	updateErr := s.updateError
	s.mu.Unlock()
	if updateErr != "" || !checked || !info.Available || !info.Installable {
		return
	}
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

// downloadUpdate stages the release set and returns it for an immediate
// install. Automatic downloads receive the scheduler's context so turning the
// toggle off cancels them; a manual download receives an independent context
// and is unaffected by the toggle.
func (s *Shell) downloadUpdate(ctx context.Context, info update.Info) (update.Stage, error) {
	if s.opts.Updater == nil {
		return update.Stage{}, errors.New("update checking is not available in this build")
	}
	downloadCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	s.mu.Lock()
	if s.updateBusy || s.updateDownloading {
		s.mu.Unlock()
		return update.Stage{}, errors.New("another update download is already in progress")
	}
	s.updateDownloading = true
	s.updateReady = false
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
	if cancelled {
		return update.Stage{}, downloadCtx.Err()
	}
	if err != nil {
		return update.Stage{}, err
	}
	return stage, nil
}
