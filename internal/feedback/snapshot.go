package feedback

/**
 * [INPUT]: 一个能列线程、读线程的来源（Client，或测试替身）
 * [OUTPUT]: Source、Snapshot（线程列表 + 选中线程的时间线）、ReadSnapshot
 * [POS]: 桌面 WebView 桥与手机 Host v3 `GET /feedback` 共用的页面读法——两端的 Feedback 页读到同一个形状；broker 不在 = Online=false 而不是错误，线程已不存在 = 无 Detail
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"errors"
)

// Source is what a page reads: the broker's client, or a stand-in.
type Source interface {
	Threads(ctx context.Context, waitingOnly bool, project string) ([]ThreadSummary, error)
	Thread(ctx context.Context, id string) (Thread, error)
}

// Snapshot is what the Feedback page draws, one value per read.
type Snapshot struct {
	// Online is false while no process serves the broker; the page shows its offline state and keeps polling.
	Online  bool            `json:"online"`
	Threads []ThreadSummary `json:"threads"`
	Waiting int             `json:"waiting"`
	// Selected is the thread Detail belongs to ("" = none).
	Selected string  `json:"selected"`
	Detail   *Thread `json:"detail,omitempty"`
}

// ReadSnapshot reads the thread list and, for selected, its timeline. An offline broker and a thread that is gone
// are states of the snapshot, not errors.
func ReadSnapshot(ctx context.Context, src Source, selected string) (Snapshot, error) {
	out := Snapshot{Threads: []ThreadSummary{}}
	threads, err := src.Threads(ctx, false, "")
	if err != nil {
		if errors.Is(err, ErrOffline) {
			return out, nil
		}
		return out, err
	}
	out.Online = true
	if threads != nil {
		out.Threads = threads
	}
	for _, t := range out.Threads {
		out.Waiting += t.Waiting
	}
	if selected == "" {
		return out, nil
	}
	detail, err := src.Thread(ctx, selected)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return out, nil
		}
		return out, err
	}
	out.Selected = selected
	out.Detail = &detail
	return out, nil
}
