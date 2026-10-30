package nativeui

/**
 * [INPUT]: hosts.entries（sshconfig 索引）、Shell.recentHosts（最近 Connect 的别名，MRU，随 settings.Session 持久）、hosts.filter
 * [OUTPUT]: hostEndpoint（user@host 副标题）、hostMatches（过滤）、noteRecentHost（记录 Connect）、hostsStart（未选主机时的起始页：「Recently connected」卡片，带 Connect/Edit，无记录时退回一句话空态）
 * [POS]: 2026-10-30 UX audit 议题 3（空态 B，用户选定；design: designs/ux-audit）：Hosts 空白页变入口；同名 ForwardAgent 一类块靠 user@host 副标题区分；只读 sshconfig 索引，不新建主机登记
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/sshconfig"
)

// recentHostsKept bounds the remembered hosts (the start page lists them all).
const recentHostsKept = 4

// hostEndpoint is "user@host" from a block's own HostName/User directives
// (either half alone when only one is set, "" when neither is).
func hostEndpoint(entry sshconfig.Entry) string {
	var host, user string
	for _, kv := range entry.Facts() {
		switch strings.ToLower(kv.Key) {
		case "hostname":
			host = kv.Value
		case "user":
			user = kv.Value
		}
	}
	switch {
	case user != "" && host != "":
		return user + "@" + host
	case host != "":
		return host
	}
	return user
}

// hostMatches reports whether the block's name or endpoint contains q
// (case-insensitive); an empty q matches everything.
func hostMatches(entry sshconfig.Entry, q string) bool {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return true
	}
	return strings.Contains(strings.ToLower(entry.Title()), q) ||
		strings.Contains(strings.ToLower(hostEndpoint(entry)), q)
}

// filteredHostEntries is the index narrowed by the sidebar's filter.
func (s *Shell) filteredHostEntries() []sshconfig.Entry {
	if strings.TrimSpace(s.hosts.filter) == "" {
		return s.hosts.entries
	}
	kept := make([]sshconfig.Entry, 0, len(s.hosts.entries))
	for _, entry := range s.hosts.entries {
		if hostMatches(entry, s.hosts.filter) {
			kept = append(kept, entry)
		}
	}
	return kept
}

// noteRecentHost moves alias to the front of the remembered hosts.
func (s *Shell) noteRecentHost(alias string) {
	kept := append(make([]string, 0, recentHostsKept), alias)
	for _, a := range s.recentHosts {
		if a != alias && len(kept) < recentHostsKept {
			kept = append(kept, a)
		}
	}
	s.recentHosts = kept
}

// recentHostEntries are the remembered aliases still in the config, newest
// first.
func (s *Shell) recentHostEntries() []sshconfig.Entry {
	var out []sshconfig.Entry
	for _, alias := range s.recentHosts {
		for _, entry := range s.hosts.entries {
			if entry.Alias() == alias {
				out = append(out, entry)
				break
			}
		}
	}
	return out
}

// hostsStart is the Hosts center while no host is selected.
func (s *Shell) hostsStart(c *ui.Context) {
	recent := s.recentHostEntries()
	if len(recent) == 0 {
		emptyState(c, "Select a host", "")
		return
	}
	t := c.Theme()
	sp := Spacing()
	ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
		contentColumn(c, 520, func() {
			ui.Column(c).FillWidth().Padding(sp.XL, sp.L).Gap(sp.L).Children(func() {
				ui.Text(c, "Select a host").FontWeight(WeightSemibold)
				ui.Column(c).FillWidth().Gap(sp.S).Children(func() {
					ui.Text(c, "RECENTLY CONNECTED").FontSize(Typography().Micro).TextColor(t.TextMuted)
					for _, entry := range recent {
						s.hostStartCard(c, entry)
					}
				})
			})
		})
	})
}

// hostStartCard is one remembered host: its name and endpoint, Connect, Edit.
func (s *Shell) hostStartCard(c *ui.Context, entry sshconfig.Entry) {
	t := c.Theme()
	sp := Spacing()
	tokens := designTokens(t.Dark)
	alias := entry.Alias()
	ui.Row(c.Key("host-start:"+alias)).FillWidth().Padding(sp.M, sp.L).Gap(sp.M).AlignItems(ui.Center).
		Radius(Radius().Card).Border(1, tokens.BorderSubtle).Children(func() {
		ui.Icon(c, iconGlobe).Size(16, 16).TextColor(t.TextMuted).Shrink(0)
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			ui.Text(c, entry.Title()).FontWeight(WeightSemibold).SingleLine()
			if endpoint := hostEndpoint(entry); endpoint != "" {
				ui.Text(c, endpoint).FontSize(Typography().BodySmall).TextColor(t.TextMuted).SingleLine()
			}
		})
		if regularButton(c, "Edit").Clicked() {
			s.hosts.selected = hostEntryKey(entry)
			s.openHostEditor(entry.File, false)
		}
		if regularPrimaryButton(c, "Connect").Clicked() {
			s.connectHost(alias)
		}
	})
}
