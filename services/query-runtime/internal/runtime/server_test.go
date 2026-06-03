package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

type mockExecutor struct{}

func (m *mockExecutor) Execute(ctx context.Context, req QueryRequest) QueryResponse {
	return QueryResponse{
		Answer: "mocked answer",
		Trace: RuntimeTrace{
			TenantID: req.TenantID,
			UserID:   req.UserID,
		},
	}
}

func newTestServer(cfg Config) *Server {
	backend := NewMemoryBackend()
	apiKeys := NewMemoryAPIKeyResolver("gw_test_key", TenantContext{TenantID: "tenant_demo", Region: "uk", KeyName: "test"})
	return NewServerWithExecutor(cfg, backend, apiKeys, &mockExecutor{})
}

func TestQueryRejectsMissingAPIKey(t *testing.T) {
	server := newTestServer(Config{})
	body := `{"user_id":"user_1","question":"test"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/query", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestHealthEndpoints(t *testing.T) {
	server := newTestServer(Config{})
	for _, path := range []string{"/health", "/healthz", "/livez", "/readyz"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		server.Routes().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected %s to return 200, got %d: %s", path, rec.Code, rec.Body.String())
		}
	}
}

func TestQueryIgnoresTenantIDFromBody(t *testing.T) {
	server := newTestServer(Config{})
	body := `{"tenant_id":"attacker","region":"eu","user_id":"user_1","question":"test"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/query", bytes.NewBufferString(body))
	req.Header.Set("X-Groundwork-API-Key", "gw_test_key")
	rec := httptest.NewRecorder()

	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"tenant_id":"tenant_demo"`) {
		t.Fatalf("expected tenant from API key, got %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"tenant_id":"attacker"`) {
		t.Fatalf("request body tenant_id was trusted: %s", rec.Body.String())
	}
}

func TestCreateAPIKeyAndUseResolvedTenant(t *testing.T) {
	server := newTestServer(Config{})

	createBody := `{"name":"sdk-demo","scopes":["query"],"rate_limit_rpm":120}`
	createReq := httptest.NewRequest(http.MethodPost, "/v1/admin/api-keys", bytes.NewBufferString(createBody))
	createReq.Header.Set("Authorization", "Bearer gw_test_key")
	createRec := httptest.NewRecorder()

	server.Routes().ServeHTTP(createRec, createReq)

	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", createRec.Code, createRec.Body.String())
	}
	var created CreateAPIKeyResponse
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	queryBody := `{"tenant_id":"attacker","region":"US","user_id":"user_1","question":"test"}`
	queryReq := httptest.NewRequest(http.MethodPost, "/v1/query", bytes.NewBufferString(queryBody))
	queryReq.Header.Set("Authorization", "Bearer "+created.Key)
	queryRec := httptest.NewRecorder()

	server.Routes().ServeHTTP(queryRec, queryReq)

	if queryRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", queryRec.Code, queryRec.Body.String())
	}
	if !strings.Contains(queryRec.Body.String(), `"tenant_id":"tenant_demo"`) {
		t.Fatalf("expected tenant from generated API key, got %s", queryRec.Body.String())
	}
}

func TestRevokeAPIKeyBlocksFutureUse(t *testing.T) {
	server := newTestServer(Config{})

	createReq := httptest.NewRequest(http.MethodPost, "/v1/admin/api-keys", bytes.NewBufferString(`{"name":"revoke-me","scopes":["query"]}`))
	createReq.Header.Set("Authorization", "Bearer gw_test_key")
	createRec := httptest.NewRecorder()
	server.Routes().ServeHTTP(createRec, createReq)

	var created CreateAPIKeyResponse
	_ = json.Unmarshal(createRec.Body.Bytes(), &created)

	revokeReq := httptest.NewRequest(http.MethodDelete, "/v1/admin/api-keys/"+strconv.FormatInt(created.ID, 10), nil)
	revokeReq.Header.Set("Authorization", "Bearer gw_test_key")
	revokeRec := httptest.NewRecorder()
	server.Routes().ServeHTTP(revokeRec, revokeReq)

	if revokeRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", revokeRec.Code, revokeRec.Body.String())
	}

	queryReq := httptest.NewRequest(http.MethodPost, "/v1/query", bytes.NewBufferString(`{"user_id":"user_1","question":"test"}`))
	queryReq.Header.Set("Authorization", "Bearer "+created.Key)
	queryRec := httptest.NewRecorder()
	server.Routes().ServeHTTP(queryRec, queryReq)

	if queryRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected revoked key to fail with 401, got %d: %s", queryRec.Code, queryRec.Body.String())
	}
}

func TestRotateAPIKeyInvalidatesOldKey(t *testing.T) {
	server := newTestServer(Config{})

	createReq := httptest.NewRequest(http.MethodPost, "/v1/admin/api-keys", bytes.NewBufferString(`{"name":"rotate-me","scopes":["query"]}`))
	createReq.Header.Set("Authorization", "Bearer gw_test_key")
	createRec := httptest.NewRecorder()
	server.Routes().ServeHTTP(createRec, createReq)

	var created CreateAPIKeyResponse
	_ = json.Unmarshal(createRec.Body.Bytes(), &created)

	rotateReq := httptest.NewRequest(http.MethodPost, "/v1/admin/api-keys/"+strconv.FormatInt(created.ID, 10)+"/rotate", nil)
	rotateReq.Header.Set("Authorization", "Bearer gw_test_key")
	rotateRec := httptest.NewRecorder()
	server.Routes().ServeHTTP(rotateRec, rotateReq)

	if rotateRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rotateRec.Code, rotateRec.Body.String())
	}
	var rotated RotateAPIKeyResponse
	_ = json.Unmarshal(rotateRec.Body.Bytes(), &rotated)
	if rotated.Key == "" || rotated.Key == created.Key {
		t.Fatalf("expected fresh rotated key, got %+v", rotated)
	}

	oldReq := httptest.NewRequest(http.MethodPost, "/v1/query", bytes.NewBufferString(`{"user_id":"user_1","question":"test"}`))
	oldReq.Header.Set("Authorization", "Bearer "+created.Key)
	oldRec := httptest.NewRecorder()
	server.Routes().ServeHTTP(oldRec, oldReq)
	if oldRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected old key to fail with 401, got %d: %s", oldRec.Code, oldRec.Body.String())
	}

	newReq := httptest.NewRequest(http.MethodPost, "/v1/query", bytes.NewBufferString(`{"user_id":"user_1","question":"test"}`))
	newReq.Header.Set("Authorization", "Bearer "+rotated.Key)
	newRec := httptest.NewRecorder()
	server.Routes().ServeHTTP(newRec, newReq)
	if newRec.Code != http.StatusOK {
		t.Fatalf("expected rotated key to work, got %d: %s", newRec.Code, newRec.Body.String())
	}
}

func TestQueryScopeCannotCreateAPIKey(t *testing.T) {
	server := newTestServer(Config{})

	createReq := httptest.NewRequest(http.MethodPost, "/v1/admin/api-keys", bytes.NewBufferString(`{"name":"query-only","scopes":["query"]}`))
	createReq.Header.Set("Authorization", "Bearer gw_test_key")
	createRec := httptest.NewRecorder()
	server.Routes().ServeHTTP(createRec, createReq)

	var created CreateAPIKeyResponse
	_ = json.Unmarshal(createRec.Body.Bytes(), &created)

	adminReq := httptest.NewRequest(http.MethodPost, "/v1/admin/api-keys", bytes.NewBufferString(`{"name":"should-not-work"}`))
	adminReq.Header.Set("Authorization", "Bearer "+created.Key)
	adminRec := httptest.NewRecorder()
	server.Routes().ServeHTTP(adminRec, adminReq)

	if adminRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", adminRec.Code, adminRec.Body.String())
	}
}
