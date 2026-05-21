-- ============================================================================
-- 016_fts5_search: chat_messages 全文检索虚拟表（SQLite FTS5）
-- 依赖：014_chat_sessions（chat_messages 表必须先存在）
-- ============================================================================

-- FTS5 content 表：只存索引，实际内容读取走 chat_messages（content= 模式）
CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
    content,
    content=chat_messages,
    content_rowid=id,
    tokenize='unicode61'
);

-- 填充已有数据（仅用户和 AI 消息，系统消息跳过）
INSERT OR IGNORE INTO messages_fts(rowid, content)
SELECT id, content FROM chat_messages WHERE role IN ('user', 'assistant');

-- 新消息自动同步到 FTS
CREATE TRIGGER IF NOT EXISTS fts_insert
AFTER INSERT ON chat_messages
BEGIN
    INSERT INTO messages_fts(rowid, content) VALUES (new.id, new.content);
END;

-- 删除消息时同步从 FTS 移除
CREATE TRIGGER IF NOT EXISTS fts_delete
AFTER DELETE ON chat_messages
BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, content)
    VALUES ('delete', old.id, old.content);
END;

-- 更新消息时同步更新 FTS（先删后插）
CREATE TRIGGER IF NOT EXISTS fts_update
AFTER UPDATE ON chat_messages
BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, content)
    VALUES ('delete', old.id, old.content);
    INSERT INTO messages_fts(rowid, content) VALUES (new.id, new.content);
END;
