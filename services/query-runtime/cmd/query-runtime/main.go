package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"groundwork/query-runtime/internal/engine"
	"groundwork/query-runtime/internal/mcp"
	"groundwork/query-runtime/internal/runtime"
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

	core := &engine.Engine{
		Config: engine.TimeoutConfig{
			Total:        cfg.QueryTimeout,
			Embedding:    cfg.EmbeddingTimeout,
			QdrantSearch: envDuration("QDRANT_TIMEOUT_MS", 15*time.Second),
			OpenFGACheck: cfg.OpenFGATimeout,
			AuditWrite:   envDuration("AUDIT_TIMEOUT_MS", 30*time.Millisecond),
		},
		Backend: engine.VectorRetrievalClient{Vector: backend.Vector},
		ACL:     backend.ACL,
		Auditor: engine.RuntimeTraceAuditWriter{Trace: backend.Trace},
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

	// MCP mode: run as stdio MCP server for AI agents (Claude Desktop, etc.)
	if os.Getenv("GROUNDWORK_MCP") == "true" {
		mcpServer := mcp.NewServer(
			core,
			env("BOOTSTRAP_TENANT_ID", "tenant_demo"),
			env("BOOTSTRAP_TENANT_REGION", "uk"),
			identityVerifier,
			allowDemoIdentity,
		)
		log.Println("groundwork MCP server starting (stdio transport)")
		if err := mcpServer.Run(context.Background()); err != nil {
			log.Fatal(err)
		}
		return
	}

	// HTTP mode: run as REST API server
	server := runtime.NewServerWithExecutor(cfg, backend, apiKeys, core)
	server.SetIdentity(identityVerifier, allowDemoIdentity)
	log.Printf("groundwork query runtime listening on %s", cfg.Addr)
	if err := http.ListenAndServe(cfg.Addr, server.Routes()); err != nil {
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
