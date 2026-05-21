package memory

import (
	"context"
	"encoding/json"

	"github.com/mrlaoliai/polaris-harness/internal/protocol"
)

// ============================================================================
// SemanticMemory (L2) — 文档/实体存储
// ============================================================================

type SemanticMem struct {
	store protocol.Store
}

func NewSemanticMem(store protocol.Store) *SemanticMem {
	return &SemanticMem{store: store}
}

func (sm *SemanticMem) StoreDocument(ctx context.Context, doc protocol.Document) error {
	key := []byte("doc:" + doc.ID)
	data, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	return sm.store.Put(ctx, key, data)
}

func (sm *SemanticMem) StoreChunks(ctx context.Context, docID string, chunks []protocol.Chunk) error {
	for _, ch := range chunks {
		key := []byte("chunk:" + ch.ID)
		data, err := json.Marshal(ch)
		if err != nil {
			return err
		}
		if err := sm.store.Put(ctx, key, data); err != nil {
			return err
		}
	}
	return nil
}

func (sm *SemanticMem) GetDocument(ctx context.Context, id string) (*protocol.Document, error) {
	data, err := sm.store.Get(ctx, []byte("doc:"+id))
	if err != nil {
		return nil, err
	}
	var doc protocol.Document
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

func (sm *SemanticMem) Archive(ctx context.Context, id string, reason string) error {
	doc, err := sm.GetDocument(ctx, id)
	if err != nil {
		return err
	}
	doc.Archived = true
	return sm.StoreDocument(ctx, *doc)
}

// ============================================================================
// ProceduralMemory (L3) — 委托 M6 SkillRegistry
// ============================================================================

type ProceduralMem struct {
	skills protocol.SkillRegistry
}

func (pm *ProceduralMem) Skills() protocol.SkillRegistry {
	return pm.skills
}
