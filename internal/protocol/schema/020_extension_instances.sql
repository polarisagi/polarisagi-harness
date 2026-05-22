-- ============================================================================
-- 020_extension_instances: 统一安装实例表（Layer 1 SSoT）
-- ============================================================================
-- 架构角色: 所有已安装扩展的单一事实来源。替代旧的 skill_sources/plugins/apps 三表。
-- 三层模型:
--   Layer 0: plugin_marketplaces + registry_cache（目录层）
--   Layer 1: extension_instances（安装层，本表）
--   Layer 2: mcp_servers / skills（运行时层）
-- origin 枚举:
--   builtin    = 程序内嵌，启动 UPSERT，trust_tier=4
--   marketplace= 市场安装（catalog_id 非空），trust_tier 继承 registry_cache
--   user       = 用户手动创建，trust_tier=1
--   learned    = M9 自演化 promote，trust_tier=1
-- 关联: M13-bis(Extension Registry), ADR-0019
-- ============================================================================

CREATE TABLE IF NOT EXISTS extension_instances (
    id           TEXT    PRIMARY KEY,           -- "ext_{8字节hex}"
    ext_type     TEXT    NOT NULL,              -- 'mcp' | 'skill' | 'plugin' | 'app'
    origin       TEXT    NOT NULL,              -- 'builtin' | 'marketplace' | 'user' | 'learned'
    catalog_id   TEXT    NOT NULL DEFAULT '',   -- registry_cache.id；user/learned 时为空
    name         TEXT    NOT NULL,
    publisher    TEXT    NOT NULL DEFAULT '',
    trust_tier   INTEGER NOT NULL DEFAULT 0,
    enabled      INTEGER NOT NULL DEFAULT 1,
    runtime_id   TEXT    NOT NULL DEFAULT '',   -- mcp_servers.id 或 skills.name；安装后写入
    install_path TEXT    NOT NULL DEFAULT '',   -- 文件系统绝对路径；MCP/App 为空字符串
    config       TEXT    NOT NULL DEFAULT '{}', -- JSON：覆盖参数（env/args/entrypoint/url）
    status       TEXT    NOT NULL DEFAULT 'installed', -- 'downloading'|'installed'|'error'|'disabled'
    error_msg    TEXT    NOT NULL DEFAULT '',
    parent_id    TEXT    NOT NULL DEFAULT '',   -- plugin bundle 子记录指向父记录 id
    created_at   TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    updated_at   TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_ext_type   ON extension_instances(ext_type);
CREATE INDEX IF NOT EXISTS idx_ext_origin ON extension_instances(origin);
CREATE INDEX IF NOT EXISTS idx_ext_status ON extension_instances(status);
CREATE INDEX IF NOT EXISTS idx_ext_parent ON extension_instances(parent_id) WHERE parent_id != '';
CREATE INDEX IF NOT EXISTS idx_ext_catalog ON extension_instances(catalog_id) WHERE catalog_id != '';
-- 同一 catalog 条目只允许安装一次（顶级记录，非 bundle 子记录）
CREATE UNIQUE INDEX IF NOT EXISTS uniq_ext_catalog
    ON extension_instances(catalog_id) WHERE catalog_id != '' AND parent_id = '';
