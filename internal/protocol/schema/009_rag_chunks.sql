-- M10 Knowledge RAG DDL
-- 记录知识库文档块与 FTS 索引

CREATE TABLE IF NOT EXISTS rag_chunks (
    id              TEXT PRIMARY KEY,
    doc_id          TEXT NOT NULL,
    content         TEXT NOT NULL,
    taint_level     INTEGER NOT NULL DEFAULT 1,
    taint_source    TEXT,
    created_at      TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- 使用 FTS5 建立全文检索
CREATE VIRTUAL TABLE IF NOT EXISTS rag_chunks_fts USING fts5(
    content,
    content='rag_chunks',
    content_rowid='rowid'
);

-- 触发器：自动维护 FTS 索引
CREATE TRIGGER IF NOT EXISTS rag_chunks_ai AFTER INSERT ON rag_chunks BEGIN
  INSERT INTO rag_chunks_fts(rowid, content) VALUES (new.rowid, new.content);
END;

CREATE TRIGGER IF NOT EXISTS rag_chunks_ad AFTER DELETE ON rag_chunks BEGIN
  INSERT INTO rag_chunks_fts(rag_chunks_fts, rowid, content) VALUES('delete', old.rowid, old.content);
END;

CREATE TRIGGER IF NOT EXISTS rag_chunks_au AFTER UPDATE ON rag_chunks BEGIN
  INSERT INTO rag_chunks_fts(rag_chunks_fts, rowid, content) VALUES('delete', old.rowid, old.content);
  INSERT INTO rag_chunks_fts(rowid, content) VALUES (new.rowid, new.content);
END;

CREATE INDEX IF NOT EXISTS idx_rag_chunks_doc_id ON rag_chunks(doc_id);
