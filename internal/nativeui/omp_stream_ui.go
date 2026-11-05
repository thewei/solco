package nativeui

import (
	"os"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/history"
	"github.com/thewei/solco/internal/ompstream"
)

/**
 * [INPUT]: Application-embedded OMP/Pi Extension, temp HOME-injectable Manager, Settings→Agents user interaction.
 * [OUTPUT]: OMP/Pi live text diagnosis and explicit Install/Update action with stateful UI feedback.
 * [POS]: SC-05/SC-06 Doctor presentation; no automatic installation, Herdr lifecycle or active OMP restart.
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

type ompStreamUIState struct {
	manager      *ompstream.Manager
	diagnosis    ompstream.Diagnosis
	loaded       bool
	loading      bool
	installing   bool
	pendingToast string
}

// WithOMPStreamExtension attaches the exact extension bundled in Solco.app;
// the package does not need to find a developer checkout at runtime.
func WithOMPStreamExtension(source []byte) ShellOption {
	return func(s *Shell) {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return
		}
		s.ompStream.manager = ompstream.NewManager(home, source)
		s.piStream.manager = ompstream.NewPiManagerWithDir(home, os.Getenv("PI_CODING_AGENT_DIR"), source)
	}
}

func (s *Shell) streamState(provider history.AgentID) *ompStreamUIState {
	switch provider {
	case history.AgentOMP:
		return &s.ompStream
	case history.AgentPi:
		return &s.piStream
	default:
		return nil
	}
}

func (s *Shell) requestOMPStreamDiagnosis() { s.requestAgentStreamDiagnosis(history.AgentOMP) }

func (s *Shell) requestAgentStreamDiagnosis(provider history.AgentID) {
	state := s.streamState(provider)
	if state == nil {
		return
	}
	if state.manager == nil || state.loading || state.installing {
		return
	}
	state.loading = true
	s.dispatch(func() {
		fresh := state.manager.Diagnose()
		s.applyOnWindow(func() {
			state.diagnosis = fresh
			state.loaded = true
			state.loading = false
		})
	})
}

func (s *Shell) installOMPStreamExtension() { s.installAgentStreamExtension(history.AgentOMP) }

func (s *Shell) installAgentStreamExtension(provider history.AgentID) {
	state := s.streamState(provider)
	if state == nil {
		return
	}
	if state.manager == nil || state.installing || state.loading || !state.loaded {
		return
	}
	if state.diagnosis.State != ompstream.Missing && state.diagnosis.State != ompstream.Outdated {
		return
	}
	state.installing = true
	s.dispatch(func() {
		fresh, err := state.manager.Install()
		s.applyOnWindow(func() {
			state.installing = false
			state.loaded = true
			state.diagnosis = fresh
			if err != nil {
				state.pendingToast = "Couldn't install " + string(provider) + " live text. Check the extensions folder."
			} else {
				state.pendingToast = string(provider) + " live text is ready for new sessions"
			}
		})
	})
}

func ompStreamAction(state ompstream.State) string {
	switch state {
	case ompstream.Missing:
		return "Install Live Text"
	case ompstream.Outdated:
		return "Update Live Text"
	default:
		return ""
	}
}

func ompStreamTone(state ompstream.State) StatusTone {
	switch state {
	case ompstream.Current:
		return ToneSuccess
	case ompstream.Outdated:
		return ToneWarning
	case ompstream.Foreign, ompstream.Error:
		return ToneError
	case ompstream.Missing:
		return ToneMuted
	default:
		return ToneWorking
	}
}

func ompStreamStatus(state ompstream.State) string {
	switch state {
	case ompstream.Current:
		return "Installed"
	case ompstream.Outdated:
		return "Update available"
	case ompstream.Foreign:
		return "Protected"
	case ompstream.Error:
		return "Unavailable"
	case ompstream.Missing:
		return "Not installed"
	default:
		return "Checking"
	}
}

// ompStreamRow is an attached facet under OMP's existing toggle row.
// It cannot be triggered merely by switching OMP on: it needs a direct click.
func (s *Shell) ompStreamRow(c *ui.Context, provider history.AgentID) settingsRow {
	state := s.streamState(provider)
	label := "Pi live text"
	if provider == history.AgentOMP {
		label = "OMP live text"
	}
	return settingsRow{
		attached: true,
		lead:     func() { ui.Box(c).Width(24).Shrink(0) },
		label:    label,
		note: func() string {
			if !state.loaded {
				return "Checks an independent read-only extension"
			}
			return state.diagnosis.Detail
		}(),
		control: func() {
			ui.Row(c).Gap(Spacing().M).AlignItems(ui.Center).Children(func() {
				if state.loading || state.installing {
					ui.Spinner(c).Size(14, 14)
				}
				if state.loaded {
					statusPill(c, ompStreamStatus(state.diagnosis.State), ompStreamTone(state.diagnosis.State))
				}
				if label := ompStreamAction(state.diagnosis.State); label != "" {
					if regularButton(c, label).Label(label).Disabled(state.loading || state.installing).Clicked() {
						s.installAgentStreamExtension(provider)
					}
				}
			})
		},
	}
}
