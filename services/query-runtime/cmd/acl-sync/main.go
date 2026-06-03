// Command acl-sync reconciles enterprise source-of-truth permissions into OpenFGA and
// reports drift. It currently uses the mock enterprise connector; real Microsoft Graph /
// SharePoint, Okta, and Google connectors plug in behind the same aclsync.Connector
// interface later.
//
//	# Sync the mock connector into a live OpenFGA, then report drift:
//	OPENFGA_URL=http://openfga:8080 go run ./cmd/acl-sync -tenant tenant_demo
//
//	# Drift-only (read-only) check (exit 2 if drift is found):
//	OPENFGA_URL=http://openfga:8080 go run ./cmd/acl-sync -drift-only
//
// With no OPENFGA_URL set it uses an in-memory sink (dev/demo only).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"groundwork/query-runtime/internal/aclsync"
)

func main() {
	tenant := flag.String("tenant", "tenant_demo", "tenant id to sync")
	driftOnly := flag.Bool("drift-only", false, "only report drift; do not write tuples")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	connector := aclsync.NewMockConnector()

	var sink aclsync.TupleSink
	if url := os.Getenv("OPENFGA_URL"); url != "" {
		sink = aclsync.NewOpenFGASink(url, env("OPENFGA_STORE_NAME", "groundwork_local"))
		logger.Info("acl-sync using OpenFGA sink", "url", url)
	} else {
		sink = aclsync.NewMemoryFGA()
		logger.Warn("OPENFGA_URL not set; using in-memory sink (dev/demo only, not persisted)")
	}

	syncer := aclsync.NewSyncer(connector, sink, logger)
	ctx := context.Background()

	if !*driftOnly {
		res, err := syncer.SyncToOpenFGA(ctx, *tenant)
		if err != nil {
			logger.Error("sync failed", "err", err)
			os.Exit(1)
		}
		printJSON("sync_result", res)
	}

	drift, err := syncer.DetectDrift(ctx, *tenant)
	if err != nil {
		logger.Error("drift detection failed", "err", err)
		os.Exit(1)
	}
	printJSON("drift_report", drift)

	if *driftOnly && drift.HasDrift() {
		os.Exit(2)
	}
}

func printJSON(label string, v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Printf("%s:\n%s\n", label, string(b))
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
