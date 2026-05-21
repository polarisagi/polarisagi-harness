package knowledge

import (
	"context"
	"database/sql"

	perrors "github.com/mrlaoliai/polaris-harness/internal/errors"
)

// HybridRetrieverImpl 实现 HybridRetriever。
// 生产实现：结合 FTS5（当前阶段主导）和 Dense Vector（预留）的混合检索。
type HybridRetrieverImpl struct {
	db *sql.DB
}

var _ HybridRetriever = (*HybridRetrieverImpl)(nil)

func NewHybridRetriever(db *sql.DB) *HybridRetrieverImpl {
	return &HybridRetrieverImpl{db: db}
}

// Search 执行检索。MVP 版本进行 FTS5 搜索。
func (hr *HybridRetrieverImpl) Search(ctx context.Context, query *SearchQuery) ([]Chunk, error) {
	if query.Text == "" {
		return nil, nil
	}

	// 使用 FTS5 的 MATCH 语法进行搜索，按 bm25 排序
	sqlQuery := `
		SELECT id, doc_id, content, taint_level, taint_source
		FROM rag_chunks
		WHERE rowid IN (
			SELECT rowid FROM rag_chunks_fts
			WHERE rag_chunks_fts MATCH ?
			ORDER BY rank
		)
	`
	args := []any{query.Text}

	if query.TopK > 0 {
		sqlQuery += " LIMIT ?"
		args = append(args, query.TopK)
	}

	rows, err := hr.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, perrors.Wrap(perrors.CodeInternal, "hybrid_retriever: search failed", err)
	}
	defer rows.Close()

	var results []Chunk
	for rows.Next() {
		var chunk Chunk
		var taintSource sql.NullString
		if err := rows.Scan(&chunk.ID, &chunk.DocID, &chunk.Content, &chunk.TaintLevel, &taintSource); err != nil {
			return nil, err
		}
		if taintSource.Valid {
			chunk.TaintSource = taintSource.String
		}
		results = append(results, chunk)
	}

	return results, nil
}
