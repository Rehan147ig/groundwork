package runtime

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	gwmetrics "groundwork/query-runtime/internal/metrics"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Server struct {
	cfg      Config
	backend  Backend
	apiKeys  APIKeyResolver
	executor QueryExecutor
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
	mux.HandleFunc("POST /v1/query", s.requireAPIKey("query", s.query))
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
	if req.UserID == "" || req.Question == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user_id and question are required"})
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

func (s *Server) rrf(vector []Candidate, keyword []Candidate) []Candidate {
	merged := map[string]Candidate{}
	add := func(candidates []Candidate, weight float64) {
		for _, candidate := range candidates {
			key := candidate.Chunk.ChunkID
			current := merged[key]
			current.Chunk = candidate.Chunk
			current.Score += weight * (1.0 / float64(60+candidate.Rank))
			merged[key] = current
		}
	}
	add(vector, s.cfg.VectorWeight)
	add(keyword, s.cfg.KeywordWeight)

	out := make([]Candidate, 0, len(merged))
	for _, candidate := range merged {
		out = append(out, candidate)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	for i := range out {
		out[i].Rank = i + 1
	}
	return out
}

func (s *Server) filterAuthorized(parent context.Context, req QueryRequest, candidates []Candidate) ([]Candidate, int, int, []AccessDecision) {
	aclCtx, cancel := context.WithTimeout(parent, s.cfg.ACLTimeout)
	defer cancel()

	type result struct {
		candidate Candidate
		allowed   bool
		regionOK  bool
		reason    string
	}

	resultCh := make(chan result, len(candidates))
	for _, candidate := range candidates {
		go func(item Candidate) {
			if item.Chunk.Region != req.Region {
				resultCh <- result{candidate: item, allowed: false, regionOK: false, reason: "region_mismatch"}
				return
			}
			ok, err := s.backend.ACL.CanAccess(aclCtx, req, item.Chunk)
			reason := "allowed"
			if err != nil {
				reason = "acl_unreachable_fail_closed"
			} else if !ok {
				reason = "scope_mismatch"
			}
			resultCh <- result{candidate: item, allowed: err == nil && ok, regionOK: true, reason: reason}
		}(candidate)
	}

	var allowed []Candidate
	var decisions []AccessDecision
	blockedACL := 0
	blockedRegion := 0
	for range candidates {
		select {
		case item := <-resultCh:
			decisions = append(decisions, decisionFromResult(item.candidate, item.allowed && item.regionOK, item.reason))
			if !item.regionOK {
				blockedRegion++
				continue
			}
			if !item.allowed {
				blockedACL++
				continue
			}
			allowed = append(allowed, item.candidate)
		case <-aclCtx.Done():
			blockedACL += len(candidates) - len(allowed) - blockedACL - blockedRegion
			for _, candidate := range candidates[len(decisions):] {
				decisions = append(decisions, decisionFromResult(candidate, false, "acl_timeout_fail_closed"))
			}
			return allowed, blockedACL, blockedRegion, decisions
		}
	}
	return allowed, blockedACL, blockedRegion, decisions
}

type FailureDetail struct {
	Stage   string
	Code    string
	Message string
}

func (s *Server) writeFailClosed(w http.ResponseWriter, req QueryRequest, started time.Time, mode string, failure FailureDetail) {
	trace := RuntimeTrace{
		TraceID:         newTraceID(),
		TenantID:        req.TenantID,
		UserID:          req.UserID,
		Region:          req.Region,
		StartedAt:       started,
		LatencyMs:       time.Since(started).Milliseconds(),
		DecisionMode:    mode,
		FailureStage:    failure.Stage,
		ErrorCode:       failure.Code,
		ErrorMessage:    failure.Message,
		AccessDecisions: []AccessDecision{{Allowed: false, Reason: failure.Code}},
	}
	trace.ImmutableDigest = traceDigest(trace)
	_ = s.backend.Trace.WriteTrace(context.Background(), trace)
	writeJSON(w, http.StatusOK, QueryResponse{
		Answer:     "No grounded answer was found in the permitted source set.",
		Confidence: 0,
		Citations:  []Citation{},
		Trace:      trace,
	})
}

func classifyRetrievalFailure(err error) FailureDetail {
	message := err.Error()
	switch {
	case errors.Is(err, ErrCircuitOpen):
		return FailureDetail{Stage: "qdrant", Code: "qdrant_circuit_open", Message: "qdrant circuit breaker is open; retrieval failed closed"}
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(message, "context deadline exceeded") || strings.Contains(message, "Client.Timeout"):
		return FailureDetail{Stage: "qdrant", Code: "qdrant_timeout", Message: "qdrant retrieval exceeded configured timeout; retrieval failed closed"}
	case strings.Contains(message, "embedding"):
		return FailureDetail{Stage: "embedding", Code: "embedding_unavailable", Message: "embedding service unavailable; retrieval failed closed"}
	case strings.Contains(message, "qdrant search failed"):
		return FailureDetail{Stage: "qdrant", Code: "qdrant_error", Message: message}
	default:
		return FailureDetail{Stage: "retrieval", Code: "retrieval_error", Message: message}
	}
}

func decisionFromResult(candidate Candidate, allowed bool, reason string) AccessDecision {
	chunk := candidate.Chunk
	return AccessDecision{
		ChunkID:       chunk.ChunkID,
		ChunkHash:     chunk.ChunkHash,
		DocumentID:    chunk.DocumentID,
		Allowed:       allowed,
		Reason:        reason,
		Region:        chunk.Region,
		RequiredScope: chunk.RequiredScope,
	}
}

func rerank(candidates []Candidate, limit int) []Candidate {
	sort.Slice(candidates, func(i, j int) bool {
		left := candidates[i].Score * candidates[i].Chunk.FreshnessScore
		right := candidates[j].Score * candidates[j].Chunk.FreshnessScore
		return left > right
	})
	if len(candidates) > limit {
		return candidates[:limit]
	}
	return candidates
}

func confidence(candidates []Candidate) float64 {
	if len(candidates) == 0 {
		return 0
	}
	score := candidates[0].Score * 45
	if score > 0.99 {
		return 0.99
	}
	return score
}

func toCitations(candidates []Candidate) []Citation {
	citations := make([]Citation, 0, len(candidates))
	for _, candidate := range candidates {
		chunk := candidate.Chunk
		citations = append(citations, Citation{
			DocumentID:     chunk.DocumentID,
			ChunkID:        chunk.ChunkID,
			ChunkHash:      chunk.ChunkHash,
			Page:           chunk.Page,
			Offset:         chunk.Offset,
			Text:           chunk.Text,
			Score:          candidate.Score,
			FreshnessScore: chunk.FreshnessScore,
		})
	}
	return citations
}

func newTraceID() string {
	var bytes [12]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return time.Now().Format("20060102150405")
	}
	return hex.EncodeToString(bytes[:])
}

func traceDigest(trace RuntimeTrace) string {
	payload, _ := json.Marshal(trace)
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
