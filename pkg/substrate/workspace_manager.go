package substrate

// WorkspaceManager — 重型中间物文件系统。
// 架构文档: docs/arch/02-Storage-Fabric-深度选型.md §3

type WorkspaceManager struct {
	rootDir   string // ~/.polaris-harness/workspaces
	maxSize   int64  // Tier 0 = 500MB
	manifests map[string]*WorkspaceManifest
}

// NewWorkspaceManager 创建 WorkspaceManager.
func NewWorkspaceManager(rootDir string, maxSize int64) *WorkspaceManager {
	return &WorkspaceManager{
		rootDir:   rootDir,
		maxSize:   maxSize,
		manifests: make(map[string]*WorkspaceManifest),
	}
}

// GetRootDir 返回工作区根目录。
func (wm *WorkspaceManager) GetRootDir() string {
	return wm.rootDir
}

type WorkspaceManifest struct {
	TaskID    int64
	CreatedAt int64
	Files     []WorkspaceFile
	TotalSize int64
}

type WorkspaceFile struct {
	Path        string
	Size        int64
	Summary     string // ~50 字
	ContentType string
}

// CheckQuota 写入前检查配额。
// workspace_write 前 du 累积占用量 + 待写入 > maxSize → ErrQuotaExhausted
func (wm *WorkspaceManager) CheckQuota(pendingWrite int64) error {
	var total int64
	for _, m := range wm.manifests {
		total += m.TotalSize
	}
	if total+pendingWrite > wm.maxSize {
		return ErrWorkspaceQuotaExhausted
	}
	return nil
}

// GC 回收 > 7 天且关联 Task 为终态的 workspace。
// 回收策略: 按 CreatedAt 升序 → 确认 Task Status∈{Done,Failed} → os.RemoveAll → 释放至 < maxSize×0.7.
func (wm *WorkspaceManager) GC(now int64) {
	for id, m := range wm.manifests {
		if now-m.CreatedAt > 7*86400 {
			delete(wm.manifests, id)
		}
	}
}

var ErrWorkspaceQuotaExhausted = &WorkspaceError{"workspace quota exhausted"}

type WorkspaceError struct{ msg string }

func (e *WorkspaceError) Error() string { return e.msg }
