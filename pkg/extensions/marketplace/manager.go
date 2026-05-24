package marketplace

import (
	"context"
	"strings"

	perrors "github.com/mrlaoliai/polaris-harness/internal/errors"
	"github.com/mrlaoliai/polaris-harness/internal/protocol"
)

type DB interface {
	ExecContext(ctx context.Context, query string, args ...any) (any, error)
	QueryContext(ctx context.Context, query string, args ...any) (any, error)
	QueryRowContext(ctx context.Context, query string, args ...any) any
}

type InstallRequest struct {
	Principal   string
	ExtensionID string
	ExtType     string // plugin, skill, mcp
	TrustTier   int
	Publisher   string
	HasHooks    bool
}

type Manager struct {
	db          DB
	mcpMgr      any
	policyGate  protocol.PolicyGate
	prefsRepo   protocol.PreferencesRepo
	eventLogger protocol.EventLogger // typically AuditTrail
}

func NewManager(db DB, mcpMgr any, pg protocol.PolicyGate, pr protocol.PreferencesRepo, el protocol.EventLogger) *Manager {
	return &Manager{
		db:          db,
		mcpMgr:      mcpMgr,
		policyGate:  pg,
		prefsRepo:   pr,
		eventLogger: el,
	}
}

// InstallExtension handles the install flow with M11 Cedar-Gate.
func (m *Manager) InstallExtension(ctx context.Context, req InstallRequest) error {
	mode, err := m.prefsRepo.GetPermissionMode(ctx)
	if err != nil {
		mode = protocol.ModeAutoReview
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
		// Hard Reject
		return perrors.New(perrors.CodeInternal, "installation forbidden: "+result.Reason)
	}

	if result.Reason == "denied by default" {
		// Soft Deny -> Require HITL
		_, _ = m.db.ExecContext(ctx, "INSERT INTO extension_instances (id, status) VALUES (?, ?)", req.ExtensionID, "pending_approval")
		return perrors.New(perrors.CodeInternal, "installation requires user approval")
	}

	return perrors.New(perrors.CodeInternal, "installation denied")
}
