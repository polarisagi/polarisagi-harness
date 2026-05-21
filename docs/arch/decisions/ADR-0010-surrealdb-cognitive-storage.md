# ADR-0010: SurrealDB-Core(Rust FFI)作为认知检索轴

- **状态**: Accepted（回填）
- **日期**: 2026-05-16
- **决策者**: 架构组
- **相关模块**: M2 / M5 / M10 / `pkg/substrate/storage/surreal_store.go`
- **实现详情**: [M02 §1.1](../M02-Storage-Fabric.md) | [00-Dict §6 Storage-SurrealDB-Core](../00-Global-Dictionary.md)
- **关联 ADR**: [ADR-0003](./ADR-0003-sqlite-modernc-primary-storage.md)(互补) | [ADR-0005](./ADR-0005-purego-ffi-cedar.md)(surreal_store.go 当前 cgo 偏离待 P3 处置)

## 上下文

cognition/swarm 需多模态检索:KV / 向量近邻(HNSW)/ 图遍历 / 全文检索 BM25。多独立引擎(Qdrant + neo4j + Elasticsearch + Redis)违反 Tier-0 单二进制。SQLite 单独无法满足(向量索引+图查询不足)。

## 决策

**采用 SurrealDB-Core(Rust 库)作为认知检索轴,经 FFI 桥接。**

职责分工(与 ADR-0003 互补):
- **SQLite (modernc/sqlite)**: EventLog / Outbox / 元数据 / FTS5 基础 — 真相源 + 强 ACID
- **SurrealDB-Core (Rust FFI)**: KV / 向量 / 图 / 高级 FTS — 认知检索轴

约束:
- Tier 0 MVP: SurrealDB-Core 纯内存(进程重启数据丢失)
- 持久化经 SQLite Outbox 投影(M02 §2.5)
- 统一经 `StorageRouter` 路由,`Store` 接口屏蔽底层差异

## 被驳与反例守护

| 方案 | 驳回理由 |
|------|---------|
| Qdrant + neo4j + Elasticsearch | 三独立进程;启动成本;跨引擎一致性;Tier-0 内存超 8GB |
| 仅 SQLite + 自建向量/图层 | 重复造轮子;HNSW 实现复杂;性能不可控 |
| 仅 BoltDB + 内存索引 | 无 SQL 表达力;图遍历需手撸 |
| 全部 Rust 重写存储层 | 增加 Rust 暴露面;Go 层失去生态(FTS5、迁移) |

**反例守护**:
- 未来如有人提议"为 X 引入 Qdrant/neo4j"—本 ADR 拒绝。多引擎依赖与 Tier-0/单二进制不兼容
- 未来如有人提议"用 SQLite 自己做向量近邻"—可作 Tier 0 暴力扫描兜底,不替代 SurrealDB-Core 主路径
