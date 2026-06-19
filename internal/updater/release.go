// Package updater handles in-app updates: query GitHub Releases, compare
// versions, download the platform-matched asset, and hand off to the
// OS-specific installer.
//
// MVP scope: full auto-replace + restart (per the user requirements). We do
// NOT use Wails v3's built-in updater (the alpha series has none) — this is
// a hand-rolled implementation against the GitHub REST API.
package updater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// DefaultRepo is the GitHub repo we poll for releases.
const DefaultRepo = "peisp/catdb"

// ReleaseAsset is one downloadable file attached to a release.
type ReleaseAsset struct {
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
	URL         string `json:"browser_download_url"`
}

// Release is the trimmed subset of fields we need from the GitHub API.
type Release struct {
	TagName     string         `json:"tag_name"`
	Name        string         `json:"name"`
	Body        string         `json:"body"`
	Prerelease  bool           `json:"prerelease"`
	Draft       bool           `json:"draft"`
	PublishedAt time.Time      `json:"published_at"`
	HTMLURL     string         `json:"html_url"`
	Assets      []ReleaseAsset `json:"assets"`
}

// Version returns the tag with any leading "v" stripped.
func (r *Release) Version() string {
	return strings.TrimPrefix(r.TagName, "v")
}

// FetchLatest queries GitHub for the most recent NON-prerelease, NON-draft
// release of the given repo (owner/name). Returns ErrNoRelease if the repo
// has no published releases.
//
// The endpoint is unauthenticated — GitHub allows 60 req/hr per IP, which is
// fine for once-per-launch checks.
func FetchLatest(ctx context.Context, repo string) (*Release, error) {
	if repo == "" {
		repo = DefaultRepo
	}
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("updater: GitHub API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNoRelease
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("updater: GitHub API %d: %s", resp.StatusCode, string(body))
	}

	var r Release
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("updater: decode release: %w", err)
	}
	return &r, nil
}

// ErrNoRelease means the repo exists but has no published release.
var ErrNoRelease = errors.New("updater: no published release")

// ErrNoAsset means we matched a release but no asset for the current OS/arch.
var ErrNoAsset = errors.New("updater: no matching asset for this platform")

// CompareVersions returns -1 if a<b, 0 if equal, +1 if a>b. Both inputs are
// treated as dotted decimals ("1.2.3"); non-numeric / pre-release suffixes
// fall back to lexicographic compare on the remainder.
//
// This is intentionally permissive — our CI tags are pure X.Y.Z and we don't
// need full semver-spec ordering for MVP.
func CompareVersions(a, b string) int {
	a = strings.TrimPrefix(strings.TrimSpace(a), "v")
	b = strings.TrimPrefix(strings.TrimSpace(b), "v")
	if a == b {
		return 0
	}
	pa := strings.SplitN(a, "-", 2)
	pb := strings.SplitN(b, "-", 2)
	an := strings.Split(pa[0], ".")
	bn := strings.Split(pb[0], ".")
	for i := 0; i < len(an) || i < len(bn); i++ {
		var ai, bi int
		if i < len(an) {
			ai, _ = strconv.Atoi(an[i])
		}
		if i < len(bn) {
			bi, _ = strconv.Atoi(bn[i])
		}
		if ai != bi {
			if ai < bi {
				return -1
			}
			return 1
		}
	}
	// numeric parts equal — a release tag (no suffix) outranks a prerelease.
	sa, sb := "", ""
	if len(pa) > 1 {
		sa = pa[1]
	}
	if len(pb) > 1 {
		sb = pb[1]
	}
	if sa == sb {
		return 0
	}
	if sa == "" {
		return 1
	}
	if sb == "" {
		return -1
	}
	if sa < sb {
		return -1
	}
	return 1
}

// PickAsset returns the asset for the current OS/arch. Naming convention is
// dictated by .github/workflows/release.yml:
//
//	macOS:   catdb-<ver>-darwin-<arch>.dmg               (arch in {amd64, arm64})
//	Windows: catdb-<ver>-windows-<arch>-installer.exe    (arch in {amd64})
//
// Match is case-insensitive substring on the asset name.
func PickAsset(r *Release) (*ReleaseAsset, error) {
	if r == nil || len(r.Assets) == 0 {
		return nil, ErrNoAsset
	}
	goos := runtime.GOOS
	arch := runtime.GOARCH

	var wantSuffix, wantOS string
	switch goos {
	case "darwin":
		wantOS = "darwin"
		wantSuffix = ".dmg"
	case "windows":
		wantOS = "windows"
		wantSuffix = ".exe"
	default:
		return nil, fmt.Errorf("%w: unsupported OS %q", ErrNoAsset, goos)
	}

	for i := range r.Assets {
		name := strings.ToLower(r.Assets[i].Name)
		if !strings.HasSuffix(name, wantSuffix) {
			continue
		}
		if !strings.Contains(name, wantOS) {
			continue
		}
		if !strings.Contains(name, arch) {
			continue
		}
		return &r.Assets[i], nil
	}
	return nil, fmt.Errorf("%w: %s/%s", ErrNoAsset, goos, arch)
}
