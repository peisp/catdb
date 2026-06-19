package updater

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// ProgressFn is called periodically during a download. It receives bytes
// transferred so far and the total advertised by Content-Length (-1 if the
// server didn't send one). It must be safe to call from a goroutine and may
// fire several times per second.
type ProgressFn func(downloaded, total int64)

// DownloadAsset streams the asset to a temp file under os.TempDir() and
// returns the local path. The file is named after the asset so the OS-level
// installer (DMG / NSIS) can recognise it normally.
//
// Downloads of an asset that already exists locally (same size) are skipped —
// useful if the user retries after a failed install.
func DownloadAsset(ctx context.Context, asset *ReleaseAsset, onProgress ProgressFn) (string, error) {
	if asset == nil {
		return "", fmt.Errorf("updater: nil asset")
	}
	dir := filepath.Join(os.TempDir(), "catdb-update")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("updater: mkdir %s: %w", dir, err)
	}
	dest := filepath.Join(dir, asset.Name)

	// Short-circuit if we already have a fully-downloaded copy.
	if fi, err := os.Stat(dest); err == nil && asset.Size > 0 && fi.Size() == asset.Size {
		if onProgress != nil {
			onProgress(fi.Size(), asset.Size)
		}
		return dest, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 30 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("updater: download %s: %w", asset.URL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("updater: download %s: HTTP %d", asset.URL, resp.StatusCode)
	}

	tmp, err := os.CreateTemp(dir, asset.Name+".partial.*")
	if err != nil {
		return "", fmt.Errorf("updater: tempfile: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		// Best-effort cleanup of the partial if we bailed out.
		if _, err := os.Stat(tmpPath); err == nil {
			_ = os.Remove(tmpPath)
		}
	}()

	total := resp.ContentLength
	if total <= 0 && asset.Size > 0 {
		total = asset.Size
	}

	pr := &progressReader{
		r:        resp.Body,
		total:    total,
		onUpdate: onProgress,
		lastEmit: time.Now(),
	}
	if _, err := io.Copy(tmp, pr); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("updater: copy: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		return "", fmt.Errorf("updater: rename: %w", err)
	}
	if onProgress != nil {
		onProgress(pr.done, total)
	}
	return dest, nil
}

type progressReader struct {
	r        io.Reader
	done     int64
	total    int64
	onUpdate ProgressFn
	lastEmit time.Time
}

func (p *progressReader) Read(buf []byte) (int, error) {
	n, err := p.r.Read(buf)
	if n > 0 {
		p.done += int64(n)
		// throttle to ~5 emits/sec so the front-end doesn't drown in events
		if p.onUpdate != nil && time.Since(p.lastEmit) > 200*time.Millisecond {
			p.onUpdate(p.done, p.total)
			p.lastEmit = time.Now()
		}
	}
	return n, err
}
