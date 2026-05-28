package native

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	perrors "github.com/polarisagi/polarisagi-harness/internal/errors"
	"github.com/polarisagi/polarisagi-harness/internal/protocol"
	"github.com/polarisagi/polarisagi-harness/pkg/action"
	"github.com/polarisagi/polarisagi-harness/pkg/extensions/marketplace"
)

type searchExtensionArgs struct {
	Query string `json:"query"`
}

type installExtensionArgs struct {
	ID string `json:"id"`
}

// MakeExtensionSearchFn creates an InProcessFn for searching official extensions.
func MakeExtensionSearchFn(client *marketplace.MCPMarketplaceClient) action.InProcessFn {
	return func(ctx context.Context, input []byte) ([]byte, error) {
		if client == nil {
			return nil, perrors.New(perrors.CodeInternal, "search_extension: marketplace client is not initialized")
		}
		var args searchExtensionArgs
		if err := json.Unmarshal(input, &args); err != nil {
			return nil, perrors.Wrap(perrors.CodeInternal, "search_extension: invalid args", err)
		}

		slog.Info("native: search_extension invoked", "query", args.Query)
		results, err := client.Search(ctx, args.Query)
		if err != nil {
			return nil, perrors.Wrap(perrors.CodeInternal, "search_extension: search failed", err)
		}

		data, err := json.Marshal(results)
		if err != nil {
			return nil, perrors.Wrap(perrors.CodeInternal, "search_extension: encode results failed", err)
		}
		return data, nil
	}
}

// MakeExtensionInstallFn creates an InProcessFn for installing official extensions.
func MakeExtensionInstallFn(client *marketplace.MCPMarketplaceClient, installMgr *marketplace.Manager, hitlGateway protocol.HITL) action.InProcessFn {
	return func(ctx context.Context, input []byte) ([]byte, error) {
		if client == nil {
			return nil, perrors.New(perrors.CodeInternal, "install_extension: marketplace client is not initialized")
		}
		var args installExtensionArgs
		if err := json.Unmarshal(input, &args); err != nil {
			return nil, perrors.Wrap(perrors.CodeInternal, "install_extension: invalid args", err)
		}

		slog.Info("native: install_extension invoked", "id", args.ID)

		// Search first to get the RegistryEntry
		results, err := client.Search(ctx, args.ID)
		if err != nil || len(results) == 0 {
			return nil, perrors.Wrap(perrors.CodeInternal, "install_extension: package not found via search", err)
		}

		// Find exact match
		var target *protocol.RegistryEntry
		for i := range results {
			if results[i].ID == args.ID {
				target = &results[i]
				break
			}
		}

		if target == nil {
			return nil, perrors.New(perrors.CodeInternal, fmt.Sprintf("install_extension: exact package %q not found", args.ID))
		}

		// Security Gate check before installing
		if installMgr != nil {
			installReq := marketplace.InstallRequest{
				Principal:   "agent",
				ExtensionID: "ext_" + target.ID, // arbitrary temporary ID
				ExtType:     target.Type,
				TrustTier:   target.TrustTier,
				Publisher:   target.Publisher,
				HasHooks:    false,
			}
			if err := installMgr.InstallExtension(ctx, installReq); err != nil {
				if errors.Is(err, marketplace.ErrRequiresApproval) && hitlGateway != nil {
					_, _ = hitlGateway.Prompt(ctx, protocol.HITLPrompt{
						ID:             installReq.ExtensionID,
						CheckpointType: "security_review",
						PromptText:     "Agent requests to install extension: " + target.Name,
						Options: []protocol.HITLOption{
							{Key: "approve", Label: "Approve"},
							{Key: "deny", Label: "Deny"},
						},
					})
					return json.Marshal(map[string]string{
						"status":  "pending_approval",
						"message": "Installation suspended pending user approval. Please wait for user response.",
					})
				}
				return nil, perrors.Wrap(perrors.CodeForbidden, "install_extension: blocked by policy", err)
			}
		}

		installDir, err := client.Install(ctx, *target)
		if err != nil {
			return nil, perrors.Wrap(perrors.CodeInternal, "install_extension: install failed", err)
		}

		slog.Info("native: installed extension successfully", "id", args.ID, "dir", installDir)
		result := map[string]string{
			"status":        "success",
			"id":            args.ID,
			"installed_dir": installDir,
			"message":       "Extension installed successfully. The environment will auto-reload to expose new capabilities.",
		}
		return json.Marshal(result)
	}
}
