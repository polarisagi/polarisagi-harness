-- ============================================================================
-- 005_workspace_vfs: WorkspaceManager VFS 引用计数 + 热表 B-Tree 页保护
-- ============================================================================
-- 架构角色: 将 >4KB 的热表载荷从 B-Tree 页中剥离至独立文件系统存储，
--           热表仅保留 vfs_ref 引用指针。防止 B-Tree 溢出页导致 Page Cache 血崩。
-- 生产者:    M2 WorkspaceManager（写入时创建 VFS 文件 + 写入引用记录）、
--           M7 Workspace Bridge（Wasm 沙箱经宿主侧写入 workspace）
-- 消费者:    M2 WorkspaceManager（GC 回收）、SQLite Trigger（级联删除时自动递减引用计数）
-- 不变量:
--   1. >4KB 热行载荷不入 B-Tree 页，写入 VFS 文件，热表仅存 vfs_ref 指针（4KB 硬限 CI 强制执行）
--   2. BEFORE DELETE trigger 自动递减引用计数，归零入队 GC
--   3. workspace_write 仅允许白名单路径（三类）:
--      (a) ~/.polaris-harness/workspaces/<task_id>/（M2 WorkspaceManager 托管）
--      (b) /tmp/sandbox/{skill_id}/（经 [Sandbox-L2] 显式挂载的临时目录）
--      (c) 启动时传入的 Workspace Root（用户项目根目录），需经 Cedar-Gate 显式授权
--   4. 禁止覆盖保护: 即使白名单内路径，仍禁止覆盖 ~/.polaris-harness/config/、secrets/、data/、audit/
--   5. Taint Gate（两级分流）:
--      目标在 (c) Workspace Root: [TaintLevel]<=[Taint-Medium]→允许; [TaintLevel]>=[Taint-High]→仅 ephemeral
--      目标不在 (c): [TaintLevel]<=[Taint-Low]→允许; [TaintLevel]>=[Taint-Medium]→仅 ephemeral
-- 写入路径: vfs_ref 引用计数变更走 MutationBus；VFS 文件系统操作走 WorkspaceManager
-- 关联模块: M2(Storage) §3, M7(Tool) §4.5, M11(Policy) §2.5
-- ============================================================================

CREATE TABLE IF NOT EXISTS sys_vfs_references (
    vfs_ref   TEXT PRIMARY KEY,
    -- ↑ VFS 文件引用标识。格式: vfs://{sha256_prefix}/{uuid}.blob。
    --   sha256_prefix 用于目录分桶，避免单目录文件数爆炸。

    ref_count INTEGER NOT NULL DEFAULT 1,
    -- ↑ 引用计数。每条引用此 VFS 文件的热表行贡献 1。归零时入队 GC 物理删除文件。
    --   UPDATE sys_vfs_references SET ref_count = ref_count + 1 WHERE vfs_ref = ? 原子累加，
    --   严禁先读后写（防止并发引用计数撕裂）。

    created_at INTEGER NOT NULL,
    -- ↑ VFS 文件创建时间（Unix 毫秒）。

    blob_size  INTEGER NOT NULL
    -- ↑ VFS 文件实际大小（字节）。用于 M3 CGO 内存估算和 GC 优先级排序。
);

-- ----------------------------------------------------------------------------
-- schema_versions: Schema 版本迁移管理
-- ----------------------------------------------------------------------------
-- 架构角色: 记录每次 DDL 迁移的版本号和校验和。SchemaManager 启动时查询 max(version)
--           确定当前 schema 版本，然后执行未应用的迁移。
-- 生产者:    M2 SchemaManager（迁移执行后写入）
-- 消费者:    M2 SchemaManager（启动时读取）
-- ============================================================================

CREATE TABLE IF NOT EXISTS schema_versions (
    version    INTEGER PRIMARY KEY,
    -- ↑ Schema 版本号，单调递增。对应 migrations 目录中文件名前缀。

    applied_at TEXT NOT NULL,
    -- ↑ 迁移执行时间（ISO 8601 格式）。

    checksum   TEXT NOT NULL
    -- ↑ 迁移 SQL 文件的 SHA-256 校验和，用于检测文件篡改。
);

-- ----------------------------------------------------------------------------
-- sys_config: 系统运行时配置（MigrationGuard 崩溃恢复 + 运行时动态配置）
-- ----------------------------------------------------------------------------
-- 架构角色: 键值对存储系统级配置。SchemaManager 迁移前写入 migration_status
--           和 migration_version_target 用于崩溃恢复。
-- 生产者:    M2 SchemaManager、M13 CLI（polaris config set）
-- 消费者:    全模块（启动时读取各自关心的配置项）
-- 写入路径:  仅 SchemaManager 维护模式豁免 —— 可直接写 sys_config。
--           运行时配置变更走 MutationBus。
-- ============================================================================

CREATE TABLE IF NOT EXISTS sys_config (
    key   TEXT PRIMARY KEY,
    -- ↑ 配置键。如 'migration_status' | 'orchestrator_epoch' | 'min_skill_version'。

    value TEXT NOT NULL
    -- ↑ 配置值。纯文本，无类型约束——各模块自行解析和校验。
);
