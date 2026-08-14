// Package update checks GitHub for newer hrdx releases and performs
// in-place self-updates. The checker is called at TUI startup and
// rendered as a footer notice; `hrdx update` uses the same resolution
// path so the two never disagree about what "latest" means.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"golang.org/x/mod/semver"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// checkTTL is how often we hit the GitHub API to look for a new
// release. Half a day is frequent enough to notice the same day a
// release ships without spamming the API on every launch.
const checkTTL = 12 * time.Hour

// checkFile is the on-disk cache, keyed to the current binary version.
const checkFile = "update-check.json"

// releasesAPI is the REST endpoint we query. Using the API (not the
// HTML redirect) because the JSON response is stable and small.
const releasesAPI = "https://api.github.com/repos/patriceckhart/hrdx/releases/latest"

// Info describes the result of an update check. Zero-value means "no
// update available, no error, don't show anything".
type Info struct {
	Current   string // e.g. "0.0.4"
	Latest    string // e.g. "0.0.5"
	Available bool   // true when latest > current
	URL       string // release page url for the changelog link
}

// cache is the on-disk structure written next to the state file.
type cache struct {
	CheckedAt time.Time `json:"checked_at"`
	// The version that was current when we last checked. Invalidates
	// the cache if the binary itself has been updated since.
	CurrentAt string `json:"current_at"`
	Latest    string `json:"latest"`
	URL       string `json:"url"`
}

// Check returns info about a newer release, using a cached result when
// one is fresh enough. Designed to be called at TUI startup and shown
// as a footer notice. Always returns a usable Info (zero-value on
// error); network failures silently no-op so startup never blocks.
func Check(ctx context.Context, cacheDir, currentVersion string) Info {
	// Dev builds ("0.0.0") never have an update to offer.
	if currentVersion == "" || currentVersion == "dev" || currentVersion == "0.0.0" {
		return Info{}
	}

	cachePath := filepath.Join(cacheDir, checkFile)
	if c, ok := readCache(cachePath); ok {
		// Only trust the cache when it already reports an available
		// update. If it says "up to date" we re-check anyway so a
		// release published after the last launch is picked up
		// without waiting out the full TTL.
		if time.Since(c.CheckedAt) < checkTTL && c.CurrentAt == currentVersion {
			info := buildInfo(currentVersion, c.Latest, c.URL)
			if info.Available {
				return info
			}
		}
	}

	latest, url, err := FetchLatestRelease(ctx)
	if err != nil {
		return Info{}
	}

	_ = writeCache(cachePath, cache{
		CheckedAt: time.Now().UTC(),
		CurrentAt: currentVersion,
		Latest:    latest,
		URL:       url,
	})

	return buildInfo(currentVersion, latest, url)
}

// CheckAsync runs Check in a goroutine and delivers the result to the
// returned channel, which is always closed.
func CheckAsync(cacheDir, currentVersion string) <-chan Info {
	ch := make(chan Info, 1)
	go func() {
		defer close(ch)
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		ch <- Check(ctx, cacheDir, currentVersion)
	}()
	return ch
}

func buildInfo(current, latest, url string) Info {
	info := Info{
		Current: VersionOnly(current),
		Latest:  strings.TrimPrefix(latest, "v"),
		URL:     url,
	}
	info.Available = VersionLess(info.Current, info.Latest)
	return info
}

// VersionLess reports whether a is an older semantic version than b.
// Invalid versions fail closed so the updater never offers a downgrade.
func VersionLess(a, b string) bool {
	a = normalizedVersion(a)
	b = normalizedVersion(b)
	if !semver.IsValid(a) || !semver.IsValid(b) {
		return false
	}
	return semver.Compare(a, b) < 0
}

func normalizedVersion(v string) string {
	v = VersionOnly(v)
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	return v
}

// VersionOnly strips build metadata appended to the version string, so
// "0.0.5 (43da5e5, 2026-05-12)" becomes "0.0.5".
func VersionOnly(v string) string {
	if i := strings.IndexAny(v, " ("); i > 0 {
		return strings.TrimSpace(v[:i])
	}
	return v
}

// FetchLatestRelease queries the GitHub API for the latest published
// release.
func FetchLatestRelease(ctx context.Context) (tag, url string, err error) {
	req, err := http.NewRequestWithContext(ctx, "GET", releasesAPI, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("accept", "application/vnd.github+json")
	req.Header.Set("x-github-api-version", "2022-11-28")

	client := &http.Client{Timeout: 4 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", "", fmt.Errorf("github api %d", resp.StatusCode)
	}

	var body struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", "", err
	}
	return body.TagName, body.HTMLURL, nil
}

// readCache loads the last check result. Returns ok=false on any error
// (missing file, bad json) so callers treat it as a cache miss.
func readCache(path string) (cache, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return cache{}, false
	}
	var c cache
	if err := json.Unmarshal(b, &c); err != nil {
		return cache{}, false
	}
	return c, true
}

func writeCache(path string, c cache) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}
