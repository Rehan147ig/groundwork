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
	"time"

	"github.com/golang-jwt/jwt/v5"
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

// capturingExecutor records the QueryRequest it was called with so tests can
// assert what server.query plumbed in (tenant, region, agent_id, etc.).
type capturingExecutor struct {
	last QueryRequest
}

func (c *capturingExecutor) Execute(ctx context.Context, req QueryRequest) QueryResponse {
	c.last = req
	return QueryResponse{Answer: "ok", Trace: RuntimeTrace{TenantID: req.TenantID, UserID: req.UserID}}
}

func newTestServer(cfg Config) *Server {
	backend := NewMemoryBackend()
	apiKeys := NewMemoryAPIKeyResolver("gw_test_key", TenantContext{TenantID: "tenant_demo", Region: "uk", KeyName: "test"})
	s := NewServerWithExecutor(cfg, backend, apiKeys, &mockExecutor{})
	s.allowDemoIdentity = true // existing tests exercise tenant/API-key behavior under dev identity
	return s
}

// newProdServer is a server in production identity mode: a JWT verifier is wired and
// demo identity is OFF, so /v1/query requires a verified end-user assertion.
func newProdServer() *Server {
	backend := NewMemoryBackend()
	apiKeys := NewMemoryAPIKeyResolver("gw_test_key", TenantContext{TenantID: "tenant_demo", Region: "uk", KeyName: "test"})
	s := NewServerWithExecutor(Config{}, backend, apiKeys, &mockExecutor{})
	s.SetIdentity(hs256Verifier("server-secret"), false)
	return s
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

func TestQueryUsesVerifiedIdentityAndIgnoresBodyUserID(t *testing.T) {
	server := newProdServer()
	token := signHS256(t, "server-secret", jwt.MapClaims{"sub": "alice@corp.com", "exp": time.Now().Add(time.Hour).Unix()})

	body := `{"user_id":"attacker","question":"q"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/query", bytes.NewBufferString(body))
	req.Header.Set("X-Groundwork-API-Key", "gw_test_key")
	req.Header.Set("X-Groundwork-User-Assertion", token)
	rec := httptest.NewRecorder()

	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"user_id":"alice@corp.com"`) {
		t.Fatalf("expected effective user from JWT claim, got %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "attacker") {
		t.Fatalf("forged body user_id was trusted: %s", rec.Body.String())
	}
}

func TestQueryFailsClosedWithoutAssertion(t *testing.T) {
	server := newProdServer()
	body := `{"user_id":"alice","question":"q"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/query", bytes.NewBufferString(body))
	req.Header.Set("X-Groundwork-API-Key", "gw_test_key")
	rec := httptest.NewRecorder()

	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 fail-closed without an identity assertion, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestQueryFailsClosedOnInvalidAssertion(t *testing.T) {
	server := newProdServer()
	bad := signHS256(t, "WRONG-secret", jwt.MapClaims{"sub": "alice", "exp": time.Now().Add(time.Hour).Unix()})

	body := `{"question":"q"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/query", bytes.NewBufferString(body))
	req.Header.Set("X-Groundwork-API-Key", "gw_test_key")
	req.Header.Set("X-Groundwork-User-Assertion", bad)
	rec := httptest.NewRecorder()

	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on invalid JWT, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestQueryTenantStillFromAPIKeyWithJWT(t *testing.T) {
	server := newProdServer()
	token := signHS256(t, "server-secret", jwt.MapClaims{"sub": "alice", "exp": time.Now().Add(time.Hour).Unix()})

	body := `{"tenant_id":"attacker","region":"eu","question":"q"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/query", bytes.NewBufferString(body))
	req.Header.Set("X-Groundwork-API-Key", "gw_test_key")
	req.Header.Set("X-Groundwork-User-Assertion", token)
	rec := httptest.NewRecorder()

	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"tenant_id":"tenant_demo"`) {
		t.Fatalf("tenant must come only from the API key, got %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "attacker") {
		t.Fatalf("body tenant_id was trusted: %s", rec.Body.String())
	}
}

// newCanonicalProdServer is a production-mode server (verified identity required) with the
// canonical principal resolver wired and GROUNDWORK_CANONICAL_IDENTITY effectively on.
func newCanonicalProdServer(resolver PrincipalResolver) *Server {
	backend := NewMemoryBackend()
	apiKeys := NewMemoryAPIKeyResolver("gw_test_key", TenantContext{TenantID: "tenant_demo", Region: "uk", KeyName: "test"})
	s := NewServerWithExecutor(Config{}, backend, apiKeys, &mockExecutor{})
	s.SetIdentity(hs256Verifier("server-secret"), false)
	s.SetCanonicalIdentity(resolver, true)
	return s
}

// /v1/query with canonical identity on: the verified token's subject resolves to a canonical
// principal and the engine sees user_id = "principal:<uuid>" (never the raw subject or body).
func TestQueryCanonicalIdentityResolvesPrincipal(t *testing.T) {
	r := NewMemoryPrincipalResolver()
	r.Seed("tenant_demo", "p-alice", []IdentityAssertion{va("jwt:sub", "alice@corp.com")})
	server := newCanonicalProdServer(r)
	token := signHS256(t, "server-secret", jwt.MapClaims{"sub": "alice@corp.com", "exp": time.Now().Add(time.Hour).Unix()})

	body := `{"user_id":"attacker","question":"q"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/query", bytes.NewBufferString(body))
	req.Header.Set("X-Groundwork-API-Key", "gw_test_key")
	req.Header.Set("X-Groundwork-User-Assertion", token)
	rec := httptest.NewRecorder()

	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"user_id":"principal:p-alice"`) {
		t.Fatalf("expected canonical principal user id, got %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "alice@corp.com") || strings.Contains(rec.Body.String(), "attacker") {
		t.Fatalf("raw subject/body user_id must not survive canonicalization: %s", rec.Body.String())
	}
}

// /v1/query with canonical identity on and a verified-but-unknown identity must fail closed
// (identity_unresolved) — it must never silently downgrade to the raw token subject.
func TestQueryCanonicalUnresolvedFailsClosed(t *testing.T) {
	server := newCanonicalProdServer(NewMemoryPrincipalResolver()) // empty resolver: nothing resolves
	token := signHS256(t, "server-secret", jwt.MapClaims{"sub": "nobody@corp.com", "exp": time.Now().Add(time.Hour).Unix()})

	req := httptest.NewRequest(http.MethodPost, "/v1/query", bytes.NewBufferString(`{"question":"q"}`))
	req.Header.Set("X-Groundwork-API-Key", "gw_test_key")
	req.Header.Set("X-Groundwork-User-Assertion", token)
	rec := httptest.NewRecorder()

	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 identity_unresolved, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "identity_unresolved") {
		t.Fatalf("expected identity_unresolved error body, got %s", rec.Body.String())
	}
}

func TestQueryDemoModeAllowsBodyUserID(t *testing.T) {
	server := newTestServer(Config{}) // demo identity enabled

	body := `{"user_id":"demo_user","question":"q"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/query", bytes.NewBufferString(body))
	req.Header.Set("X-Groundwork-API-Key", "gw_test_key")
	rec := httptest.NewRecorder()

	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 in demo mode, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"user_id":"demo_user"`) {
		t.Fatalf("demo mode should use the body user_id, got %s", rec.Body.String())
	}
}

// TestQueryStampsAgentIDFromAPIKey verifies that the PR #21 agent_id
// plumbing works: the API key's name (TenantContext.KeyName) is set on
// the QueryRequest BEFORE the executor sees it, and it cannot be set
// from the request body. AgentID lands on the audit row downstream
// (engine.auditEntryFromTrace copies it onto AuditEntry.AgentID).
func TestQueryStampsAgentIDFromAPIKey(t *testing.T) {
	backend := NewMemoryBackend()
	apiKeys := NewMemoryAPIKeyResolver("gw_test_key", TenantContext{
		TenantID: "tenant_demo", Region: "uk", KeyName: "treasury-agent",
	})
	captor := &capturingExecutor{}
	server := NewServerWithExecutor(Config{}, backend, apiKeys, captor)
	server.allowDemoIdentity = true

	// Attempt to inject a forged agent_id via the request body — should
	// be ignored because QueryRequest.AgentID has json:"-".
	body := `{"user_id":"user_1","question":"test","AgentID":"forged"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/query", bytes.NewBufferString(body))
	req.Header.Set("X-Groundwork-API-Key", "gw_test_key")
	rec := httptest.NewRecorder()
	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if captor.last.AgentID != "treasury-agent" {
		t.Fatalf("agent_id must come from TenantContext.KeyName, got %q", captor.last.AgentID)
	}
}
