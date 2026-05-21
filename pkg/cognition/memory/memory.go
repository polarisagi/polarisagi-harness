package memory

import (
	"context"
	"time"

	"github.com/mrlaoliai/polaris-harness/internal/protocol"
)

// Layer classifies memory into four levels per the 2026 consensus.
type Layer string

const (
	LayerWorking    Layer = "working"
	LayerEpisodic   Layer = "episodic"
	LayerSemantic   Layer = "semantic"
	LayerProcedural Layer = "procedural"
)

// TaintLevel values for memory entries. Must match policy.TaintLevel and protocol.TaintLevel.
const (
	TaintNone     = 0
	TaintLow      = 1
	TaintMedium   = 2
	TaintHigh     = 3
	TaintCritical = 4
)

// MemoryEntry is a unit of retrievable memory.
type MemoryEntry struct {
	ID          string         `json:"id"`
	Layer       Layer          `json:"layer"`
	Content     string         `json:"content"`
	Embedding   []float64      `json:"embedding,omitempty"`
	EmbedDim    int            `json:"embed_dim,omitempty"`
	OccurredAt  time.Time      `json:"occurred_at"`
	TaintLevel  int            `json:"taint_level"`
	TaintSource string         `json:"taint_source,omitempty"`
	Meta        map[string]any `json:"meta,omitempty"`
}

// MemorySystem is the four-layer memory manager.
type MemorySystem interface {
	Write(ctx context.Context, entry *MemoryEntry) error
	Retrieve(ctx context.Context, query *RetrievalQuery) ([]MemoryEntry, error)
	Consolidate(ctx context.Context) error
	Forget(ctx context.Context) (int, error)
	Mem() protocol.Memory // 返回四层 facade
}

// RetrievalQuery supports hybrid search across all layers.
type RetrievalQuery struct {
	Text      string    `json:"text"`
	Embedding []float64 `json:"embedding,omitempty"`
	EmbedDim  int       `json:"embed_dim,omitempty"`
	Layer     Layer     `json:"layer,omitempty"`
	TopK      int       `json:"top_k"`
	Strategy  string    `json:"strategy"` // "vector" | "fts" | "graph" | "hybrid"
	MaxTaint  int       `json:"max_taint,omitempty"`
}

// ============================================================================
// ImmutableCore — 写入经 M9 staging + M11 闸控
// ============================================================================

type ImmutableCore struct {
	UserPreferences map[string]string `json:"user_preferences"`
	GlobalGoal      string            `json:"global_goal"`
}

func NewImmutableCore() *ImmutableCore {
	return &ImmutableCore{
		UserPreferences: make(map[string]string),
	}
}

// ============================================================================
// MemImpl — protocol.Memory 的四层具体实现
// ============================================================================

type MemImpl struct {
	working    *WorkingMem
	episodic   *EpisodicMem
	semantic   *SemanticMem
	procedural *ProceduralMem
	retriever  *HybridRetrieverImpl
	reflection *ReflectionMem
}

func NewMemImpl(store protocol.Store) *MemImpl {
	procedural := &ProceduralMem{skills: nil}
	return &MemImpl{
		working:    NewWorkingMem(),
		episodic:   NewEpisodicMem(store),
		semantic:   NewSemanticMem(store),
		procedural: procedural,
		retriever:  NewHybridRetriever(store),
		reflection: NewReflectionMem(store),
	}
}

func (m *MemImpl) Working() protocol.WorkingMemory       { return m.working }
func (m *MemImpl) Episodic() protocol.EpisodicMemory     { return m.episodic }
func (m *MemImpl) Semantic() protocol.SemanticMemory     { return m.semantic }
func (m *MemImpl) Procedural() protocol.ProceduralMemory { return m.procedural }
func (m *MemImpl) Retriever() protocol.HybridRetriever   { return m.retriever }
func (m *MemImpl) Reflection() protocol.ReflectionMemory { return m.reflection }

func (m *MemImpl) InjectSkillRegistry(sr protocol.SkillRegistry) {
	m.procedural.skills = sr
}

// 编译期接口合规验证
var (
	_ protocol.Memory           = (*MemImpl)(nil)
	_ protocol.WorkingMemory    = (*WorkingMem)(nil)
	_ protocol.EpisodicMemory   = (*EpisodicMem)(nil)
	_ protocol.SemanticMemory   = (*SemanticMem)(nil)
	_ protocol.ProceduralMemory = (*ProceduralMem)(nil)
	_ protocol.HybridRetriever  = (*HybridRetrieverImpl)(nil)
	_ protocol.ReflectionMemory = (*ReflectionMem)(nil)
)
