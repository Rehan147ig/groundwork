package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"groundwork/query-runtime/internal/engine"
	"groundwork/query-runtime/internal/mcp"
	"groundwork/query-runtime/internal/runtime"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	cfg := runtime.Config{
		Addr:                  env("QUERY_RUNTIME_ADDR", ":8080"),
		QueryTimeout:          envDuration("QUERY_TIMEOUT_MS", 15*time.Second),
		ACLTimeout:            envDuration("ACL_TIMEOUT_MS", 120*time.Millisecond),
		BackendHTTPTimeout:    envDuration("BACKEND_HTTP_TIMEOUT_MS", 15*time.Second),
		EmbeddingTimeout:      envDuration("EMBEDDING_TIMEOUT_MS", 15*time.Second),
		CircuitOpenTimeout:    envDuration("QDRANT_CIRCUIT_OPEN_TIMEOUT_MS", 10*time.Second),
		CircuitFailureLimit:   envInt("QDRANT_CIRCUIT_FAILURE_LIMIT", 3),
		CircuitHalfOpenLimit:  envInt("QDRANT_CIRCUIT_HALF_OPEN_LIMIT", 1),
		OpenFGAURL:            os.Getenv("OPENFGA_URL"),
		OpenFGAStoreName:      env("OPENFGA_STORE_NAME", "groundwork_local"),
		OpenFGATimeout:        envDuration("OPENFGA_TIMEOUT_MS", 80*time.Millisecond),
		DatabaseURL:           os.Getenv("DATABASE_URL"),
		BootstrapAPIKey:       env("BOOTSTRAP_API_KEY", "gw_local_acme_key"),
		BootstrapTenantID:     env("BOOTSTRAP_TENANT_ID", "acme"),
		BootstrapTenantRegion: env("BOOTSTRAP_TENANT_REGION", "US"),
		IDKThreshold:          envFloat("IDK_THRESHOLD", 0.70),
		VectorWeight:          envFloat("VECTOR_WEIGHT", 0.60),
		KeywordWeight:         envFloat("KEYWORD_WEIGHT", 0.40),
	}

	backend := runtime.NewMemoryBackend()
	if os.Getenv("QDRANT_URL") != "" && os.Getenv("ELASTICSEARCH_URL") != "" {
		backend = runtime.NewHTTPBackend(
			os.Getenv("QDRANT_URL"),
			env("QDRANT_COLLECTION", "groundwork_chunks"),
			os.Getenv("ELASTICSEARCH_URL"),
			env("ELASTICSEARCH_INDEX", "groundwork_chunks"),
			env("EMBEDDING_URL", "http://ingestion:8090"),
			cfg,
		)
	}

	apiKeys, closeAPIKeys, err := runtime.BuildAPIKeyResolver(context.Background(), cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer closeAPIKeys()

	// Audit ledger: synchronous, append-only, tamper-evident. With DATABASE_URL set,
	// every query writes to the immutable Postgres audit_log (hash-chained); otherwise
	// an in-memory trace store is used for local/dev. The engine writes the entry
	// synchronously before returning and fails closed if the write fails.
	//
	// The same AUDIT_TIMEOUT_MS budget is used both for the engine's audit step and for the
	// Postgres writer's own per-write deadline, so the configured timeout is actually honored
	// (the writer no longer caps it at a hardcoded 30ms).
	auditWrite := envDuration("AUDIT_TIMEOUT_MS", auditTimeoutDefault(cfg.DatabaseURL))
	auditor, closeAudit, err := buildAuditWriter(cfg.DatabaseURL, backend, auditWrite)
	if err != nil {
		log.Fatal(err)
	}
	defer closeAudit()

	core := &engine.Engine{
		Config: engine.TimeoutConfig{
			Total:        cfg.QueryTimeout,
			Embedding:    cfg.EmbeddingTimeout,
			QdrantSearch: envDuration("QDRANT_TIMEOUT_MS", 15*time.Second),
			OpenFGACheck: cfg.OpenFGATimeout,
			AuditWrite:   auditWrite,
		},
		Backend: engine.VectorRetrievalClient{Vector: backend.Vector},
		ACL:     backend.ACL,
		Auditor: auditor,
		// Observe-only mode for safe enterprise onboarding: evaluate permissions and
		// log what WOULD be blocked, but do not strip. Tenant/region stay enforced.
		ShadowMode: os.Getenv("GROUNDWORK_SHADOW_MODE") == "true",
	}

	// Verified end-user identity: tenant/region come from the API key, while the
	// effective user is derived from a signed OIDC/JWT assertion (fail closed). A raw
	// demo user_id is honored only when ALLOW_DEMO_IDENTITY=true.
	identityVerifier, err := runtime.BuildIdentityVerifier()
	if err != nil {
		log.Fatal(err)
	}
	allowDemoIdentity := os.Getenv("ALLOW_DEMO_IDENTITY") == "true"

	// Canonical identity (GROUNDWORK_CANONICAL_IDENTITY=true): resolve each verified end-user
	// to a tenant-scoped principal so the engine checks user:principal:<uuid> instead of the
	// raw token subject. The resolver is Postgres-backed in production and in-memory for
	// local/demo; a short-TTL cache keeps the per-query alias lookup off the hot path. The
	// flag (not the resolver) gates canonicalization, so demo/local mode keeps working when
	// it is off, and a verified-but-unresolved identity fails closed when it is on.
	canonicalIdentity := os.Getenv("GROUNDWORK_CANONICAL_IDENTITY") == "true"
	resolver, closeResolver, err := buildPrincipalResolver(cfg.DatabaseURL, envDuration("GROUNDWORK_PRINCIPAL_CACHE_TTL_MS", time.Minute))
	if err != nil {
		log.Fatal(err)
	}
	defer closeResolver()

	// MCP mode: run as stdio MCP server for AI agents (Claude Desktop, etc.)
	if os.Getenv("GROUNDWORK_MCP") == "true" {
		mcpServer := mcp.NewServer(
			core,
			env("BOOTSTRAP_TENANT_ID", "tenant_demo"),
			env("BOOTSTRAP_TENANT_REGION", "uk"),
			identityVerifier,
			allowDemoIdentity,
		)
		mcpServer.SetCanonicalIdentity(resolver, canonicalIdentity)
		log.Println("groundwork MCP server starting (stdio transport)")
		if err := mcpServer.Run(context.Background()); err != nil {
			log.Fatal(err)
		}
		return
	}

	// HTTP mode: REST API + the Cloud MCP endpoint (/mcp) on the same listener. Both
	// reuse the single engine `core`; /mcp authenticates with the same API key resolver.
	server := runtime.NewServerWithExecutor(cfg, backend, apiKeys, core)
	server.SetIdentity(identityVerifier, allowDemoIdentity)
	server.SetCanonicalIdentity(resolver, canonicalIdentity)

	mcpHTTP := mcp.NewHTTPServer(core, apiKeys, identityVerifier, allowDemoIdentity)
	mcpHTTP.SetCanonicalIdentity(resolver, canonicalIdentity)
	root := http.NewServeMux()
	root.Handle("/", server.Routes())
	root.Handle("/mcp", mcpHTTP)

	log.Printf("groundwork query runtime listening on %s (REST + Cloud MCP at POST /mcp)", cfg.Addr)
	if err := http.ListenAndServe(cfg.Addr, root); err != nil {
		log.Fatal(err)
	}
}

func env(key string, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envFloat(key string, fallback float64) float64 {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func envInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fallback
	}
	return time.Duration(parsed) * time.Millisecond
}

// buildAuditWriter selects the audit sink: the immutable Postgres ledger when
// DATABASE_URL is set, otherwise the in-memory trace store for local/dev.
func buildAuditWriter(databaseURL string, backend runtime.Backend, timeout time.Duration) (engine.AuditWriter, func(), error) {
	if databaseURL == "" {
		return engine.RuntimeTraceAuditWriter{Trace: backend.Trace}, func() {}, nil
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, func() {}, err
	}
	// Honor the configured AUDIT_TIMEOUT_MS for the per-write deadline. The bare
	// NewPostgresAuditWriter hardcodes a 30ms budget that is too tight for a real Postgres
	// round-trip (advisory lock + select + insert) and would fail audit writes — and thus
	// fail queries closed — under load or on a cold connection.
	return engine.NewPostgresAuditWriterWithTimeout(db, timeout), func() { _ = db.Close() }, nil
}

// buildPrincipalResolver constructs the canonical principal resolver: the Postgres-backed
// resolver when DATABASE_URL is set (production), otherwise an in-memory resolver for
// local/demo. Both are wrapped in a short-TTL caching resolver so the per-query alias
// lookup does not hit the database on every request. The resolver is always non-nil — the
// GROUNDWORK_CANONICAL_IDENTITY flag, not the resolver, decides whether canonicalization runs.
func buildPrincipalResolver(databaseURL string, ttl time.Duration) (runtime.PrincipalResolver, func(), error) {
	if databaseURL == "" {
		return runtime.NewCachingResolver(runtime.NewMemoryPrincipalResolver(), ttl), func() {}, nil
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, func() {}, err
	}
	return runtime.NewCachingResolver(runtime.NewPostgresPrincipalResolver(db), ttl), func() { _ = db.Close() }, nil
}

// auditTimeoutDefault gives the synchronous audit write a realistic budget: a tight
// 30ms for the in-memory store, but a larger window for a real Postgres round-trip
// (which holds a per-tenant advisory lock). Override with AUDIT_TIMEOUT_MS.
func auditTimeoutDefault(databaseURL string) time.Duration {
	if databaseURL != "" {
		return 2 * time.Second
	}
	return 30 * time.Millisecond
}
