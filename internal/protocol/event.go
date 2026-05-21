package protocol

import "time"

// EventType enumerates structured coordination event kinds for the blackboard.
type EventType string

const (
	EventIntent        EventType = "intent"
	EventClaim         EventType = "claim"
	EventResult        EventType = "result"
	EventFail          EventType = "fail"
	EventCancel        EventType = "cancel"
	EventHeartbeat     EventType = "heartbeat"
	EventActionPending EventType = "action_pending"
	EventActionDone    EventType = "action_done"
)

// EventStatus tracks lifecycle.
type EventStatus string

const (
	StatusPending   EventStatus = "pending"
	StatusClaimed   EventStatus = "claimed"
	StatusExecuting EventStatus = "executing"
	StatusDone      EventStatus = "done"
	StatusFailed    EventStatus = "failed"
)

// OutboxEvent is a row in the outbox table — the single source of truth for
// all async projections (graph build, vector index, skill deploy, event dispatch).
//
// The id column MUST be an AUTOINCREMENT integer to guarantee monotonic physical
// write order. UUIDv7 is broken for cursor polling because its random suffix
// causes lexicographic inversion under same-millisecond concurrent inserts.
//
// Worker polling uses: SELECT * FROM outbox WHERE id > :cursor AND committed_at > :last_scan
// The committed_at guard handles the case where an uncommitted row causes
// AUTOINCREMENT to skip a value before commit.
type OutboxEvent struct {
	ID          int64        `json:"id"`         // AUTOINCREMENT, monotonic
	EventID     string       `json:"event_id"`   // logical Event.ID
	EventType   string       `json:"event_type"` // "graph_build" | "vector_index" | "skill_deploy"
	Payload     []byte       `json:"payload"`
	CommittedAt int64        `json:"committed_at"` // unix nano, set on INSERT
	ClaimedBy   string       `json:"claimed_by,omitempty"`
	RetryCount  int          `json:"retry_count"`
	MaxRetries  int          `json:"max_retries"`
	Status      OutboxStatus `json:"status"`
}

type OutboxStatus string

const (
	OutboxPending OutboxStatus = "pending"
	OutboxClaimed OutboxStatus = "claimed"
	OutboxDone    OutboxStatus = "done"
	OutboxDead    OutboxStatus = "dead" // exceeded MaxRetries
)

// Event is the unit of structured coordination on the blackboard.
// Natural language content goes in Payload; coordination metadata is typed.
type Event struct {
	ID        string        `json:"id"`
	Type      EventType     `json:"type"`
	Status    EventStatus   `json:"status"`
	TaskID    string        `json:"task_id"`
	AgentID   string        `json:"agent_id,omitempty"`
	Payload   []byte        `json:"payload,omitempty"`
	CreatedAt time.Time     `json:"created_at"`
	TTL       time.Duration `json:"ttl,omitempty"`
}
