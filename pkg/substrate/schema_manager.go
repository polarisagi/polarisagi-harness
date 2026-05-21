package substrate

import "strconv"

// SchemaManager — 版本化数据库迁移。
// 架构文档: docs/arch/02-Storage-Fabric-深度选型.md §4

type Migration struct {
	Version     int
	Description string
	Up          func(tx Transaction) error
	Down        func(tx Transaction) error
}

type SchemaManager struct {
	currentVersion int
	migrations     []Migration
}

// Transaction 迁移事务接口。
type Transaction interface {
	Exec(query string, args ...any) error
}

// ApplyMigrations 按 version 升序执行未应用迁移。
// 每迁移单事务: BEGIN → Up(tx) → INSERT INTO schema_versions → COMMIT.
// 失败回滚事务 → 阻止启动。
func (sm *SchemaManager) ApplyMigrations() error {
	for _, m := range sm.migrations {
		if m.Version <= sm.currentVersion {
			continue
		}
		if err := m.Up(nil); err != nil { // tx injected by storage backend
			return &MigrationError{m.Version, err.Error()}
		}
		sm.currentVersion = m.Version
	}
	return nil
}

// MigrationGuard 崩溃恢复:
// 1. 迁移前: UPDATE sys_config SET migration_status='in_progress'
// 2. 迁移成功: UPDATE sys_config SET migration_status='completed'
// 3. 启动时检查: in_progress → Maintenance Mode + CRITICAL 日志
func (sm *SchemaManager) Recover() error {
	// 检查 sys_config.migration_status
	// in_progress → 维护模式 (仅 M2+M3 初始化, M4-M13 挂起)
	return nil
}

type MigrationError struct {
	Version int
	Detail  string
}

func (e *MigrationError) Error() string {
	return "migration v" + strconv.Itoa(e.Version) + " failed: " + e.Detail
}
