// Command acl-sync reconciles enterprise source-of-truth permissions into OpenFGA.
//
// ACL_SYNC_MODE=once  (default): perform one full sync and exit.
// ACL_SYNC_MODE=watch        : perform an initial sync, then continuously apply
// permission changes and periodically reconcile + check drift until SIGINT/SIGTERM.
//
// Currently uses the mock connector (ACL_CONNECTOR_TYPE=mock); real Microsoft Graph /
// Okta / Google connectors plug in behind the same aclsync.Connector interface later.
//
// Environment:
//
//	ACL_SYNC_MODE                     once|watch          (default once)
//	ACL_SYNC_TENANT_ID                tenant to sync      (default tenant_demo)
//	ACL_SYNC_INTERVAL_SECONDS         reconcile interval  (default 60,  watch mode)
//	ACL_DRIFT_CHECK_INTERVAL_SECONDS  drift interval      (default 300, watch mode)
//	ACL_CONNECTOR_TYPE                mock                (default mock)
//	OPENFGA_API_URL (or OPENFGA_URL)  OpenFGA endpoint; if unset, in-memory sink (dev only)
//	OPENFGA_STORE_ID                  store id (optional; else resolved by name)
//	OPENFGA_STORE_NAME                store name          (default groundwork_local)
//	OPENFGA_AUTHORIZATION_MODEL_ID    pinned model id (optional)
//	ACL_SYNC_METRICS_ADDR             expose Prometheus /metrics (optional, e.g. :9090)
package main

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"groundwork/query-runtime/internal/aclsync"
	"groundwork/query-runtime/internal/aclsync/msgraph"
	"groundwork/query-runtime/internal/metrics"
	"groundwork/query-runtime/internal/runtime"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg := aclsync.Config{
		Mode:               aclsync.Mode(env("ACL_SYNC_MODE", "once")),
		TenantID:           env("ACL_SYNC_TENANT_ID", "tenant_demo"),
		SyncInterval:       time.Duration(envInt("ACL_SYNC_INTERVAL_SECONDS", 60)) * time.Second,
		DriftCheckInterval: time.Duration(envInt("ACL_DRIFT_CHECK_INTERVAL_SECONDS", 300)) * time.Second,
	}

	// Canonical identity: when enabled, the connector pre-provisions a tenant-scoped principal
	// (and its verified aliases) for every directory user and emits user:principal:<uuid>
	// tuples. The resolver must share the query runtime's Postgres (DATABASE_URL) so the
	// aliases the sync writes are the same ones the runtime resolves at query time.
	canonicalIdentity := os.Getenv("GROUNDWORK_CANONICAL_IDENTITY") == "true"
	var resolver runtime.PrincipalResolver
	closeResolver := func() {}
	if canonicalIdentity {
		var err error
		resolver, closeResolver, err = buildSyncResolver(os.Getenv("DATABASE_URL"), logger)
		if err != nil {
			logger.Error("failed to build principal resolver", "err", err)
			os.Exit(1)
		}
	}
	defer closeResolver()

	connectorType := env("ACL_CONNECTOR_TYPE", "mock")
	var connector aclsync.Connector
	switch connectorType {
	case "mock":
		if canonicalIdentity {
			logger.Warn("GROUNDWORK_CANONICAL_IDENTITY=true but connector is mock; mock emits raw user tuples (canonical principals are only synced by real connectors)")
		}
		connector = aclsync.NewMockConnector()
	case "msgraph":
		if os.Getenv("MS_GRAPH_CONNECTOR_ENABLED") != "true" {
			logger.Error("msgraph connector selected but MS_GRAPH_CONNECTOR_ENABLED is not 'true'")
			os.Exit(1)
		}
		graphCfg := msgraph.Config{
			TenantID:         os.Getenv("MS_GRAPH_TENANT_ID"),
			ClientID:         os.Getenv("MS_GRAPH_CLIENT_ID"),
			ClientSecret:     os.Getenv("MS_GRAPH_CLIENT_SECRET"),
			SiteID:           os.Getenv("MS_GRAPH_SITE_ID"),
			DriveID:          os.Getenv("MS_GRAPH_DRIVE_ID"),
			AuthorityHost:    os.Getenv("MS_GRAPH_AUTHORITY_HOST"),
			DeltaPollSeconds: envInt("ACL_SYNC_INTERVAL_SECONDS", 60),
			Enabled:          true,
		}
		var deltaStore msgraph.DeltaTokenStore = msgraph.NewMemoryDeltaTokenStore()
		if dir := os.Getenv("ACL_DELTA_TOKEN_DIR"); dir != "" {
			deltaStore = msgraph.NewFileDeltaTokenStore(dir)
		}
		// Secrets are never logged; only non-sensitive identifiers.
		graphConnector := msgraph.NewConnector(msgraph.NewHTTPGraphClient(graphCfg), graphCfg, logger, deltaStore)
		graphConnector.SetCanonicalIdentity(resolver, canonicalIdentity)
		connector = graphConnector
		logger.Info("acl-sync using Microsoft Graph connector", "site_id", graphCfg.SiteID, "drive_id", graphCfg.DriveID, "canonical_identity", canonicalIdentity)
	default:
		logger.Error("unsupported connector type", "type", connectorType, "supported", "mock|msgraph")
		os.Exit(1)
	}

	var sink aclsync.TupleSink
	if url := firstNonEmpty(os.Getenv("OPENFGA_API_URL"), os.Getenv("OPENFGA_URL")); url != "" {
		fs := aclsync.NewOpenFGASink(url, env("OPENFGA_STORE_NAME", "groundwork_local"))
		fs.StoreID = os.Getenv("OPENFGA_STORE_ID")
		fs.AuthorizationModelID = os.Getenv("OPENFGA_AUTHORIZATION_MODEL_ID")
		sink = fs
		logger.Info("acl-sync using OpenFGA sink", "url", url, "store_id", fs.StoreID, "store_name", fs.StoreName)
	} else {
		sink = aclsync.NewMemoryFGA()
		logger.Warn("no OpenFGA endpoint set; using in-memory sink (dev/demo only, not persisted)")
	}

	metrics.RegisterAll()
	if addr := os.Getenv("ACL_SYNC_METRICS_ADDR"); addr != "" {
		go func() {
			mux := http.NewServeMux()
			mux.Handle("/metrics", promhttp.Handler())
			if err := http.ListenAndServe(addr, mux); err != nil {
				logger.Error("metrics endpoint stopped", "err", err)
			}
		}()
		logger.Info("metrics endpoint listening", "addr", addr)
	}

	svc := aclsync.NewService(connector, aclsync.NewSyncer(connector, sink, logger), cfg, logger, promMetrics{})

	// Graceful shutdown on SIGINT/SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := svc.Run(ctx); err != nil && ctx.Err() == nil {
		logger.Error("acl-sync exited with error", "err", err)
		os.Exit(1)
	}
}

// promMetrics adapts the Prometheus collectors to aclsync.Metrics.
type promMetrics struct{}

func (promMetrics) SyncRun(t string)                       { metrics.RecordACLSyncRun(t) }
func (promMetrics) SyncError(t string)                     { metrics.RecordACLSyncError(t) }
func (promMetrics) DriftItems(t string, n int)             { metrics.SetACLSyncDriftItems(t, n) }
func (promMetrics) SyncDuration(t string, d time.Duration) { metrics.RecordACLSyncDuration(t, d) }

// buildSyncResolver builds the principal resolver the connector uses to mint principals and
// write aliases. It is Postgres-backed when DATABASE_URL is set (shared with the query
// runtime), otherwise an in-memory resolver for local/demo (not shared across processes —
// canonical sync against an in-memory resolver only makes sense in a single-process test).
func buildSyncResolver(databaseURL string, logger *slog.Logger) (runtime.PrincipalResolver, func(), error) {
	if databaseURL == "" {
		logger.Warn("GROUNDWORK_CANONICAL_IDENTITY=true but DATABASE_URL is unset; using in-memory principal resolver (dev only, aliases are NOT shared with the query runtime)")
		return runtime.NewMemoryPrincipalResolver(), func() {}, nil
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, func() {}, err
	}
	return runtime.NewPostgresPrincipalResolver(db), func() { _ = db.Close() }, nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
