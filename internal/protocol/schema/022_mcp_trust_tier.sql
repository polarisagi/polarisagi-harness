-- 022: mcp_servers 表增加 trust_tier 列（ADR-0016 §2.1）
-- 官方推荐安装（catalog_id 非空）自动升级至 TrustOfficial(3)；其余默认 TrustCommunity(2)。
ALTER TABLE mcp_servers ADD COLUMN trust_tier INTEGER NOT NULL DEFAULT 2;

-- 从 Catalog 安装的视为 TrustOfficial（官方推荐）
UPDATE mcp_servers
   SET trust_tier = 3
 WHERE catalog_id IS NOT NULL AND catalog_id != '';
