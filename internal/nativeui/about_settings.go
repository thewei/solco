package nativeui

/**
 * [INPUT]: mygo.App.Version、internal/changelog（版本比对、快照与多版本日志解析）、
 * brands/solco-128.png 应用标记、mygo.Shell.OpenExternal（经 openAboutURL 接缝）、
 * Shell.updateInfo / Shell.checkForUpdates / Shell.triggerUpdate
 * [OUTPUT]: Settings → About 页（/settings/about）：应用标记、版本号、新版本感知与“Update Version”按钮、
 * 跨版本聚合 Changelog 摘要卡片、GitHub 仓库与发布页外链、手动“Check for Updates”
 * [POS]: 表现层的版本与发布说明主视图。支持检测新版本、总结当前版本与最新版本间的全部改动
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"fmt"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/changelog"
)

const (
	aboutRepoURL = "https://github.com/thewei/solco"
	// aboutReleasesURL is the releases list — the notes door for dev builds,
	// where the snapshot does not describe the running binary.
	aboutReleasesURL = aboutRepoURL + "/releases"
)

// openAboutURL is the browser door, a seam for tests.
var openAboutURL = func(u string) error { return mygo.Shell.OpenExternal(u) }

// settingsAbout is Settings → About: the mark, the version, what this
// release changed, and the doors to GitHub (2026-11-01).
func (s *Shell) settingsAbout(c *ui.Context) {
	t := c.Theme()
	sp := Spacing()
	version := mygo.App.Version()
	if version == "" {
		version = "dev"
	}
	release, ok := changelog.Current()
	info := s.updateInfo()

	// Hero: the app mark, the name, the running version.
	ui.Row(c).Gap(sp.L).AlignItems(ui.Center).Children(func() {
		if bmp := brandPNG("solco-128"); bmp != nil {
			ui.Image(c, bmp).Width(64).Height(64).Fit(ui.ScaleDown)
		}
		ui.Column(c).Gap(2).Children(func() {
			ui.Row(c).Gap(sp.S).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Solco").FontSize(Typography().Heading).FontWeight(WeightSemibold)
				if info.HasUpdate && info.LatestVersion != "" {
					ui.Row(c).Padding(2, 7).Radius(10).Background(t.Danger.Alpha(0.15)).Children(func() {
						ui.Textf(c, "v%s Available", info.LatestVersion).
							FontSize(Typography().Caption).FontWeight(WeightMedium).TextColor(t.Danger)
					})
				}
			})
			ui.Textf(c, "Version %s", version).FontSize(Typography().Body).TextColor(t.TextMuted)
		})
	})
	ui.Text(c, "Coding agents, without the juggling. Free and open source, GPL-3.0.").
		FontSize(Typography().BodySmall).TextColor(t.TextMuted)

	// Update action card: appears when an update is available.
	if info.HasUpdate {
		settingsCard(c, "New Version Available · v"+info.LatestVersion, func() {
			ui.Text(c, "A newer version of Solco is available with the latest features and improvements.").
				FontSize(Typography().BodySmall).TextColor(t.TextMuted)
			ui.Row(c).Gap(sp.M).AlignItems(ui.Center).Padding(sp.XS, 0).Children(func() {
				if regularPrimaryButton(c, "Update Version").Clicked() {
					s.triggerUpdate()
				}
				if regularButton(c, "View on GitHub").Clicked() {
					go func() {
						url := aboutReleasesURL
						if info.LatestURL != "" {
							url = info.LatestURL
						} else if info.LatestVersion != "" {
							url = aboutRepoURL + "/releases/tag/v" + info.LatestVersion
						}
						_ = openAboutURL(url)
					}()
				}
			})
		})
	}

	notesURL := aboutReleasesURL
	notesNote := "Every release, on GitHub."
	if info.HasUpdate && info.LatestVersion != "" {
		notesURL = aboutRepoURL + "/releases/tag/v" + info.LatestVersion
		notesNote = "What shipped in v" + info.LatestVersion + "."
	} else if ok && release.Version != "" {
		notesURL = aboutRepoURL + "/releases/tag/v" + release.Version
		notesNote = "What shipped in v" + release.Version + "."
	}

	updateStatus := "You are running the latest version."
	if info.Checking {
		updateStatus = "Checking for updates…"
	} else if info.HasUpdate {
		updateStatus = "Version v" + info.LatestVersion + " is available."
	}

	settingsGroup(c, "Links",
		row("Software Update", updateStatus, func() {
			btn := regularButton(c, "Check for Updates")
			if info.Checking {
				btn.Disabled(true)
			}
			if btn.Clicked() {
				s.checkForUpdates(true)
			}
		}),
		row("GitHub Repository", "Source, issues and releases.", func() {
			if regularButton(c, "Open").Clicked() {
				go func() { _ = openAboutURL(aboutRepoURL) }()
			}
		}),
		row("Release Notes", notesNote, func() {
			if regularButton(c, "Open").Clicked() {
				go func() { _ = openAboutURL(notesURL) }()
			}
		}),
	)

	// Changelog section:
	// If an update is available with releases spanning from current to latest,
	// summarize all of them so the user misses nothing.
	if info.HasUpdate && len(info.Releases) > 0 {
		cardTitle := "What's new in v" + info.LatestVersion
		if len(info.Releases) > 1 {
			cardTitle = fmt.Sprintf("Update Summary (v%s → v%s · %d Releases)", version, info.LatestVersion, len(info.Releases))
		}
		settingsCard(c, cardTitle, func() {
			if len(info.Releases) > 1 {
				ui.Textf(c, "Summarizing all changes across %d releases between v%s and v%s:", len(info.Releases), version, info.LatestVersion).
					FontSize(Typography().BodySmall).TextColor(t.TextMuted)
			}
			for _, rel := range info.Releases {
				if len(info.Releases) > 1 {
					ui.Row(c).Gap(sp.S).AlignItems(ui.Baseline).Padding(sp.XS, 0, 0, 0).Children(func() {
						ui.Textf(c, "v%s", rel.Version).FontSize(Typography().Section).FontWeight(WeightSemibold)
						if rel.Date != "" {
							ui.Textf(c, "(%s)", rel.Date).FontSize(Typography().Caption).TextColor(t.TextMuted)
						}
					})
				} else if rel.Date != "" {
					ui.Textf(c, "Released %s", rel.Date).FontSize(Typography().Caption).TextColor(t.TextMuted)
				}
				s.renderChangelogGroups(c, rel.Groups)
			}
		})
		return
	}

	// What's new: the curated section of the shipped release. A dev build
	// shows the last released one, which is the honest answer to "what
	// changed most recently".
	if !ok || len(release.Groups) == 0 {
		return
	}
	settingsCard(c, "What's new in v"+release.Version, func() {
		if release.Date != "" {
			ui.Textf(c, "Released %s", release.Date).FontSize(Typography().Caption).TextColor(t.TextMuted)
		}
		s.renderChangelogGroups(c, release.Groups)
	})
}

// renderChangelogGroups displays the New/Fixed/Improved bullet groups.
func (s *Shell) renderChangelogGroups(c *ui.Context, groups []changelog.Group) {
	sp := Spacing()
	for _, group := range groups {
		if group.Name == "Internal" || len(group.Items) == 0 {
			continue
		}
		groupLabel(c, group.Name)
		ui.Column(c).FillWidth().Gap(sp.XXS).Padding(0, 0, 0, Spacing().XS).Children(func() {
			for _, item := range group.Items {
				ui.Textf(c, "•  %s", item).FontSize(Typography().BodySmall)
			}
		})
	}
}
