package nativeui

/**
 * [INPUT]: 依赖 internal/changelog（版本比对与 Markdown 日志解析）、
 * github.com/egoist/mygo/plugins/updater、GitHub Releases REST API
 * [OUTPUT]: 对外提供 Shell 的更新状态与检查接口（hasUpdate、updateInfo、checkForUpdates、triggerUpdate）
 * [POS]: internal/nativeui 表现层的更新感知管理。在后台探测新版本、驱动侧栏与设置小红点及 About 页跨版本更新汇总
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater"
	"github.com/thewei/solco/internal/changelog"
)

const (
	githubReleasesAPI = "https://api.github.com/repos/thewei/solco/releases?per_page=20"
	updateCheckTTL    = 1 * time.Hour
)

// UpdateInfo holds the observed update status for presentation.
type UpdateInfo struct {
	Checked        bool
	Checking       bool
	HasUpdate      bool
	CurrentVersion string
	LatestVersion  string
	Releases       []changelog.Release // Releases newer than CurrentVersion up to LatestVersion
	LatestURL      string
	CheckedAt      time.Time
	Error          string
}

// updateManager manages update discovery and release notes aggregation.
type updateManager struct {
	mu            sync.RWMutex
	info          UpdateInfo
	customChecker func(ctx context.Context, current string) (UpdateInfo, error)
	httpClient    *http.Client
}

func newUpdateManager() *updateManager {
	return &updateManager{
		httpClient: &http.Client{Timeout: 6 * time.Second},
	}
}

// githubRelease mirrors the GitHub Releases API item.
type githubRelease struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	Body        string `json:"body"`
	PublishedAt string `json:"published_at"`
	Draft       bool   `json:"draft"`
	Prerelease  bool   `json:"prerelease"`
	HTMLURL     string `json:"html_url"`
}

// check checks for newer releases from GitHub or the embedded snapshot.
func (m *updateManager) check(ctx context.Context, currentVersion string, force bool) UpdateInfo {
	m.mu.Lock()
	if m.info.Checking {
		info := m.info
		m.mu.Unlock()
		return info
	}
	if !force && m.info.Checked && time.Since(m.info.CheckedAt) < updateCheckTTL {
		info := m.info
		m.mu.Unlock()
		return info
	}
	m.info.Checking = true
	m.info.Error = ""
	checker := m.customChecker
	client := m.httpClient
	m.mu.Unlock()

	var newInfo UpdateInfo
	var checkErr error

	if checker != nil {
		newInfo, checkErr = checker(ctx, currentVersion)
	} else {
		newInfo, checkErr = m.fetchGitHubReleases(ctx, client, currentVersion)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.info.Checking = false
	m.info.Checked = true
	m.info.CheckedAt = time.Now()
	m.info.CurrentVersion = currentVersion

	if checkErr != nil {
		m.info.Error = checkErr.Error()
		// Fallback to embedded snapshot when network fails
		m.fallbackToEmbedded(currentVersion)
		return m.info
	}

	m.info.HasUpdate = newInfo.HasUpdate
	m.info.LatestVersion = newInfo.LatestVersion
	m.info.Releases = newInfo.Releases
	m.info.LatestURL = newInfo.LatestURL
	m.info.Error = ""
	return m.info
}

// fetchGitHubReleases queries the GitHub releases API.
func (m *updateManager) fetchGitHubReleases(ctx context.Context, client *http.Client, current string) (UpdateInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubReleasesAPI, nil)
	if err != nil {
		return UpdateInfo{}, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "Solco-Updater")

	resp, err := client.Do(req)
	if err != nil {
		return UpdateInfo{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return UpdateInfo{}, fmt.Errorf("github api status: %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return UpdateInfo{}, err
	}

	var ghReleases []githubRelease
	if err := json.Unmarshal(body, &ghReleases); err != nil {
		return UpdateInfo{}, err
	}

	var parsed []changelog.Release
	var latestURL string

	for _, gh := range ghReleases {
		if gh.Draft || gh.Prerelease {
			continue
		}
		ver := strings.TrimPrefix(gh.TagName, "v")
		date := gh.PublishedAt
		if idx := strings.Index(date, "T"); idx != -1 {
			date = date[:idx]
		}
		if latestURL == "" && gh.HTMLURL != "" {
			latestURL = gh.HTMLURL
		}
		// Parse markdown notes from GitHub release body
		md := fmt.Sprintf("## v%s — %s\n\n%s", ver, date, gh.Body)
		rels := changelog.ParseReleases(md)
		if len(rels) > 0 {
			parsed = append(parsed, rels[0])
		} else {
			parsed = append(parsed, changelog.Release{
				Version: ver,
				Date:    date,
			})
		}
	}

	if len(parsed) == 0 {
		return UpdateInfo{}, fmt.Errorf("no releases found")
	}

	latestVer := parsed[0].Version
	hasUpdate := changelog.CompareVersions(latestVer, current) > 0

	var between []changelog.Release
	if hasUpdate {
		for _, rel := range parsed {
			if changelog.CompareVersions(rel.Version, current) > 0 {
				between = append(between, rel)
			}
		}
	}

	return UpdateInfo{
		HasUpdate:     hasUpdate,
		LatestVersion: latestVer,
		Releases:      between,
		LatestURL:     latestURL,
	}, nil
}

// fallbackToEmbedded uses local snapshot when offline.
func (m *updateManager) fallbackToEmbedded(current string) {
	all := changelog.All()
	if len(all) == 0 {
		return
	}
	latest := all[0].Version
	if changelog.CompareVersions(latest, current) > 0 {
		m.info.HasUpdate = true
		m.info.LatestVersion = latest
		m.info.Releases = changelog.Between(current, "")
		m.info.LatestURL = aboutRepoURL + "/releases/tag/v" + latest
	}
}

// hasUpdate reports whether a newer version is known to be available.
func (s *Shell) hasUpdate() bool {
	if s.updates == nil {
		return false
	}
	s.updates.mu.RLock()
	defer s.updates.mu.RUnlock()
	return s.updates.info.HasUpdate
}

// updateInfo returns a copy of current update state.
func (s *Shell) updateInfo() UpdateInfo {
	if s.updates == nil {
		return UpdateInfo{}
	}
	s.updates.mu.RLock()
	defer s.updates.mu.RUnlock()
	return s.updates.info
}

// checkForUpdates initiates an update check in the background.
func (s *Shell) checkForUpdates(force bool) {
	if s.updates == nil {
		return
	}
	current := mygo.App.Version()
	if current == "" {
		current = "dev"
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		info := s.updates.check(ctx, current, force)
		slog.Info("update check completed", "has_update", info.HasUpdate, "latest", info.LatestVersion, "err", info.Error)

		s.applyOnWindow(func() {
			// Window re-evaluates the frame with the new state
		})
	}()
}

// triggerUpdate executes the update action.
func (s *Shell) triggerUpdate() {
	if mygo.Updater.Enabled() {
		updater.CheckForUpdates()
		return
	}
	// Fall back to opening the release page in the browser
	url := aboutReleasesURL
	info := s.updateInfo()
	if info.LatestURL != "" {
		url = info.LatestURL
	} else if info.LatestVersion != "" {
		url = aboutRepoURL + "/releases/tag/v" + info.LatestVersion
	}
	_ = openAboutURL(url)
}

// setUpdateSimulation sets simulated update state for unit tests.
func (s *Shell) setUpdateSimulation(hasUpdate bool, latestVersion string, releases []changelog.Release) {
	if s.updates == nil {
		s.updates = newUpdateManager()
	}
	s.updates.mu.Lock()
	defer s.updates.mu.Unlock()
	s.updates.info.Checked = true
	s.updates.info.HasUpdate = hasUpdate
	s.updates.info.LatestVersion = latestVersion
	s.updates.info.Releases = releases
}
