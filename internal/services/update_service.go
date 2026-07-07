package services

import (
	"context"
	"fmt"
	"sync"
	"time"

	"catdb/internal/storage"
	"catdb/internal/updater"
	"catdb/wailsbridge"
)

// settingsKeySkippedVersion is the key under which a user-skipped release
// version is stored in app_settings.
const settingsKeySkippedVersion = "updater.skipped_version"

// settingsKeyLastCheckDate stores the timestamp (ISO 8601) of the last
// successful check; the frontend gates automatic re-checks on a minimum
// interval (older builds stored a plain YYYY-MM-DD, still parseable).
const settingsKeyLastCheckDate = "updater.last_check_date"

// Update progress event sent over the bridge while a download is in flight.
// Phase is one of: "downloading", "downloaded", "installing", "ready", "error".
const eventUpdateProgress = "update:progress"

// UpdateService exposes the auto-updater to the front-end:
//
//	CheckForUpdate     — talk to GitHub Releases, compare to currentVersion
//	GetSkippedVersion  — what version the user has chosen to ignore
//	SkipVersion        — persist a skip decision
//	DownloadUpdate     — download the matched asset and stage it locally;
//	                     the install waits for the user's explicit go-ahead
//	RestartAndInstall  — hand the staged asset to the platform installer
//	                     (silent) and quit so the swap can complete
//
// The repo is hard-coded to updater.DefaultRepo ("peisp/catdb") — there is
// no per-user override yet.
type UpdateService struct {
	store *storage.Store
	repo  string

	// downloadedPath is the staged installer/DMG from the last successful
	// DownloadUpdate — consumed by RestartAndInstall.
	mu             sync.Mutex
	downloadedPath string
}

// NewUpdateService constructs the service. Pass an empty repo to use the default.
func NewUpdateService(store *storage.Store, repo string) *UpdateService {
	if repo == "" {
		repo = updater.DefaultRepo
	}
	return &UpdateService{store: store, repo: repo}
}

// ServiceName satisfies Wails v3's Service interface.
func (s *UpdateService) ServiceName() string { return "UpdateService" }

// UpdateCheckResult is what the front-end sees after a CheckForUpdate.
type UpdateCheckResult struct {
	// Available is true when latest > current AND latest != skipped.
	Available bool `json:"available"`
	// LatestVersion is the trimmed tag (e.g. "1.4.2").
	LatestVersion string `json:"latestVersion"`
	// CurrentVersion echoes what the front-end passed in.
	CurrentVersion string `json:"currentVersion"`
	// ReleaseNotes is the GitHub Release body — markdown.
	ReleaseNotes string `json:"releaseNotes"`
	// ReleaseURL points at the GitHub release page.
	ReleaseURL string `json:"releaseUrl"`
	// PublishedAt — RFC3339; "" if missing.
	PublishedAt string `json:"publishedAt"`
	// AssetName is the matched installer for this OS/arch; "" if none.
	AssetName string `json:"assetName"`
	// HasAsset is true when PickAsset succeeded — used to gate the "install" CTA.
	HasAsset bool `json:"hasAsset"`
	// Skipped is true when LatestVersion equals the user-skipped version.
	Skipped bool `json:"skipped"`
}

// CheckForUpdate compares the latest GitHub release to currentVersion. Returns
// a fully populated result even when no update is available (so the UI can
// render "you're up to date" instead of throwing).
func (s *UpdateService) CheckForUpdate(ctx context.Context, currentVersion string) (UpdateCheckResult, error) {
	// ponytail: dev builds skip the GitHub API call entirely.
	if currentVersion == "dev" {
		return UpdateCheckResult{CurrentVersion: "dev"}, nil
	}
	rel, err := updater.FetchLatest(ctx, s.repo)
	if err != nil {
		return UpdateCheckResult{}, err
	}

	skipped, _ := s.store.GetSetting(ctx, settingsKeySkippedVersion)

	res := UpdateCheckResult{
		CurrentVersion: currentVersion,
		LatestVersion:  rel.Version(),
		ReleaseNotes:   rel.Body,
		ReleaseURL:     rel.HTMLURL,
	}
	if !rel.PublishedAt.IsZero() {
		res.PublishedAt = rel.PublishedAt.Format(time.RFC3339)
	}
	if asset, err := updater.PickAsset(rel); err == nil {
		res.AssetName = asset.Name
		res.HasAsset = true
	}
	if updater.CompareVersions(res.LatestVersion, currentVersion) > 0 {
		res.Available = true
	}
	if skipped != "" && updater.CompareVersions(res.LatestVersion, skipped) == 0 {
		res.Skipped = true
		res.Available = false
	}
	return res, nil
}

// GetSkippedVersion returns the version the user previously chose to skip,
// or "" if none.
func (s *UpdateService) GetSkippedVersion(ctx context.Context) (string, error) {
	return s.store.GetSetting(ctx, settingsKeySkippedVersion)
}

// SkipVersion persists version as the "ignore this release" marker.
func (s *UpdateService) SkipVersion(ctx context.Context, version string) error {
	return s.store.SetSetting(ctx, settingsKeySkippedVersion, version)
}

// GetLastCheckDate returns the timestamp (ISO 8601) of the last successful
// update check, or "" if none.
func (s *UpdateService) GetLastCheckDate(ctx context.Context) (string, error) {
	return s.store.GetSetting(ctx, settingsKeyLastCheckDate)
}

// SetLastCheckDate persists the timestamp string after a successful check.
func (s *UpdateService) SetLastCheckDate(ctx context.Context, date string) error {
	return s.store.SetSetting(ctx, settingsKeyLastCheckDate, date)
}

// emitProgress sends an update:progress event. `code` is a stable,
// locale-independent slug (e.g. "fetch-failed"); the front-end maps it to a
// localized message (stores/updates → error.update.* / update.*). The raw
// technical detail, when present, rides along under extra["error"].
func (s *UpdateService) emitProgress(phase, code string, extra map[string]any) {
	payload := map[string]any{
		"phase": phase,
		"code":  code,
	}
	for k, v := range extra {
		payload[k] = v
	}
	wailsbridge.Emit(eventUpdateProgress, payload)
}

// DownloadUpdate downloads the matched asset for currentVersion→latest and
// stages it locally. It emits update:progress events while running and ends
// in phase "downloaded" — installing/restarting waits for the user to call
// RestartAndInstall explicitly.
func (s *UpdateService) DownloadUpdate(ctx context.Context, currentVersion string) error {
	rel, err := updater.FetchLatest(ctx, s.repo)
	if err != nil {
		s.emitProgress("error", "fetch-failed", map[string]any{"error": err.Error()})
		return err
	}
	if updater.CompareVersions(rel.Version(), currentVersion) <= 0 {
		s.emitProgress("error", "up-to-date", nil)
		return fmt.Errorf("updater: no newer release (current=%s latest=%s)", currentVersion, rel.Version())
	}
	asset, err := updater.PickAsset(rel)
	if err != nil {
		s.emitProgress("error", "no-asset", map[string]any{"error": err.Error()})
		return err
	}

	s.emitProgress("downloading", "", map[string]any{
		"downloaded": int64(0),
		"total":      asset.Size,
	})

	path, err := updater.DownloadAsset(ctx, asset, func(downloaded, total int64) {
		s.emitProgress("downloading", "", map[string]any{
			"downloaded": downloaded,
			"total":      total,
		})
	})
	if err != nil {
		s.emitProgress("error", "download-failed", map[string]any{"error": err.Error()})
		return err
	}

	s.mu.Lock()
	s.downloadedPath = path
	s.mu.Unlock()

	s.emitProgress("downloaded", "", map[string]any{"path": path})
	return nil
}

// RestartAndInstall hands the staged asset from the last DownloadUpdate to
// the OS-specific installer (silent — no installer UI) and then quits the
// app so the swap can complete; the installer relaunches the app afterwards.
func (s *UpdateService) RestartAndInstall(ctx context.Context) error {
	s.mu.Lock()
	path := s.downloadedPath
	s.mu.Unlock()
	if path == "" {
		s.emitProgress("error", "no-download", nil)
		return fmt.Errorf("updater: no staged download — call DownloadUpdate first")
	}

	s.emitProgress("installing", "", map[string]any{"path": path})

	if err := updater.Install(ctx, path); err != nil {
		s.emitProgress("error", "install-failed", map[string]any{"error": err.Error()})
		return err
	}

	s.emitProgress("ready", "", nil)

	// Give the front-end a beat to render the "ready" state before we yank
	// the process out from under it. The installer is already detached.
	go func() {
		time.Sleep(800 * time.Millisecond)
		wailsbridge.Quit()
	}()
	return nil
}
