package services

import (
	"context"
	"fmt"
	"time"

	"catdb/internal/storage"
	"catdb/internal/updater"
	"catdb/wailsbridge"
)

// settingsKeySkippedVersion is the key under which a user-skipped release
// version is stored in app_settings.
const settingsKeySkippedVersion = "updater.skipped_version"

// settingsKeyLastCheckDate stores the date (YYYY-MM-DD) of the last
// successful check so we skip re-checking on the same calendar day.
const settingsKeyLastCheckDate = "updater.last_check_date"

// Update progress event sent over the bridge while a download is in flight.
// Phase is one of: "downloading", "installing", "ready", "error".
const eventUpdateProgress = "update:progress"

// UpdateService exposes the auto-updater to the front-end:
//
//	CheckForUpdate     — talk to GitHub Releases, compare to currentVersion
//	GetSkippedVersion  — what version the user has chosen to ignore
//	SkipVersion        — persist a skip decision
//	StartInstall       — download the matched asset and hand off to the
//	                     platform installer; the app quits when control
//	                     returns from the installer spawn.
//
// The repo is hard-coded to updater.DefaultRepo ("peisp/catdb") — there is
// no per-user override yet.
type UpdateService struct {
	store *storage.Store
	repo  string
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

// GetLastCheckDate returns the date (YYYY-MM-DD) of the last successful
// update check, or "" if none.
func (s *UpdateService) GetLastCheckDate(ctx context.Context) (string, error) {
	return s.store.GetSetting(ctx, settingsKeyLastCheckDate)
}

// SetLastCheckDate persists the date string after a successful check.
func (s *UpdateService) SetLastCheckDate(ctx context.Context, date string) error {
	return s.store.SetSetting(ctx, settingsKeyLastCheckDate, date)
}

// StartInstall downloads the matched asset for currentVersion→latest and
// then hands off to the OS-specific installer. It emits update:progress
// events while running and finally quits the app so the swap can complete.
//
// Returns once the installer has been spawned (or an error before that). The
// caller (front-end) should already have shown a confirm dialog by this point.
func (s *UpdateService) StartInstall(ctx context.Context, currentVersion string) error {
	emit := func(phase, message string, extra map[string]any) {
		payload := map[string]any{
			"phase":   phase,
			"message": message,
		}
		for k, v := range extra {
			payload[k] = v
		}
		wailsbridge.Emit(eventUpdateProgress, payload)
	}

	rel, err := updater.FetchLatest(ctx, s.repo)
	if err != nil {
		emit("error", "无法获取最新发布信息", map[string]any{"error": err.Error()})
		return err
	}
	if updater.CompareVersions(rel.Version(), currentVersion) <= 0 {
		emit("error", "已是最新版本", nil)
		return fmt.Errorf("updater: no newer release (current=%s latest=%s)", currentVersion, rel.Version())
	}
	asset, err := updater.PickAsset(rel)
	if err != nil {
		emit("error", "未找到适配当前系统的安装包", map[string]any{"error": err.Error()})
		return err
	}

	emit("downloading", fmt.Sprintf("正在下载 %s", asset.Name), map[string]any{
		"downloaded": int64(0),
		"total":      asset.Size,
	})

	path, err := updater.DownloadAsset(ctx, asset, func(downloaded, total int64) {
		emit("downloading", "", map[string]any{
			"downloaded": downloaded,
			"total":      total,
		})
	})
	if err != nil {
		emit("error", "下载失败", map[string]any{"error": err.Error()})
		return err
	}

	emit("installing", "正在准备安装", map[string]any{"path": path})

	if err := updater.Install(ctx, path); err != nil {
		emit("error", "启动安装器失败", map[string]any{"error": err.Error()})
		return err
	}

	emit("ready", "应用即将退出以完成更新", nil)

	// Give the front-end a beat to render the "ready" state before we yank
	// the process out from under it. The installer is already detached.
	go func() {
		time.Sleep(800 * time.Millisecond)
		wailsbridge.Quit()
	}()
	return nil
}
