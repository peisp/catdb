package updater

import (
	"runtime"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"v1.0.0", "1.0.0", 0},
		{"1.0.0", "1.0.1", -1},
		{"1.1.0", "1.0.9", 1},
		{"2.0.0", "1.99.99", 1},
		{"1.2", "1.2.0", 0},
		{"1.2.0", "1.2", 0},
		{"1.2.3", "1.2.3-rc1", 1}, // release > prerelease
		{"1.2.3-rc1", "1.2.3", -1},
		{"1.2.3-rc1", "1.2.3-rc2", -1},
		{"1.2.3-beta.2", "1.2.3-beta.10", -1}, // numeric suffix segments compare as numbers
		{"1.2.3-beta.10", "1.2.3-beta.2", 1},
		{"1.2.3-beta.1", "1.2.3-rc.1", -1}, // beta < rc
		{"1.2.3-beta", "1.2.3-beta.1", -1}, // fewer segments rank lower
		{"1.2.3-beta.1", "1.2.3-beta.1", 0},
		{"0.2.0-beta.1", "0.1.9", 1}, // higher numerics beat prerelease-ness
		{"dev", "1.0.0", -1},         // dev parses to 0 — anything > 0 wins
	}
	for _, c := range cases {
		got := CompareVersions(c.a, c.b)
		if got != c.want {
			t.Errorf("CompareVersions(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestPickAssetMatchesCurrentPlatform(t *testing.T) {
	// Asset names match what .github/workflows/release.yml uploads.
	r := &Release{Assets: []ReleaseAsset{
		{Name: "catdb-1.2.3-darwin-amd64.dmg"},
		{Name: "catdb-1.2.3-darwin-arm64.dmg"},
		{Name: "catdb-1.2.3-windows-amd64-installer.exe"},
	}}
	got, err := PickAsset(r)
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		if err != nil {
			t.Fatalf("PickAsset on %s/%s: %v", runtime.GOOS, runtime.GOARCH, err)
		}
		if got == nil {
			t.Fatalf("PickAsset returned nil asset")
		}
	}
}

func TestPickAssetNoMatchReturnsErr(t *testing.T) {
	r := &Release{Assets: []ReleaseAsset{
		{Name: "catdb-1.2.3-linux-amd64.tar.gz"},
	}}
	if _, err := PickAsset(r); err == nil {
		t.Fatal("expected ErrNoAsset, got nil")
	}
}
