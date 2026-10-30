package nativeui

import (
	"context"
	"maps"
	"strings"
	"time"

	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/history"
)

// §17.2 usage secondary line + session title: the quick-panel/status rows
// render a cached History-meta usage projection ("Sonnet · 38k tok · $0.21
// est.") when the bound session metadata carries model/token facts, and the
// Agent cards title themselves after the session's title — read live from
// the named session file, because the catalog only refreshes on explicit
// scans while agents rename their sessions mid-task. The refresh runs on
// the dispatch lane; the render only reads the resulting maps — no
// render-time IO.

// usageProjectionThrottle mirrors the 0.8 aggregator's one-minute throttle.
const usageProjectionThrottle = time.Minute

// usageLineFor reads the cached per-Agent usage line (pure map read).
func (s *Shell) usageLineFor(key agent.AgentKey) string {
	if s.usageByAgent == nil {
		return ""
	}
	return s.usageByAgent[key].FormatUsageLine()
}

// sessionTitleFor reads the cached History-meta session title for one
// agent (pure map read; empty until the catalog knows the session).
func (s *Shell) sessionTitleFor(key agent.AgentKey) string {
	if s.agentSessionTitle == nil {
		return ""
	}
	return s.agentSessionTitle[key]
}

// refreshUsageProjection reloads the cached History-meta projection for the
// live agents: exact session identities (id/path kinds) match catalog
// metadata; the matched SessionMeta feeds the §17.6 usage snapshot and the
// card-title cache (sessionTitleFor lets titles win over the runtime
// name). Throttled; force bypasses (quick panel opening, a fresh session
// identity from the hook lane).
func (s *Shell) refreshUsageProjection(force bool) {
	service := s.hist.service
	if service == nil {
		return
	}
	if !force && time.Since(s.usageRefreshedAt) < usageProjectionThrottle {
		return
	}
	s.usageRefreshedAt = time.Now()

	// Capture the live identities on the UI lane; the background lane only
	// reads the catalog and these captured facts.
	type usageTarget struct {
		key      agent.AgentKey
		provider history.AgentID
		kind     string
		value    string
	}
	cards := s.workbenchCards()
	targets := make([]usageTarget, 0, len(cards))
	for _, runtimeAgent := range s.projection.Agents {
		if runtimeAgent.AgentSession == nil {
			continue
		}
		provider, ok := history.ParseAgentID(runtimeAgent.AgentSession.Agent)
		if !ok {
			continue
		}
		for _, card := range cards {
			if card.Key.TerminalID == runtimeAgent.TerminalID {
				targets = append(targets, usageTarget{
					key:      card.Key,
					provider: provider,
					kind:     runtimeAgent.AgentSession.Kind,
					value:    runtimeAgent.AgentSession.Value,
				})
				break
			}
		}
	}
	if len(targets) == 0 {
		return
	}

	s.dispatch(func() {
		summaries, err := service.Recent(context.Background(), 100)
		if err != nil {
			return
		}
		snapshots := make(map[agent.AgentKey]agent.AgentUsageSnapshot, len(targets))
		titles := make(map[agent.AgentKey]string, len(targets))
		for _, target := range targets {
			title := ""
			for _, summary := range summaries {
				if usageIdentityMatches(target.provider, target.kind, target.value, summary.Meta) {
					snapshots[target.key] = agent.BuildAgentUsageSnapshot(target.key, summary.Meta)
					title = strings.TrimSpace(summary.Meta.Title)
					break
				}
			}
			// The catalog indexes a source only on scans: a brand-new
			// agent's session is not in it yet, and a renamed session's
			// fresh title lands there even later. One bounded parse of the
			// named file per agent per refresh keeps the card current
			// without waiting for either.
			if live, ok := liveSessionTitle(target.provider, target.kind, target.value); ok {
				title = live
			}
			if title != "" {
				titles[target.key] = title
			}
		}
		s.applyOnWindow(func() {
			s.usageByAgent = snapshots
			titleChanged := !maps.Equal(s.agentSessionTitle, titles)
			s.agentSessionTitle = titles
			// Only a real title change replays the workbench reconcile:
			// same-phase replay is inert (ClassifyTransition returns None)
			// but not free.
			if titleChanged {
				s.reconcileWorkbench()
			}
		})
	})
}

// usageIdentityMatches one typed session identity against a catalog
// metadata row: same provider, and the declared kind's value equal to the
// native id ("id") or the exact source path ("path"). Never cwd/mtime.
func usageIdentityMatches(provider history.AgentID, kind, value string, meta history.SessionMeta) bool {
	if meta.Agent != provider || value == "" {
		return false
	}
	switch kind {
	case "id":
		return value == meta.ID
	case "path":
		return value == meta.FilePath
	default:
		return false
	}
}

// liveSessionTitle reads one agent's session file directly for its current
// title. Only a path identity names a file; an id relies on the catalog.
func liveSessionTitle(provider history.AgentID, kind, value string) (string, bool) {
	if kind != "path" || value == "" {
		return "", false
	}
	summary, err := history.SummarizeSource(history.SessionFileRef{Agent: provider, FilePath: value})
	if err != nil {
		return "", false
	}
	title := strings.TrimSpace(summary.Meta.Title)
	return title, title != ""
}
