-- ============================================================================
-- 021_plugins: 独立程序插件运行配置表
-- ============================================================================
-- 架构角色: 记录不属于 MCP 协议，且非纯文本技能的独立执行程序（如 Python/Node.js 脚本）。
-- 它们通过沙箱系统的 run_command 或自定义执行器直接调用。
-- 关联: M13-bis(Extension Registry), ADR-0019
-- ============================================================================

CREATE TABLE IF NOT EXISTS plugins (
    id          TEXT    PRIMARY KEY,
    name        TEXT    NOT NULL,
    description TEXT    NOT NULL DEFAULT '',
    entrypoint  TEXT    NOT NULL DEFAULT '',        -- 执行命令，如 "python3 main.py"
    args        TEXT    NOT NULL DEFAULT '[]',      -- 默认执行参数 (JSON array)
    env         TEXT    NOT NULL DEFAULT '{}',      -- 默认环境变量 (JSON object)
    enabled     INTEGER NOT NULL DEFAULT 1,
    timeout     INTEGER NOT NULL DEFAULT 60,        -- 执行超时（秒）
    trust_tier  INTEGER NOT NULL DEFAULT 1,         -- 信任等级 (与 taint 同步)
    catalog_id  TEXT    NOT NULL DEFAULT '',        -- 关联 extension_catalog.id
    created_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    updated_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_plugins_enabled   ON plugins(enabled);
CREATE INDEX IF NOT EXISTS idx_plugins_catalog   ON plugins(catalog_id) WHERE catalog_id != '';
CREATE INDEX IF NOT EXISTS idx_plugins_trust     ON plugins(trust_tier);
