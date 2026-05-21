package skill

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	perrors "github.com/mrlaoliai/polaris-harness/internal/errors"
	"github.com/mrlaoliai/polaris-harness/internal/protocol"
)

// SQLiteRegistryImpl 实现了持久化的技能注册表，基于 SQLite。
// 并发写入通过 SQLite 事务隔离保证。
type SQLiteRegistryImpl struct {
	db *sql.DB
}

func NewSQLiteRegistry(db *sql.DB) *SQLiteRegistryImpl {
	return &SQLiteRegistryImpl{db: db}
}

var _ protocol.SkillRegistry = (*SQLiteRegistryImpl)(nil)

// Register 插入或更新技能元数据。
func (r *SQLiteRegistryImpl) Register(ctx context.Context, meta protocol.SkillMeta) error {
	if meta.Trust < protocol.TrustLocal {
		return errCosignVerifyFailed
	}
	if !strings.HasPrefix(meta.Name, "skill:") {
		return perrors.Wrap(perrors.CodeInternal, fmt.Sprintf("skill name error: got %s", meta.Name), errInvalidSkillName)
	}

	capsBytes, _ := json.Marshal(meta.Capabilities)
	benchBytes, _ := json.Marshal(meta.Benchmarks)

	// SQLite upsert：写 trust_tier（新列），同时向后兼容写 signature_valid
	query := `
		INSERT INTO skills (
			name, version, runtime, risk_level, sandbox, capabilities,
			signature_valid, trust_tier, idempotent, benchmarks, deprecated, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(name) DO UPDATE SET
			version=excluded.version,
			runtime=excluded.runtime,
			risk_level=excluded.risk_level,
			sandbox=excluded.sandbox,
			capabilities=excluded.capabilities,
			signature_valid=excluded.signature_valid,
			trust_tier=excluded.trust_tier,
			idempotent=excluded.idempotent,
			benchmarks=excluded.benchmarks,
			deprecated=excluded.deprecated,
			updated_at=CURRENT_TIMESTAMP
	`
	sigValid := meta.Trust >= protocol.TrustLocal // 向后兼容
	_, err := r.db.ExecContext(ctx, query,
		meta.Name, meta.Version, meta.Runtime, meta.RiskLevel, meta.Sandbox,
		string(capsBytes), sigValid, int(meta.Trust), meta.Idempotent, string(benchBytes), meta.Deprecated,
	)
	if err != nil {
		return perrors.Wrap(perrors.CodeInternal, "sqlite_registry: insert failed", err)
	}

	return nil
}

func (r *SQLiteRegistryImpl) Get(ctx context.Context, name, version string) (*protocol.SkillMeta, error) {
	// 读取 trust_tier（新列）；若迁移前列不存在则回退到 signature_valid 派生
	query := `
		SELECT name, version, runtime, risk_level, sandbox, capabilities,
		       trust_tier, idempotent, benchmarks, deprecated
		FROM skills WHERE name = ?
	`
	args := []any{name}
	if version != "" {
		query += " AND version = ?"
		args = append(args, version)
	}

	row := r.db.QueryRowContext(ctx, query, args...)

	var meta protocol.SkillMeta
	var capsRaw, benchRaw string
	var trustInt int
	err := row.Scan(
		&meta.Name, &meta.Version, &meta.Runtime, &meta.RiskLevel, &meta.Sandbox,
		&capsRaw, &trustInt, &meta.Idempotent, &benchRaw, &meta.Deprecated,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errSkillNotFound
		}
		return nil, perrors.Wrap(perrors.CodeInternal, "sqlite_registry: get failed", err)
	}
	meta.Trust = protocol.TrustTier(trustInt)

	json.Unmarshal([]byte(capsRaw), &meta.Capabilities) //nolint:errcheck
	json.Unmarshal([]byte(benchRaw), &meta.Benchmarks)  //nolint:errcheck

	return &meta, nil
}

func (r *SQLiteRegistryImpl) List(ctx context.Context, filter protocol.SkillFilter) ([]protocol.SkillMeta, error) {
	query := `
		SELECT name, version, runtime, risk_level, sandbox, capabilities,
		       trust_tier, idempotent, benchmarks, deprecated
		FROM skills WHERE 1=1
	`
	var args []any

	if !filter.IncludeDeprecated {
		query += " AND deprecated = 0"
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, perrors.Wrap(perrors.CodeInternal, "sqlite_registry: list query failed", err)
	}
	defer rows.Close()

	var result []protocol.SkillMeta
	for rows.Next() {
		var meta protocol.SkillMeta
		var capsRaw, benchRaw string
		var trustInt int
		if err := rows.Scan(
			&meta.Name, &meta.Version, &meta.Runtime, &meta.RiskLevel, &meta.Sandbox,
			&capsRaw, &trustInt, &meta.Idempotent, &benchRaw, &meta.Deprecated,
		); err != nil {
			return nil, err
		}
		meta.Trust = protocol.TrustTier(trustInt)
		json.Unmarshal([]byte(capsRaw), &meta.Capabilities) //nolint:errcheck
		json.Unmarshal([]byte(benchRaw), &meta.Benchmarks)  //nolint:errcheck

		// 内存级二次过滤
		if filter.RiskLevelMax != "" && riskGT(meta.RiskLevel, filter.RiskLevelMax) {
			continue
		}
		if len(filter.Capabilities) > 0 && !hasCapability(meta.Capabilities, filter.Capabilities) {
			continue
		}

		result = append(result, meta)
	}
	return result, nil
}

func (r *SQLiteRegistryImpl) Deprecate(ctx context.Context, name, version string, reason string) error {
	query := "UPDATE skills SET deprecated = 1, updated_at = CURRENT_TIMESTAMP WHERE name = ?"
	args := []any{name}
	if version != "" {
		query += " AND version = ?"
		args = append(args, version)
	}
	res, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return perrors.Wrap(perrors.CodeInternal, "sqlite_registry: deprecate failed", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return errSkillNotFound
	}
	return nil
}
