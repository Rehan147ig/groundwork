package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"groundwork/query-runtime/internal/engine"
	"groundwork/query-runtime/internal/runtime"
)

// HTTPServer exposes the Groundwork MCP tools over HTTP using JSON-RPC 2.0 on
// POST /mcp. This is the first Streamable-HTTP-compatible Cloud MCP transport
// milestone: plain request/response JSON-RPC (no SSE/session layer yet).
//
// It reuses the exact same tool registry and the single Engine.Execute path as the
// stdio MCP server (through *Server.dispatch). It does NOT create a second engine.
type HTTPServer struct {
	mcp     *Server
	apiKeys runtime.APIKeyResolver
}

// NewHTTPServer builds the Cloud MCP HTTP endpoint. tenant_id/region are resolved per
// request from the Groundwork API key (never from the request body or tool arguments);
// verifier/allowDemo drive end-user identity exactly as the stdio transport does.
func NewHTTPServer(eng *engine.Engine, apiKeys runtime.APIKeyResolver, verifier runtime.IdentityVerifier, allowDemo bool) *HTTPServer {
	return &HTTPServer{
		// tenant/region are supplied per-request by ServeHTTP, so the wrapped Server's
		// own tenant/region fields are intentionally empty here.
		mcp:     NewServer(eng, "", "", verifier, allowDemo),
		apiKeys: apiKeys,
	}
}

// SetCanonicalIdentity forwards the canonical principal resolver and feature flag to the
// wrapped MCP server, so the HTTP transport resolves canonical principals identically to
// stdio (it shares the same dispatch/executeSearch path).
func (h *HTTPServer) SetCanonicalIdentity(resolver runtime.PrincipalResolver, canonical bool) {
	h.mcp.SetCanonicalIdentity(resolver, canonical)
}

func (h *HTTPServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]string{
			"error":  "method_not_allowed",
			"detail": "POST a JSON-RPC 2.0 request to /mcp",
		})
		return
	}
	if h.apiKeys == nil {
		writeJSONStatus(w, http.StatusUnauthorized, map[string]string{"error": "api_key_resolver_unavailable"})
		return
	}

	// The Groundwork API key authenticates the caller and resolves tenant + region.
	// These NEVER come from the request body or the tool arguments.
	rawKey := runtime.ExtractAPIKey(r)
	if rawKey == "" {
		writeJSONStatus(w, http.StatusUnauthorized, map[string]string{"error": "missing_api_key"})
		return
	}
	authCtx, cancel := context.WithTimeout(r.Context(), 500*time.Millisecond)
	defer cancel()
	tenant, err := h.apiKeys.Resolve(authCtx, rawKey)
	if err != nil {
		writeJSONStatus(w, http.StatusUnauthorized, map[string]string{"error": "invalid_api_key"})
		return
	}
	if !runtime.HasScope(tenant, "query") {
		writeJSONStatus(w, http.StatusForbidden, map[string]string{"error": "insufficient_scope"})
		return
	}

	var req jsonrpcRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSONStatus(w, http.StatusOK, errResponse(nil, -32700, "parse error"))
		return
	}

	// Optional out-of-band end-user identity assertion. When present it takes
	// precedence over the user_token tool argument (see executeSearch).
	assertion := extractUserAssertionHeader(r)

	resp, ok := h.mcp.dispatch(r.Context(), tenant.TenantID, tenant.Region, assertion, req)
	if !ok {
		// JSON-RPC notification (e.g. "initialized"): acknowledge with no body.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeJSONStatus(w, http.StatusOK, resp)
}

func extractUserAssertionHeader(r *http.Request) string {
	value := strings.TrimSpace(r.Header.Get("X-Groundwork-User-Assertion"))
	const prefix = "Bearer "
	if strings.HasPrefix(strings.ToLower(value), strings.ToLower(prefix)) {
		return strings.TrimSpace(value[len(prefix):])
	}
	return value
}

func writeJSONStatus(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
