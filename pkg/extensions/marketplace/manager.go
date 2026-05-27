package marketplace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	perrors "github.com/polarisagi/polaris-harness/internal/errors"
	"github.com/polarisagi/polaris-harness/internal/protocol"
	"github.com/polarisagi/polaris-harness/pkg/substrate"
)

type InstallRequest struct {
	Principal   string
	ExtensionID string
	ExtType     string // plugin, skill, mcp
	TrustTier   int
	Publisher   string
	HasHooks    bool
}

var ErrRequiresApproval = errors.New("installation requires user approval")

type Manager struct {
	db                *sql.DB
	mcpMgr            any
	policyGate        protocol.PolicyGate
	prefsRepo         protocol.PreferencesRepo
	auditTrail        *substrate.AuditTrail
	publisherTrustMap map[string]int
}

func NewManager(db *sql.DB, mcpMgr any, pg protocol.PolicyGate, pr protocol.PreferencesRepo, at *substrate.AuditTrail, publisherTrustMap map[string]int) *Manager {
	if publisherTrustMap == nil {
		publisherTrustMap = make(map[string]int)
	}
	return &Manager{
		db:                db,
		mcpMgr:            mcpMgr,
		policyGate:        pg,
		prefsRepo:         pr,
		auditTrail:        at,
		publisherTrustMap: publisherTrustMap,
	}
}

// InstallExtension handles the install flow with M11 Cedar-Gate.
func (m *Manager) InstallExtension(ctx context.Context, req InstallRequest) error {
	mode, err := m.prefsRepo.GetPermissionMode(ctx)
	if err != nil {
		mode = protocol.ModeAutoReview
	}

	// 1. TrustTier Override based on whitelist
	if knownTier, ok := m.publisherTrustMap[req.Publisher]; ok {
		req.TrustTier = knownTier
	} else if req.TrustTier >= int(protocol.TrustOfficial) {
		req.TrustTier = int(protocol.TrustCommunity) // Downgrade self-claimed official
	}

	evalCtx := map[string]any{
		"trust_level":     req.TrustTier,
		"publisher":       req.Publisher,
		"ext_type":        req.ExtType,
		"permission_mode": string(mode),
		"has_hooks":       req.HasHooks,
	}

	reviewReq := protocol.PolicyReviewRequest{
		Principal: req.Principal,
		Action:    "install_extension",
		Resource:  req.ExtensionID,
		Context:   evalCtx,
	}

	result, err := m.policyGate.Review(ctx, reviewReq)
	if err != nil {
		return err
	}

	if result.Allowed {
		// Proceed with installation
		// (simulate DB write to extension_instances with status=installing)
		_, _ = m.db.ExecContext(ctx, "INSERT INTO extension_instances (id, status) VALUES (?, ?)", req.ExtensionID, "installing")
		// Log action (using string literal from substrate.ActionInstallApproved conceptually)
		// Assuming we emit a generic event via eventLogger:
		// m.eventLogger.AppendEvent(...)
		return nil
	}

	if strings.HasPrefix(result.Reason, "forbidden:") {
		return perrors.New(perrors.CodeForbidden, "installation forbidden: "+result.Reason)
	}

	if result.Reason == "denied by default" {
		return ErrRequiresApproval
	}

	return perrors.New(perrors.CodeForbidden, "installation denied")
}

// UninstallExtension completely removes an extension and its physical files.
//
//nolint:nestif
func (m *Manager) UninstallExtension(ctx context.Context, catalogID string) error {
	rows, err := m.db.QueryContext(ctx, "SELECT id, ext_type, runtime_id, install_path, origin FROM extension_instances WHERE catalog_id=?", catalogID)
	if err != nil {
		return err
	}
	defer rows.Close()

	type instRow struct {
		id, extType, runtimeID, installPath, origin string
	}
	var insts []instRow
	for rows.Next() {
		var inst instRow
		if err := rows.Scan(&inst.id, &inst.extType, &inst.runtimeID, &inst.installPath, &inst.origin); err == nil {
			insts = append(insts, inst)
		}
	}

	if len(insts) == 0 {
		return perrors.New(perrors.CodeNotFound, "extension not installed")
	}

	for _, inst := range insts {
		m.removeRuntime(ctx, inst.extType, inst.runtimeID, catalogID)

		if inst.installPath != "" {
			if inst.extType == "plugin" {
				var bundle protocol.PluginBundleManifest
				if raw, err := os.ReadFile(filepath.Join(inst.installPath, "plugin.json")); err == nil {
					_ = json.Unmarshal(raw, &bundle)
					if hook, ok := bundle.Hooks["uninstall"]; ok && hook != "" {
						hookPath := filepath.Join(inst.installPath, hook)
						// 简单的路径防穿越（避免逃逸出 installPath）
						if strings.HasPrefix(filepath.Clean(hookPath), filepath.Clean(inst.installPath)) {
							cmd := exec.CommandContext(ctx, hookPath)
							cmd.Dir = inst.installPath
							if err := cmd.Run(); err != nil {
								slog.Warn("marketplace: uninstall hook failed", "ext", inst.id, "err", err)
							}
						}
					}
				}
			}

			_ = os.RemoveAll(inst.installPath)
		}

		_, _ = m.db.ExecContext(ctx, "DELETE FROM extension_instances WHERE id=? OR parent_id=?", inst.id, inst.id)

		m.cleanCatalog(ctx, inst.origin, catalogID)
	}
	return nil
}

func (m *Manager) removeRuntime(ctx context.Context, extType, runtimeID, catalogID string) {
	type mcpRemover interface{ Remove(id string) }
	switch extType {
	case "mcp":
		if remover, ok := m.mcpMgr.(mcpRemover); ok && runtimeID != "" {
			remover.Remove(runtimeID)
		}
		_, _ = m.db.ExecContext(ctx, "DELETE FROM mcp_servers WHERE id=?", runtimeID)
	case "skill":
		if runtimeID != "" {
			_, _ = m.db.ExecContext(ctx, "UPDATE skills SET deprecated=1, updated_at=CURRENT_TIMESTAMP WHERE name=?", runtimeID)
		}
	case "plugin":
		_, _ = m.db.ExecContext(ctx, "DELETE FROM plugins WHERE catalog_id=?", catalogID)
	}
}

func (m *Manager) cleanCatalog(ctx context.Context, origin, catalogID string) {
	if origin == "user" {
		_, _ = m.db.ExecContext(ctx, "DELETE FROM extension_catalog WHERE id=?", catalogID)
	} else if origin == "marketplace" {
		var isBuiltin int
		err := m.db.QueryRowContext(ctx, "SELECT is_builtin FROM plugin_marketplaces WHERE id = (SELECT marketplace_id FROM extension_catalog WHERE id=?)", catalogID).Scan(&isBuiltin)
		if err == nil && isBuiltin == 0 {
			_, _ = m.db.ExecContext(ctx, "DELETE FROM extension_catalog WHERE id=?", catalogID)
		}
	}
}
