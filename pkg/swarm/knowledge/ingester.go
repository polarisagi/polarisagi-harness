package knowledge

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	perrors "github.com/mrlaoliai/polaris-harness/internal/errors"
)

// PipelineImpl 实现 IngestionPipeline (M10 Knowledge RAG 摄取管道)。
// 生产实现: 将文本分块，并落盘到 SQLite rag_chunks 表以支持 FTS。
type PipelineImpl struct {
	db *sql.DB
}

var _ IngestionPipeline = (*PipelineImpl)(nil)

func NewPipeline(db *sql.DB) *PipelineImpl {
	return &PipelineImpl{db: db}
}

// Ingest 将文档转换为 Chunk 并持久化。
func (p *PipelineImpl) Ingest(ctx context.Context, doc *Document, initialTaint int) (*DocTree, error) {
	if doc == nil {
		return nil, perrors.New(perrors.CodeInvalidInput, "knowledge: doc is nil")
	}
	content := string(doc.Raw)
	// 简单的基于双换行符的分段
	parts := strings.Split(content, "\n\n")

	var nodes []*DocNode //nolint:prealloc

	for i, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		chunkID := fmt.Sprintf("%s_chunk_%d", doc.Ref.ContentHash, i)

		nodes = append(nodes, &DocNode{
			ID:      chunkID,
			Title:   fmt.Sprintf("Chunk %d", i),
			Level:   1,
			Content: part,
		})

		// 持久化 Chunk 到 SQLite rag_chunks 表
		_, err := p.db.ExecContext(ctx, `
			INSERT INTO rag_chunks (id, doc_id, content, taint_level, taint_source)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				content=excluded.content,
				taint_level=excluded.taint_level,
				taint_source=excluded.taint_source,
				created_at=CURRENT_TIMESTAMP
		`, chunkID, doc.Ref.URI, part, initialTaint, "")
		if err != nil {
			return nil, perrors.Wrap(perrors.CodeInternal, "ingester: insert chunk failed", err)
		}
	}

	tree := &DocTree{
		Document: &DocNode{
			ID:       doc.Ref.URI,
			Title:    doc.Ref.Title,
			Level:    0,
			Children: nodes,
		},
		SourceURL: doc.Ref.URI,
	}

	// MVP 阶段: DocTree 暂不持久化或可作为 JSON 存入另一个表
	// 真实系统可以将 DocTree 信息落入 rag_docs 表

	return tree, nil
}

// Delete 删除指定文档的所有片段。
func (p *PipelineImpl) Delete(ctx context.Context, uri string) error {
	_, err := p.db.ExecContext(ctx, "DELETE FROM rag_chunks WHERE doc_id = ?", uri)
	return err
}
