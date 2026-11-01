package nativeui

/**
 * [INPUT]: mygo.App.Version、internal/changelog.Current（随发布提交的 CHANGELOG 快照）、
 * brands/solco-128.png 应用标记、mygo.Shell.OpenExternal（经 openAboutURL 接缝）
 * [OUTPUT]: Settings → About 页（/settings/about）：应用标记、版本号、本版改进、
 * GitHub 仓库与发布页外链；应用菜单 About Solco 跳到同一页
 * [POS]: 纯表现层。改进内容只读发布快照，不访问网络、不新增 Herdr 方法
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
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

	// Hero: the app mark, the name, the running version.
	ui.Row(c).Gap(sp.L).AlignItems(ui.Center).Children(func() {
		if bmp := brandPNG("solco-128"); bmp != nil {
			ui.Image(c, bmp).Width(64).Height(64).Fit(ui.ScaleDown)
		}
		ui.Column(c).Gap(2).Children(func() {
			ui.Text(c, "Solco").FontSize(Typography().Heading).FontWeight(WeightSemibold)
			ui.Textf(c, "Version %s", version).FontSize(Typography().Body).TextColor(t.TextMuted)
		})
	})
	ui.Text(c, "Coding agents, without the juggling. Free and open source, GPL-3.0.").
		FontSize(Typography().BodySmall).TextColor(t.TextMuted)

	notesURL := aboutReleasesURL
	notesNote := "Every release, on GitHub."
	if ok && release.Version != "" {
		notesURL = aboutRepoURL + "/releases/tag/v" + release.Version
		notesNote = "What shipped in v" + release.Version + "."
	}
	settingsGroup(c, "Links",
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
		for _, group := range release.Groups {
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
	})
}
