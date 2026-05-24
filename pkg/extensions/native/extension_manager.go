package native

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	perrors "github.com/mrlaoliai/polaris-harness/internal/errors"
	"github.com/mrlaoliai/polaris-harness/internal/protocol"
	"github.com/mrlaoliai/polaris-harness/pkg/action"
	"github.com/mrlaoliai/polaris-harness/pkg/extensions/marketplace"
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
func MakeExtensionInstallFn(client *marketplace.MCPMarketplaceClient) action.InProcessFn {
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
