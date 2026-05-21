package swarm

import (
	"sync"
	"sync/atomic"
	"time"
)

// Blackboard — 多 Agent 协调黑板。
// 架构文档: docs/arch/08-Multi-Agent-Orchestrator-深度选型.md §1

type TaskStatus int32

const (
	TaskPending TaskStatus = iota
	TaskClaimed
	TaskExecuting
	TaskSuspended
	TaskCompensating
	TaskDone
	TaskFailed
)

// TaskEntry 是黑板上的一项任务。
// Version 单调递增防 ABA 竞态，Status 严格单调不可回退。
type TaskEntry struct {
	ID         string
	Type       string
	Priority   int
	Version    atomic.Int32
	Status     atomic.Int32
	ClaimedBy  atomic.Pointer[string]
	ClaimedAt  int64
	ExpiresAt  int64
	RenewCount int32
	Toxicity   atomic.Int32
	Intent     []byte
	Result     []byte
	Artifacts  []string
	DependsOn  []string
	SubTasks   []string
	Deadline   int64
	CreatedAt  int64
	UpdatedAt  int64
}

// Blackboard 协调黑板实现。
// 常量: DefaultLeaseTTL=60s, HeartbeatInterval=15s(±5s jitter), ReaperScanInterval=1s.
type Blackboard struct {
	mu           sync.RWMutex
	tasks        map[string]*TaskEntry
	events       chan BlackboardEvent
	agents       map[string]*AgentHandle
	backpressure atomic.Bool
	epoch        int64
}

// GetEpoch 返回当前的 Orchestrator Epoch.
func (b *Blackboard) GetEpoch() int64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.epoch
}

// SetEpoch 设置当前的 Orchestrator Epoch.
func (b *Blackboard) SetEpoch(e int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.epoch = e
}

// checkBackpressure 检查 events channel 容量，>80% 时开启 backpressure，<50% 解除。
func (b *Blackboard) checkBackpressure() {
	capEvent := float64(cap(b.events))
	lenEvent := float64(len(b.events))
	if capEvent == 0 {
		return
	}
	if lenEvent > capEvent*0.8 {
		b.backpressure.Store(true)
	} else if lenEvent < capEvent*0.5 {
		b.backpressure.Store(false)
	}
}

// BlackboardEvent 黑板事件。
type BlackboardEvent struct {
	Type      string
	TaskID    string
	AgentID   string
	Payload   []byte
	Timestamp int64
}

// AgentHandle Agent 句柄。
type AgentHandle struct {
	Card         AgentCard
	Handle       any // 本地 chan 或远程 A2A gRPC
	RegisteredAt int64
	Status       string // active | inactive | unreachable
}

// AgentCard Agent 能力声明（A2A v0.3 兼容）。
type AgentCard struct {
	Name          string
	Version       string
	Description   string
	Skills        []string
	Tools         []string
	Models        []string
	MaxConcurrent int
	TrustLevel    int
	SandboxTier   int
	Endpoint      string
}

// NewBlackboard creates a new Blackboard.
func NewBlackboard() *Blackboard {
	return &Blackboard{
		tasks:  make(map[string]*TaskEntry),
		events: make(chan BlackboardEvent, 1024),
		agents: make(map[string]*AgentHandle),
	}
}

// PostTask posts a new task to the blackboard.
func (b *Blackboard) PostTask(entry *TaskEntry) error {
	b.checkBackpressure()
	if b.backpressure.Load() {
		return ErrBackpressure
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now().Unix()
	entry.CreatedAt = now
	entry.UpdatedAt = now
	entry.Status.Store(int32(TaskPending))
	entry.Version.Store(0)

	b.tasks[entry.ID] = entry
	select {
	case b.events <- BlackboardEvent{
		Type:      "task_posted",
		TaskID:    entry.ID,
		Timestamp: now,
	}:
	default:
		// Queue full, drop event
	}
	return nil
}

// PostBatch posts multiple tasks to the blackboard atomically.
func (b *Blackboard) PostBatch(entries []*TaskEntry) error {
	b.checkBackpressure()
	if b.backpressure.Load() {
		return ErrBackpressure
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now().Unix()
	for _, entry := range entries {
		entry.CreatedAt = now
		entry.UpdatedAt = now
		entry.Status.Store(int32(TaskPending))
		entry.Version.Store(0)
		b.tasks[entry.ID] = entry
		select {
		case b.events <- BlackboardEvent{
			Type:      "task_posted",
			TaskID:    entry.ID,
			Timestamp: now,
		}:
		default:
		}
	}
	return nil
}

// StartExecution transitions a claimed task to executing status.
func (b *Blackboard) StartExecution(taskID string, agentID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	entry, ok := b.tasks[taskID]
	if !ok {
		return ErrTaskNotFound
	}
	if entry.ClaimedBy.Load() == nil || *entry.ClaimedBy.Load() != agentID {
		return ErrStaleLease
	}
	if entry.Status.Load() != int32(TaskClaimed) {
		return ErrStaleLease
	}

	entry.Status.Store(int32(TaskExecuting))
	entry.UpdatedAt = time.Now().Unix()
	return nil
}

// CompleteTask marks a task as done.
func (b *Blackboard) CompleteTask(taskID string, agentID string, result []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	entry, ok := b.tasks[taskID]
	if !ok {
		return ErrTaskNotFound
	}
	if entry.ClaimedBy.Load() == nil || *entry.ClaimedBy.Load() != agentID {
		return ErrStaleLease
	}

	entry.Result = result
	entry.Status.Store(int32(TaskDone))
	entry.UpdatedAt = time.Now().Unix()

	b.events <- BlackboardEvent{
		Type:      "task_completed",
		TaskID:    taskID,
		AgentID:   agentID,
		Payload:   result,
		Timestamp: entry.UpdatedAt,
	}
	return nil
}

// FailTask marks a task as failed.
func (b *Blackboard) FailTask(taskID string, agentID string, errBytes []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	entry, ok := b.tasks[taskID]
	if !ok {
		return ErrTaskNotFound
	}
	if entry.ClaimedBy.Load() == nil || *entry.ClaimedBy.Load() != agentID {
		return ErrStaleLease
	}

	entry.Result = errBytes
	entry.Status.Store(int32(TaskFailed))
	entry.UpdatedAt = time.Now().Unix()

	b.events <- BlackboardEvent{
		Type:      "task_failed",
		TaskID:    taskID,
		AgentID:   agentID,
		Payload:   errBytes,
		Timestamp: entry.UpdatedAt,
	}
	return nil
}

// Claim CAS 原子认领：仅 ClaimedBy==nil 可认领。
func (b *Blackboard) Claim(taskID, agentID string) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	entry, ok := b.tasks[taskID]
	if !ok {
		return false, nil
	}

	if entry.ClaimedBy.Load() != nil {
		return false, nil
	}

	now := time.Now().Unix()
	if entry.Status.Load() != int32(TaskPending) {
		return false, nil
	}

	entry.ClaimedBy.Store(&agentID)
	entry.ClaimedAt = now
	entry.ExpiresAt = now + 60 // 60s TTL
	entry.Status.Store(int32(TaskClaimed))
	entry.Version.Add(1)

	b.events <- BlackboardEvent{
		Type:      "task_claimed",
		TaskID:    taskID,
		AgentID:   agentID,
		Timestamp: now,
	}

	return true, nil
}

// RenewLease 续期租约（控制平面带外，不走 events chan）。
func (b *Blackboard) RenewLease(taskID, agentID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	entry, ok := b.tasks[taskID]
	if !ok {
		return nil
	}

	if entry.ClaimedBy.Load() == nil || *entry.ClaimedBy.Load() != agentID {
		return nil
	}

	entry.ExpiresAt = time.Now().Unix() + 60
	entry.RenewCount++
	return nil
}

// SideEffectPreCheck 每 [M7-Tool] ExecuteTool 前强制执行。
func (b *Blackboard) SideEffectPreCheck(taskID, agentID string, claimedVersion int32) error {
	b.mu.RLock()
	defer b.mu.RUnlock()

	entry, ok := b.tasks[taskID]
	if !ok {
		return ErrTaskNotFound
	}

	if entry.ClaimedBy.Load() == nil || *entry.ClaimedBy.Load() != agentID {
		return ErrStaleLease
	}

	if entry.Version.Load() != claimedVersion {
		return ErrStaleLease
	}

	if time.Now().Unix() > entry.ExpiresAt {
		return ErrStaleLease
	}

	if entry.Status.Load() != int32(TaskExecuting) {
		return ErrStaleLease
	}

	return nil
}

var (
	ErrTaskNotFound = &BlackboardError{"task not found"}
	ErrStaleLease   = &BlackboardError{"stale lease"}
	ErrBackpressure = &BlackboardError{"backpressure active"}
)

type BlackboardError struct{ msg string }

func (e *BlackboardError) Error() string { return e.msg }

// TaskSnapshot Task 状态只读快照（避免拷贝含原子字段的 TaskEntry）。
type TaskSnapshot struct {
	ID     string
	Status TaskStatus
	Result []byte
}

// PeekTask 返回 Task 状态快照（只读，不修改 Task 状态）。
// 用于 CSV fan-out 轮询等场景。Task 不存在时返回 nil, nil。
func (b *Blackboard) PeekTask(taskID string) (*TaskSnapshot, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	entry, ok := b.tasks[taskID]
	if !ok {
		return nil, nil
	}
	return &TaskSnapshot{
		ID:     entry.ID,
		Status: TaskStatus(entry.Status.Load()),
		Result: entry.Result,
	}, nil
}
