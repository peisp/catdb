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
	"sync"
	"time"
)

// DefaultRepo is the GitHub repo we poll for releases.
const DefaultRepo = "peisp/catdb"

// Channel selects which releases the updater considers.
type Channel string

const (
	// ChannelStable only sees full releases (GitHub /releases/latest).
	ChannelStable Channel = "stable"
	// ChannelBeta sees the highest version among all published releases,
	// prereleases included — so beta users still get stable updates.
	ChannelBeta Channel = "beta"
)

// ParseChannel maps a stored setting to a Channel, defaulting to stable.
func ParseChannel(s string) Channel {
	if s == string(ChannelBeta) {
		return ChannelBeta
	}
	return ChannelStable
}

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

// We cache the last successful result for a short window so repeated calls
// (dev HMR, a quick second click) don't hammer GitHub's unauthenticated rate
// limit — but the TTL ensures a long-running instance still picks up a newly
// published release. Errors are never cached: a transient failure must not
// poison every later check until the process restarts.
const fetchCacheTTL = 10 * time.Minute

type cacheEntry struct {
	rel *Release
	at  time.Time
}

var (
	cacheMu sync.RWMutex
	cache   = map[string]cacheEntry{} // keyed by repo + "|" + channel
)

// FetchLatest queries GitHub for the newest published release visible to the
// given channel. Stable uses /releases/latest (GitHub already excludes
// prereleases and drafts there); beta lists recent releases and picks the
// highest version among them, prereleases included — so beta users still get
// stable releases when those are newest. Returns ErrNoRelease if the repo
// has no published releases.
//
// The result is cached in-memory per repo+channel so that repeated calls
// (e.g. during dev HMR) are free after the first.
func FetchLatest(ctx context.Context, repo string, ch Channel) (*Release, error) {
	if repo == "" {
		repo = DefaultRepo
	}
	if ch == "" {
		ch = ChannelStable
	}
	key := repo + "|" + string(ch)

	cacheMu.RLock()
	if e, ok := cache[key]; ok && time.Since(e.at) < fetchCacheTTL {
		cacheMu.RUnlock()
		return e.rel, nil
	}
	cacheMu.RUnlock()

	var rel *Release
	var err error
	if ch == ChannelBeta {
		rel, err = fetchBestOfAll(ctx, repo)
	} else {
		rel, err = fetchStableLatest(ctx, repo)
	}
	if err != nil {
		return nil, err
	}
	cacheMu.Lock()
	cache[key] = cacheEntry{rel: rel, at: time.Now()}
	cacheMu.Unlock()
	return rel, nil
}

func fetchStableLatest(ctx context.Context, repo string) (*Release, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	body, err := githubGet(ctx, url)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	var r Release
	if err := json.NewDecoder(body).Decode(&r); err != nil {
		return nil, fmt.Errorf("updater: decode release: %w", err)
	}
	return &r, nil
}

// fetchBestOfAll lists recent releases (stable + prerelease) and returns the
// highest version among them. 20 releases of lookback is plenty: the newest
// relevant release is always near the top of the reverse-chronological list.
func fetchBestOfAll(ctx context.Context, repo string) (*Release, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases?per_page=20", repo)
	body, err := githubGet(ctx, url)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	var list []Release
	if err := json.NewDecoder(body).Decode(&list); err != nil {
		return nil, fmt.Errorf("updater: decode releases: %w", err)
	}
	var best *Release
	for i := range list {
		r := &list[i]
		if r.Draft {
			continue
		}
		if best == nil || CompareVersions(r.Version(), best.Version()) > 0 {
			best = r
		}
	}
	if best == nil {
		return nil, ErrNoRelease
	}
	return best, nil
}

// githubGet performs a GitHub API GET and returns the response body on 200.
// The caller must close it.
func githubGet(ctx context.Context, url string) (io.ReadCloser, error) {
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
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, ErrNoRelease
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		resp.Body.Close()
		return nil, fmt.Errorf("updater: GitHub API %d: %s", resp.StatusCode, string(body))
	}
	return resp.Body, nil
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
	return compareSuffix(sa, sb)
}

// compareSuffix orders prerelease suffixes semver-style: dot-separated
// segments, numeric segments compared as numbers (so beta.10 > beta.2),
// numeric segments rank below alphanumeric ones, fewer segments rank lower.
func compareSuffix(sa, sb string) int {
	as := strings.Split(sa, ".")
	bs := strings.Split(sb, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		x, y := as[i], bs[i]
		if x == y {
			continue
		}
		xn, xErr := strconv.Atoi(x)
		yn, yErr := strconv.Atoi(y)
		switch {
		case xErr == nil && yErr == nil:
			if xn < yn {
				return -1
			}
			return 1
		case xErr == nil:
			return -1
		case yErr == nil:
			return 1
		default:
			if x < y {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(as) < len(bs):
		return -1
	case len(as) > len(bs):
		return 1
	}
	return 0
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
