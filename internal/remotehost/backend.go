package remotehost

import (
	"context"

	"github.com/thewei/solco/internal/herdr"
)

/**
 * [INPUT]: 依赖 internal/herdr（Manager、Projection、EventStream）
 * [OUTPUT]: 对外提供 Backend（Host 与 Herdr 之间唯一的小接口）、EventSource、NewManagerBackend
 * [POS]: Host 只经这个缝触达 Herdr：本机 Session 列表、投影、事件、窗格输入、终端 observe 命令；测试用假实现替换，Host 不 import MyGo/nativeui
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// EventSource is one live Herdr event subscription.
type EventSource interface {
	Events() <-chan herdr.RuntimeEvent
	Done() <-chan struct{}
	Close()
}

// Backend is everything the Host needs from Herdr. Instances are Herdr
// session names; the Host serves local sessions only and validates every
// instance, pane and terminal ID against this interface before it reaches a
// path or an argv.
type Backend interface {
	// Instances lists the local Herdr sessions (never saved remote machines).
	Instances() ([]string, error)
	Projection(instance string) (herdr.Projection, error)
	Subscribe(instance string, paneIDs []string) (EventSource, error)
	// SendPaneInput writes literal text, then named keys, into one pane
	// (Herdr pane.send_input). The Host never attaches to a terminal.
	SendPaneInput(ctx context.Context, instance, paneID, text string, keys []string) error
	// ReadPane returns the pane's visible screen as plain text (pane.read).
	ReadPane(ctx context.Context, instance, paneID string) (string, error)
	// ObserveCommand builds the read-only `terminal session observe` child.
	ObserveCommand(instance, terminalID string, cols, rows int) ([]string, error)
}

type managerBackend struct{ m *herdr.Manager }

// NewManagerBackend adapts the app's Herdr Manager to Backend.
func NewManagerBackend(m *herdr.Manager) Backend { return managerBackend{m: m} }

func (b managerBackend) Instances() ([]string, error) {
	all, err := b.m.ListInstances()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, in := range all {
		if in.Machine == nil {
			names = append(names, in.Name)
		}
	}
	return names, nil
}

func (b managerBackend) Projection(instance string) (herdr.Projection, error) {
	return b.m.Projection(instance)
}

func (b managerBackend) Subscribe(instance string, paneIDs []string) (EventSource, error) {
	return b.m.SubscribeEvents(instance, paneIDs)
}

func (b managerBackend) SendPaneInput(ctx context.Context, instance, paneID, text string, keys []string) error {
	return b.m.SendPaneInput(ctx, instance, paneID, text, keys)
}

func (b managerBackend) ObserveCommand(instance, terminalID string, cols, rows int) ([]string, error) {
	return b.m.TerminalObserveCommand(instance, terminalID, cols, rows)
}

func (b managerBackend) ReadPane(ctx context.Context, instance, paneID string) (string, error) {
	return b.m.ReadPane(ctx, instance, paneID, "visible")
}
