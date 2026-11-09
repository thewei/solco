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
	"strings"
)

// Source is what a page reads: the broker's client, or a stand-in.
type Source interface {
	Threads(ctx context.Context, waitingOnly bool, project string) ([]ThreadSummary, error)
	Thread(ctx context.Context, id string) (Thread, error)
}

// Manager is what a source that can also tidy the inbox offers; *Client implements it. A page asks for the
// capability by the type assertion, so a source without it simply shows no management.
type Manager interface {
	DeleteThreads(ctx context.Context, ids []string) (int, error)
	Purge(ctx context.Context, olderThanDays int, dryRun bool) (int, error)
	Search(ctx context.Context, q string) ([]string, error)
	Retention(ctx context.Context) (int, error)
	SetRetention(ctx context.Context, days int) error
	Pick(ctx context.Context, threadID, kind, q string) ([]PickItem, error)
	Thumbnails(ctx context.Context, askID string) ([]Attachment, error)
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
	// RetentionDays is how long finished threads are kept (0 or absent = forever).
	RetentionDays int `json:"retentionDays,omitempty"`
}

// ReadSnapshot reads the thread list and, for selected, its timeline. An offline broker and a thread that is gone
// are states of the snapshot, not errors.
func ReadSnapshot(ctx context.Context, src Source, selected string) (Snapshot, error) {
	return ReadSnapshotQuery(ctx, src, selected, "")
}

// ReadSnapshotQuery is ReadSnapshot with the list narrowed to the threads whose text contains query (when the source
// can search); Waiting still counts every waiting thread, so the badge never changes with a search.
func ReadSnapshotQuery(ctx context.Context, src Source, selected, query string) (Snapshot, error) {
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
	if m, ok := src.(Manager); ok {
		if days, err := m.Retention(ctx); err == nil {
			out.RetentionDays = days
		}
		if q := strings.TrimSpace(query); q != "" {
			if ids, err := m.Search(ctx, q); err == nil {
				hit := make(map[string]bool, len(ids))
				for _, id := range ids {
					hit[id] = true
				}
				kept := out.Threads[:0:0]
				for _, t := range out.Threads {
					if hit[t.ID] {
						kept = append(kept, t)
					}
				}
				out.Threads = kept
			}
		}
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
