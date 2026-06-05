package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	gwmetrics "groundwork/query-runtime/internal/metrics"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Server struct {
	cfg               Config
	backend           Backend
	apiKeys           APIKeyResolver
	executor          QueryExecutor
	identity          IdentityVerifier
	allowDemoIdentity bool
	resolver          PrincipalResolver
	canonicalIdentity bool
}

// SetIdentity wires the end-user identity verifier and the demo-identity switch.
// When allowDemo is false and no valid assertion is present, /v1/query fails closed.
func (s *Server) SetIdentity(verifier IdentityVerifier, allowDemo bool) {
	s.identity = verifier
	s.allowDemoIdentity = allowDemo
}

// SetCanonicalIdentity wires the canonical principal resolver and the feature flag
// (GROUNDWORK_CANONICAL_IDENTITY=true). When enabled, a verified end-user identity is
// resolved to a tenant-scoped canonical principal so the engine checks
// user:principal:<uuid>. A verified identity that resolves to no principal fails
// closed (identity_unresolved) — it never silently downgrades to the raw user id.
// Demo / unverified identities are always skipped (raw user id kept), so local mode
// keeps working whether or not the flag is set.
func (s *Server) SetCanonicalIdentity(resolver PrincipalResolver, canonical bool) {
	s.resolver = resolver
	s.canonicalIdentity = canonical
}

type QueryExecutor interface {
	Execute(ctx context.Context, req QueryRequest) QueryResponse
}

func NewServer(cfg Config, backend Backend) *Server {
	return NewServerWithAuth(cfg, backend, NewMemoryAPIKeyResolver("gw_test_key", TenantContext{TenantID: "tenant_demo", Region: "uk", KeyName: "test"}))
}

func NewServerWithAuth(cfg Config, backend Backend, apiKeys APIKeyResolver) *Server {
	return &Server{cfg: cfg, backend: backend, apiKeys: apiKeys}
}

func NewServerWithExecutor(cfg Config, backend Backend, apiKeys APIKeyResolver, executor QueryExecutor) *Server {
	return &Server{cfg: cfg, backend: backend, apiKeys: apiKeys, executor: executor}
}

func (s *Server) Routes() http.Handler {
	gwmetrics.RegisterAll()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /livez", s.livez)
	mux.HandleFunc("GET /readyz", s.readyz)
	mux.Handle("GET /metrics", promhttp.Handler())
	mux.HandleFunc("POST /v1/query", s.requireAPIKey("query", s.requireVerifiedIdentity(s.query)))
	mux.HandleFunc("POST /v1/admin/api-keys", s.requireAPIKey("admin", s.createAPIKey))
	mux.HandleFunc("POST /v1/admin/api-keys/{id}/rotate", s.requireAPIKey("admin", s.rotateAPIKey))
	mux.HandleFunc("DELETE /v1/admin/api-keys/{id}", s.requireAPIKey("admin", s.revokeAPIKey))
	return mux
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "groundwork-query-runtime"})
}

func (s *Server) livez(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "alive"})
}

func (s *Server) readyz(w http.ResponseWriter, _ *http.Request) {
	if s.apiKeys == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready", "reason": "api_key_resolver_unavailable"})
		return
	}
	if s.executor == nil && (s.backend.Vector == nil || s.backend.ACL == nil || s.backend.Trace == nil) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready", "reason": "runtime_backend_unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) query(w http.ResponseWriter, r *http.Request) {
	tenant, ok := tenantFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_tenant_context"})
		return
	}
	var req QueryRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	req.TenantID = tenant.TenantID
	req.Region = tenant.Region

	// Effective end-user identity: a verified assertion always wins and the
	// body-supplied user_id is ignored. The body user_id is honored only in demo
	// mode (ALLOW_DEMO_IDENTITY=true). tenant_id/region above come solely from the
	// API key and are never taken from the request body.
	if decision, ok := identityFromContext(r.Context()); ok && decision.identity.Verified {
		// Canonicalize the verified identity to a tenant-scoped principal when the
		// feature flag is on. When off (or for demo/unverified identities) this returns
		// the raw user id unchanged. A verified-but-unresolved identity fails closed.
		effectiveUserID, _, err := CanonicalizeIdentity(r.Context(), s.resolver, s.canonicalIdentity, tenant.TenantID, decision.identity)
		if err != nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "identity_unresolved"})
			return
		}
		req.UserID = effectiveUserID
	}
	if req.Question == "" || req.UserID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "question and a verified user identity are required"})
		return
	}
	if s.executor == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "engine_unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, s.executor.Execute(r.Context(), req))
}

func (s *Server) createAPIKey(w http.ResponseWriter, r *http.Request) {
	tenant, ok := tenantFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_tenant_context"})
		return
	}
	manager, ok := s.apiKeys.(APIKeyManager)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": ErrAPIKeyManagementUnavailable.Error()})
		return
	}
	var req CreateAPIKeyRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 700*time.Millisecond)
	defer cancel()
	resp, err := manager.Create(ctx, tenant, req)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "api_key_create_failed"})
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) revokeAPIKey(w http.ResponseWriter, r *http.Request) {
	tenant, ok := tenantFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_tenant_context"})
		return
	}
	manager, ok := s.apiKeys.(APIKeyManager)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": ErrAPIKeyManagementUnavailable.Error()})
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_api_key_id"})
		return
	}
	if id == tenant.KeyID {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot_revoke_current_key"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 700*time.Millisecond)
	defer cancel()
	revoked, err := manager.Revoke(ctx, tenant, id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "api_key_revoke_failed"})
		return
	}
	if !revoked {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "api_key_not_found"})
		return
	}
	writeJSON(w, http.StatusOK, RevokeAPIKeyResponse{ID: id, Revoked: true, Status: "revoked"})
}

func (s *Server) rotateAPIKey(w http.ResponseWriter, r *http.Request) {
	tenant, ok := tenantFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_tenant_context"})
		return
	}
	manager, ok := s.apiKeys.(APIKeyManager)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": ErrAPIKeyManagementUnavailable.Error()})
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_api_key_id"})
		return
	}
	if id == tenant.KeyID {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot_rotate_current_key"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 700*time.Millisecond)
	defer cancel()
	resp, err := manager.Rotate(ctx, tenant, id)
	if err != nil {
		if errors.Is(err, ErrInvalidAPIKey) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "api_key_not_found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "api_key_rotate_failed"})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) requireAPIKey(scope string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.apiKeys == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "api_key_resolver_unavailable"})
			return
		}
		rawKey := extractAPIKey(r)
		if rawKey == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_api_key"})
			return
		}
		authCtx, cancel := context.WithTimeout(r.Context(), 500*time.Millisecond)
		defer cancel()
		tenant, err := s.apiKeys.Resolve(authCtx, rawKey)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_api_key"})
			return
		}
		if !hasScope(tenant, scope) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient_scope"})
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), tenantContextKey{}, tenant)))
	}
}

type tenantContextKey struct{}

func tenantFromContext(ctx context.Context) (TenantContext, bool) {
	tenant, ok := ctx.Value(tenantContextKey{}).(TenantContext)
	return tenant, ok
}

type identityContextKey struct{}

// identityDecision carries the outcome of identity middleware. A verified
// identity overrides any body-supplied user_id; demo==true means the request may
// fall back to the body user_id (dev only).
type identityDecision struct {
	identity Identity
	demo     bool
}

func identityFromContext(ctx context.Context) (identityDecision, bool) {
	decision, ok := ctx.Value(identityContextKey{}).(identityDecision)
	return decision, ok
}

func extractUserAssertion(r *http.Request) string {
	value := strings.TrimSpace(r.Header.Get("X-Groundwork-User-Assertion"))
	const prefix = "Bearer "
	if strings.HasPrefix(strings.ToLower(value), strings.ToLower(prefix)) {
		return strings.TrimSpace(value[len(prefix):])
	}
	return value
}

// requireVerifiedIdentity enforces that /v1/query carries a cryptographically
// verified end-user identity. A signed assertion (X-Groundwork-User-Assertion) is
// always verified and, on success, becomes the effective user. When no assertion
// is supplied the request fails closed unless demo identity is explicitly enabled.
func (s *Server) requireVerifiedIdentity(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := extractUserAssertion(r)
		if token != "" {
			if s.identity == nil {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "identity_verifier_unavailable"})
				return
			}
			id, err := s.identity.Verify(r.Context(), token)
			if err != nil {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_identity_assertion"})
				return
			}
			ctx := context.WithValue(r.Context(), identityContextKey{}, identityDecision{identity: id})
			next(w, r.WithContext(ctx))
			return
		}
		if !s.allowDemoIdentity {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "verified_identity_required"})
			return
		}
		ctx := context.WithValue(r.Context(), identityContextKey{}, identityDecision{demo: true})
		next(w, r.WithContext(ctx))
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
