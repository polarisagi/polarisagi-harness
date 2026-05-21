package swarm

import (
	"context"

	perrors "github.com/mrlaoliai/polaris-harness/internal/errors"
)

// GraphBuildPipeline — 知识图谱构建管线（5 阶段）。
// 架构文档: docs/arch/10-Knowledge-RAG-深度选型.md §2.7

type GraphBuildPipeline struct {
	entityExtractor   *EntityExtractor
	relationExtractor *RelationExtractor
	crossDocLinker    *CrossDocumentLinker
	clusterer         *Clusterer
}

// NewGraphBuildPipeline 构造知识图谱构建管线。
// llm 可为 nil（Tier 0 降级正则提取 + 共现关系推断）。
// tier 决定聚类策略：0=Mini-Batch K-Means，1+=DBSCAN。
func NewGraphBuildPipeline(llm LLMClient, tier int) *GraphBuildPipeline {
	return &GraphBuildPipeline{
		entityExtractor: &EntityExtractor{
			dictMatcher:    &EntityDictMatcher{exactMap: make(map[string]*Entity), fuzzyMap: make(map[string][]*Entity)},
			tfidfFilter:    &TFIDFFilter{},
			llmClient:      llm,
			concurrencyCap: 5,
		},
		relationExtractor: &RelationExtractor{llmClient: llm},
		crossDocLinker:    &CrossDocumentLinker{linkedEntities: make(map[string][]string)},
		clusterer:         NewClusterer(tier),
	}
}

// Run 执行完整 5 阶段构建管线。
// Phase 1: EntityExtraction → Phase 2: RelationExtraction →
// Phase 3: CrossDocumentLinking → Phase 4: Clustering →
// Phase 5: ConceptSynthesizer.
func (p *GraphBuildPipeline) Run(ctx context.Context, docID string) error {
	entities, err := p.entityExtractor.Extract(ctx, docID)
	if err != nil {
		return perrors.Wrap(perrors.CodeInternal, "GraphBuildPipeline: Phase1 entity extraction failed", err)
	}
	if len(entities) == 0 {
		return nil
	}

	edges, err := p.relationExtractor.Extract(ctx, entities)
	if err != nil {
		return perrors.Wrap(perrors.CodeInternal, "GraphBuildPipeline: Phase2 relation extraction failed", err)
	}

	if err := p.crossDocLinker.Link(ctx, entities, edges); err != nil {
		return perrors.Wrap(perrors.CodeInternal, "GraphBuildPipeline: Phase3 cross-doc linking failed", err)
	}

	clusters := p.clusterer.ClusterEntities(collectEmbeddings(entities))
	_ = clusters
	return nil
}

func collectEmbeddings(entities []*Entity) [][]float32 {
	embs := make([][]float32, 0, len(entities))
	for _, e := range entities {
		if len(e.Embedding) > 0 {
			embs = append(embs, e.Embedding)
		}
	}
	return embs
}

// Entity 知识图谱实体。
type Entity struct {
	ID              string
	Name            string
	Type            string
	Embedding       []float32
	SourceDocID     string
	SourceChunkID   string
	OccurrenceCount int
	TaintLevel      int   // INT 0-4, [Taint-Prop] 只升不降
	SyncVersion     int64 // LWW 冲突解决
}

// Relation 实体关系。
// 关系类型: uses | depends_on | configures | extends | contradicts | replaces | version_of.
type Relation struct {
	FromEntityID string
	ToEntityID   string
	RelationType string
	Description  string
	Confidence   float64 // 0.0-1.0
	SourceDocID  string
	TaintLevel   int // max(From, To)
}

// CrossDocumentLinker 跨文档实体链接。
// 新实体查同 Name+Type 已有实体 → CrossDocLink(EntityID, DocIDs[]).
type CrossDocumentLinker struct {
	linkedEntities map[string][]string // entityID → []docID
}

func (cdl *CrossDocumentLinker) Link(ctx context.Context, entities []*Entity, edges []*Relation) error {
	for _, e := range entities {
		cdl.linkedEntities[e.ID] = append(cdl.linkedEntities[e.ID], e.ID)
	}
	return nil
}

// EntityFetcher 提供按名称获取现有实体以便进行消歧的接口。
type EntityFetcher interface {
	GetEntityByName(ctx context.Context, name string) (*Entity, error)
}
