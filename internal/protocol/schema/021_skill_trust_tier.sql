-- 021: skills 表增加 trust_tier 列（ADR-0016 §2.1）
-- 替代语义模糊的 signature_valid BOOLEAN，用 0-4 五级信任枚举。
-- TrustTier: 0=Untrusted, 1=Local, 2=Community, 3=Official, 4=System
--
-- 保守迁移策略：
--   signature_valid=1 → trust_tier=2 (TrustCommunity)
--   signature_valid=0 → trust_tier=0 (TrustUntrusted)
-- 内置技能在下次启动 UPSERT 时会升级至 trust_tier=4 (TrustSystem)，无需手动处理。
ALTER TABLE skills ADD COLUMN trust_tier INTEGER NOT NULL DEFAULT 0;

UPDATE skills SET trust_tier = 2 WHERE signature_valid = 1;
UPDATE skills SET trust_tier = 0 WHERE signature_valid = 0;
