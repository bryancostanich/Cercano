// Package update checks for new Cercano releases on GitHub and caches results.
package update

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// GitHubReleaseURL is the API endpoint for the latest release.
	GitHubReleaseURL = "https://api.github.com/repos/bryancostanich/Cercano/releases/latest"
	// CacheTTL is how long a cached check result is considered fresh.
	CacheTTL = 24 * time.Hour
	// HTTPTimeout is the max time to wait for the GitHub API.
	HTTPTimeout = 3 * time.Second
)

// UpdateInfo holds the result of a version check.
type UpdateInfo struct {
	LatestVersion  string `json:"latest_version"`
	CurrentVersion string `json:"current_version"`
	UpdateAvailable bool  `json:"update_available"`
	ReleaseURL     string `json:"release_url"`
	InstallMethod  string `json:"install_method"` // "homebrew" or "manual"
}

// UpgradeCommand returns the appropriate upgrade command for the install method.
func (u *UpdateInfo) UpgradeCommand() string {
	if u.InstallMethod == "homebrew" {
		return "brew upgrade cercano"
	}
	return fmt.Sprintf("Download from %s", u.ReleaseURL)
}

// cachedCheck is the on-disk cache format.
type cachedCheck struct {
	LatestVersion string    `json:"latest_version"`
	CheckedAt     time.Time `json:"checked_at"`
	ReleaseURL    string    `json:"release_url"`
	InstallMethod string    `json:"install_method"`
}

// githubRelease is the subset of the GitHub API response we need.
type githubRelease struct {
	TagName    string `json:"tag_name"`
	Prerelease bool   `json:"prerelease"`
	HTMLURL    string `json:"html_url"`
}

// CheckForUpdate queries GitHub for the latest release and compares versions.
// Returns nil on network failure (never errors to the caller).
func CheckForUpdate(currentVersion string) *UpdateInfo {
	return checkForUpdateWith(currentVersion, GitHubReleaseURL, &http.Client{Timeout: HTTPTimeout})
}

// checkForUpdateWith is the testable implementation.
func checkForUpdateWith(currentVersion, apiURL string, client *http.Client) *UpdateInfo {
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "cercano-update-check")

	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil
	}

	if release.Prerelease {
		return nil
	}

	latest := strings.TrimPrefix(release.TagName, "v")
	current := strings.TrimPrefix(currentVersion, "v")

	return &UpdateInfo{
		LatestVersion:   latest,
		CurrentVersion:  current,
		UpdateAvailable: CompareVersions(latest, current) > 0,
		ReleaseURL:      release.HTMLURL,
		InstallMethod:   DetectInstallMethod(),
	}
}

// CheckCached returns a cached result if fresh, otherwise fetches and caches.
// On stale cache + network failure, returns the stale result.
func CheckCached(currentVersion, configDir string) *UpdateInfo {
	return checkCachedWith(currentVersion, configDir, CheckForUpdate)
}

// checkCachedWith is the testable implementation that accepts a check function.
func checkCachedWith(currentVersion, configDir string, checkFn func(string) *UpdateInfo) *UpdateInfo {
	cachePath := filepath.Join(configDir, "update_check.json")

	// Try reading cache
	cached, err := readCache(cachePath)
	if err == nil && time.Since(cached.CheckedAt) < CacheTTL {
		// Cache is fresh
		current := strings.TrimPrefix(currentVersion, "v")
		return &UpdateInfo{
			LatestVersion:   cached.LatestVersion,
			CurrentVersion:  current,
			UpdateAvailable: CompareVersions(cached.LatestVersion, current) > 0,
			ReleaseURL:      cached.ReleaseURL,
			InstallMethod:   cached.InstallMethod,
		}
	}

	// Cache is stale or missing — fetch
	info := checkFn(currentVersion)
	if info != nil {
		writeCache(cachePath, &cachedCheck{
			LatestVersion: info.LatestVersion,
			CheckedAt:     time.Now(),
			ReleaseURL:    info.ReleaseURL,
			InstallMethod: info.InstallMethod,
		})
		return info
	}

	// Network failed — return stale cache if we have one
	if cached != nil {
		current := strings.TrimPrefix(currentVersion, "v")
		return &UpdateInfo{
			LatestVersion:   cached.LatestVersion,
			CurrentVersion:  current,
			UpdateAvailable: CompareVersions(cached.LatestVersion, current) > 0,
			ReleaseURL:      cached.ReleaseURL,
			InstallMethod:   cached.InstallMethod,
		}
	}

	return nil
}

// CompareVersions compares two semver strings. Returns:
//
//	1 if a > b, -1 if a < b, 0 if equal.
func CompareVersions(a, b string) int {
	aParts := parseSemver(a)
	bParts := parseSemver(b)

	for i := 0; i < 3; i++ {
		if aParts[i] > bParts[i] {
			return 1
		}
		if aParts[i] < bParts[i] {
			return -1
		}
	}
	return 0
}

// parseSemver splits a version string into [major, minor, patch].
func parseSemver(v string) [3]int {
	v = strings.TrimPrefix(v, "v")
	parts := strings.SplitN(v, ".", 3)
	var result [3]int
	for i := 0; i < len(parts) && i < 3; i++ {
		n, _ := strconv.Atoi(parts[i])
		result[i] = n
	}
	return result
}

// DetectInstallMethod returns "homebrew" if cercano was installed via brew, otherwise "manual".
func DetectInstallMethod() string {
	if _, err := exec.LookPath("brew"); err != nil {
		return "manual"
	}
	cmd := exec.Command("brew", "list", "cercano")
	if err := cmd.Run(); err != nil {
		return "manual"
	}
	return "homebrew"
}

func readCache(path string) (*cachedCheck, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cached cachedCheck
	if err := json.Unmarshal(data, &cached); err != nil {
		return nil, err
	}
	return &cached, nil
}

func writeCache(path string, cached *cachedCheck) {
	data, err := json.MarshalIndent(cached, "", "  ")
	if err != nil {
		return
	}
	os.MkdirAll(filepath.Dir(path), 0755)
	os.WriteFile(path, data, 0644)
}
