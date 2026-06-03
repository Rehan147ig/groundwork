package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"groundwork/query-runtime/internal/engine"
	"groundwork/query-runtime/internal/runtime"
)

func testAPIKeys() runtime.APIKeyResolver {
	return runtime.NewMemoryAPIKeyResolver("gw_test_key", runtime.TenantContext{
		TenantID: "tenant_demo", Region: "uk", KeyName: "test", Scopes: []string{"query"},
	})
}

type failingVector struct{}

func (failingVector) SearchVector(context.Context, runtime.QueryRequest, int) ([]runtime.Candidate, error) {
	return nil, errors.New("qdrant unavailable")
}

func newFailingEngine() *engine.Engine {
	backend := runtime.NewMemoryBackend()
	return &engine.Engine{
		Config:  engine.TimeoutConfig{Total: 500 * time.Millisecond, QdrantSearch: 100 * time.Millisecond, OpenFGACheck: 150 * time.Millisecond, AuditWrite: 50 * time.Millisecond},
		Backend: engine.VectorRetrievalClient{Vector: failingVector{}},
		ACL:     backend.ACL,
		Auditor: engine.RuntimeTraceAuditWriter{Trace: backend.Trace},
	}
}

func mcpPost(h *HTTPServer, apiKey, assertion string, payload any) *httptest.ResponseRecorder {
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	if apiKey != "" {
		req.Header.Set("X-Groundwork-API-Key", apiKey)
	}
	if assertion != "" {
		req.Header.Set("X-Groundwork-User-Assertion", assertion)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func searchCall(id int, args map[string]string) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0", "id": id, "method": "tools/call",
		"params": map[string]any{"name": "groundwork_search", "arguments": args},
	}
}

func TestHTTPMCPMissingAPIKeyReturns401(t *testing.T) {
	h := NewHTTPServer(newTestEngine(), testAPIKeys(), nil, true)
	rec := mcpPost(h, "", "", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{}})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without API key, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHTTPMCPInitialize(t *testing.T) {
	h := NewHTTPServer(newTestEngine(), testAPIKeys(), nil, true)
	rec := mcpPost(h, "gw_test_key", "", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{}})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"name":"groundwork"`) || !strings.Contains(body, `"protocolVersion":"2024-11-05"`) {
		t.Fatalf("unexpected initialize result: %s", body)
	}
}

func TestHTTPMCPToolsListReturnsGroundworkSearch(t *testing.T) {
	h := NewHTTPServer(newTestEngine(), testAPIKeys(), nil, true)
	rec := mcpPost(h, "gw_test_key", "", map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": map[string]any{}})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"groundwork_search"`) {
		t.Fatalf("tools/list missing groundwork_search: %s", rec.Body.String())
	}
}

func TestHTTPMCPPing(t *testing.T) {
	h := NewHTTPServer(newTestEngine(), testAPIKeys(), nil, true)
	rec := mcpPost(h, "gw_test_key", "", map[string]any{"jsonrpc": "2.0", "id": 9, "method": "ping", "params": map[string]any{}})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"result"`) {
		t.Fatalf("expected ping 200 with result, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHTTPMCPToolsCallAllowedUserReturnsChunks(t *testing.T) {
	h := NewHTTPServer(newTestEngine(), testAPIKeys(), nil, true) // demo identity on
	rec := mcpPost(h, "gw_test_key", "", searchCall(3, map[string]string{"user_id": "finance_user", "question": aclQuestion}))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "sharepoint-policy") {
		t.Fatalf("allowed user should receive sharepoint-policy: %s", rec.Body.String())
	}
}

func TestHTTPMCPToolsCallBlockedUserReturnsZeroChunks(t *testing.T) {
	h := NewHTTPServer(newTestEngine(), testAPIKeys(), nil, true)
	rec := mcpPost(h, "gw_test_key", "", searchCall(4, map[string]string{"user_id": "general_user", "question": aclQuestion}))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "sharepoint-policy") {
		t.Fatalf("blocked user must NOT receive sharepoint-policy: %s", body)
	}
	if !strings.Contains(body, "ACCESS DENIED") {
		t.Fatalf("blocked user should get an access-denied result: %s", body)
	}
}

func TestHTTPMCPForgedUserIDIgnoredWithJWT(t *testing.T) {
	t.Setenv("GROUNDWORK_JWT_HS_SECRET", "http-secret")
	verifier, err := runtime.BuildIdentityVerifier()
	if err != nil || verifier == nil {
		t.Fatalf("build verifier: err=%v nil=%v", err, verifier == nil)
	}
	h := NewHTTPServer(newTestEngine(), testAPIKeys(), verifier, false) // production identity
	token := signMCP(t, "http-secret", "finance_user")

	// Forged user_id "attacker" in the arguments, but a valid JWT for finance_user in
	// the header. The verified identity must win, so finance_user gets the document.
	rec := mcpPost(h, "gw_test_key", token, searchCall(5, map[string]string{"user_id": "attacker", "question": aclQuestion}))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "sharepoint-policy") {
		t.Fatalf("verified finance_user (JWT) should receive the doc despite forged user_id: %s", rec.Body.String())
	}
}

func TestHTTPMCPBackendFailureFailsClosed(t *testing.T) {
	h := NewHTTPServer(newFailingEngine(), testAPIKeys(), nil, true)
	rec := mcpPost(h, "gw_test_key", "", searchCall(6, map[string]string{"user_id": "finance_user", "question": aclQuestion}))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 envelope, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "sharepoint-policy") {
		t.Fatalf("backend failure must return zero chunks: %s", body)
	}
	if !strings.Contains(body, `"isError":true`) || !strings.Contains(body, "ACCESS DENIED") {
		t.Fatalf("backend failure should fail closed with isError: %s", body)
	}
}
