package memory

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"

	"github.com/polarisagi/polarisagi-harness/internal/protocol"
)

// ============================================================================
// ReflectionMemory (Mem-L1.5) — 元认知反思层
// 架构文档: docs/arch/M05-Memory-System.md §3.4
// ============================================================================

// ReflectionMem 元认知反思层实现。
// 持久化到 store（key 前缀 "reflection:"），内存缓存加速最近查询。
type ReflectionMem struct {
	store   protocol.Store
	entries []protocol.ReflectionEntry
	mu      sync.Mutex
}

func NewReflectionMem(store protocol.Store) *ReflectionMem {
	return &ReflectionMem{
		store:   store,
		entries: make([]protocol.ReflectionEntry, 0),
	}
}

func (rm *ReflectionMem) AppendReflection(ctx context.Context, entry protocol.ReflectionEntry) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now()
	}

	key := []byte("reflection:" + entry.ID)
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	if err := rm.store.Put(ctx, key, data); err != nil {
		return err
	}
	rm.entries = append(rm.entries, entry)
	return nil
}

func (rm *ReflectionMem) QueryReflections(ctx context.Context, q protocol.ReflectionQuery) ([]protocol.ReflectionEntry, error) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	// 内存缓存为空时从 store 恢复
	if len(rm.entries) == 0 && rm.store != nil {
		iter, err := rm.store.Scan(ctx, []byte("reflection:"))
		if err == nil && iter != nil {
			for iter.Next() {
				var e protocol.ReflectionEntry
				if jsonErr := json.Unmarshal(iter.Value(), &e); jsonErr == nil {
					rm.entries = append(rm.entries, e)
				}
			}
			iter.Close()
		}
	}

	var results []protocol.ReflectionEntry //nolint:prealloc
	for _, e := range rm.entries {
		if q.SessionID != "" && e.SessionID != q.SessionID {
			continue
		}
		if q.AgentID != "" && e.AgentID != q.AgentID {
			continue
		}
		results = append(results, e)
	}

	// 返回最近 K 条（时间降序）
	sort.Slice(results, func(i, j int) bool {
		return results[i].CreatedAt.After(results[j].CreatedAt)
	})
	if q.K > 0 && len(results) > q.K {
		results = results[:q.K]
	}
	return results, nil
}
