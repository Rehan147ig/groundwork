// Command msgraph-connector authenticates to Microsoft Graph, instantiates
// the aclsync.Service against a Microsoft Graph connector, and executes
// Service.RunOnce. PR #18 of the Microsoft Graph pilot.
//
// Scope intentionally narrow:
//   - Authenticate to Microsoft Graph (OAuth2 client credentials).
//   - Wire msgraph.Connector + aclsync.Service together.
//   - Execute Service.RunOnce so the full pipeline is exercised end-to-end.
//
// Explicit non-goals for this build:
//   - No OpenFGA writes: the Sink is aclsync.DiscardSink, which records what
//     would have been written but never contacts OpenFGA.
//   - No SharePoint enumeration: the GraphClient is wrapped by a
//     directory-only decorator that makes ListDriveItems and
//     ListItemPermissions return empty slices, so Snapshot covers users +
//     groups + memberships only.
//
// Exit codes:
//
//	0  success
//	1  required env var missing (env validation; same as PR #17)
//	2  FGA invariant violation: connector attempted /authorization-models
//	3  RunOnce failed (auth, network, Graph error, etc.)
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"groundwork/query-runtime/internal/aclsync"
	"groundwork/query-runtime/internal/aclsync/msgraph"
)

var requiredEnv = []string{
	"MSGRAPH_TENANT_ID",
	"MSGRAPH_CLIENT_ID",
	"MSGRAPH_CLIENT_SECRET",
	"OPENFGA_STORE_ID",
}

// validate returns the names of required env vars that are unset or empty.
// Factored out of main() so tests exercise it without spawning a subprocess.
func validate(getenv func(string) string) []string {
	var missing []string
	for _, k := range requiredEnv {
		if getenv(k) == "" {
			missing = append(missing, k)
		}
	}
	return missing
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	if missing := validate(os.Getenv); len(missing) > 0 {
		logger.Error("missing required env vars", "missing", missing)
		os.Exit(1)
	}

	cfg := msgraph.Config{
		TenantID:     os.Getenv("MSGRAPH_TENANT_ID"),
		ClientID:     os.Getenv("MSGRAPH_CLIENT_ID"),
		ClientSecret: os.Getenv("MSGRAPH_CLIENT_SECRET"),
		// SiteID + DriveID intentionally empty in PR #18; the directory-only
		// decorator below skips drive operations regardless.
	}

	// Real HTTP client to Microsoft Graph + token endpoint.
	graphClient := msgraph.NewHTTPGraphClient(cfg)

	// Wrap with a decorator that makes drive operations no-ops. Snapshot then
	// covers users + groups + memberships only.
	directoryOnly := newDirectoryOnlyGraphClient(graphClient)

	// Connector implements aclsync.Connector against the wrapped GraphClient.
	// nil delta-token store is fine here: PR #18 calls RunOnce (no watch).
	connector := msgraph.NewConnector(directoryOnly, cfg, logger, nil)

	// DiscardSink: records what would have been written without contacting
	// OpenFGA. PR #19 swaps this for the real OpenFGASink (and installs the
	// FGA invariant guard on its transport).
	sink := aclsync.NewDiscardSink(logger)
	syncer := aclsync.NewSyncer(connector, sink, logger)

	service := aclsync.NewService(connector, syncer, aclsync.Config{
		Mode:     aclsync.ModeOnce,
		TenantID: cfg.TenantID,
	}, logger, nil)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := service.RunOnce(ctx); err != nil {
		// Authentication failures are reported as ErrAuthFailed and propagated
		// (no destructive delete; tested in TestGraphAuthFailureDoesNotDeleteTuples).
		switch {
		case errors.Is(err, msgraph.ErrAuthFailed):
			logger.Error("microsoft graph authentication failed", "tenant", cfg.TenantID)
		default:
			logger.Error("connector run failed", "tenant", cfg.TenantID, "err", err.Error())
		}
		os.Exit(3)
	}

	// Smoke gate: print tenant info (stdout for human-facing summary).
	fmt.Printf("msgraph-connector OK\n  tenant_id: %s\n  mode: directory-only (PR #18 scaffold)\n  tuples_observed: %d (recorded by DiscardSink; no OpenFGA writes)\n",
		cfg.TenantID, sink.WrittenCount())
}
