package runtime

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
)

// fakeAuditReader is a deterministic in-memory AuditReader implementation
// for handler tests. It does NOT touch a database — handler tests assert
// HTTP-layer behavior (auth, tenant isolation, query parsing, error
// mapping, response shaping). The Postgres-shaped behavior of the real
// PostgresAuditReader is covered by engine/audit_query_test.go against
// a live DB.
type fakeAuditReader struct {
	listFn   func(ctx context.Context, tenantID string, filter AuditFilter, limit int, cursor string) (AuditPage, error)
	getFn    func(ctx context.Context, tenantID, traceID string) (AuditEntryRead, error)
	statsFn  func(ctx context.Context, tenantID string, from, to time.Time) (AuditStats, error)
	verifyFn func(ctx context.Context, tenantID string) (AuditVerifyResult, error)
}

func (f fakeAuditReader) ListAuditEntries(ctx context.Context, tenantID string, filter AuditFilter, limit int, cursor string) (AuditPage, error) {
	return f.listFn(ctx, tenantID, filter, limit, cursor)
}
func (f fakeAuditReader) GetAuditEntry(ctx context.Context, tenantID, traceID string) (AuditEntryRead, error) {
	return f.getFn(ctx, tenantID, traceID)
}
func (f fakeAuditReader) ListAuditStats(ctx context.Context, tenantID string, from, to time.Time) (AuditStats, error) {
	return f.statsFn(ctx, tenantID, from, to)
}
func (f fakeAuditReader) VerifyTenantChain(ctx context.Context, tenantID string) (AuditVerifyResult, error) {
	return f.verifyFn(ctx, tenantID)
}

// newAuditServer builds a Server with the audit scope wired in and a
// fake reader. The bootstrap API key has scopes [query, audit].
func newAuditServer(reader AuditReader) *Server {
	backend := NewMemoryBackend()
	apiKeys := NewMemoryAPIKeyResolver("gw_test_key", TenantContext{
		TenantID: "tenant_demo", Region: "uk", KeyName: "test-agent",
		Scopes: []string{"query", "audit"},
	})
	s := NewServerWithExecutor(Config{}, backend, apiKeys, &mockExecutor{})
	s.allowDemoIdentity = true
	s.SetAuditReader(reader)
	return s
}

func auditRequest(method, path, apiKey string) (*http.Request, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(method, path, nil)
	if apiKey != "" {
		req.Header.Set("X-Groundwork-API-Key", apiKey)
	}
	return req, httptest.NewRecorder()
}

// ---------------------------------------------------------------------
// /v1/audit list endpoint
// ---------------------------------------------------------------------

func TestAuditList_Returns200WithEntries(t *testing.T) {
	now := time.Date(2026, 6, 8, 14, 0, 0, 0, time.UTC)
	reader := fakeAuditReader{
		listFn: func(ctx context.Context, tenantID string, filter AuditFilter, limit int, cursor string) (AuditPage, error) {
			if tenantID != "tenant_demo" {
				t.Fatalf("tenant_id must come from API key context, got %q", tenantID)
			}
			return AuditPage{
				Entries: []AuditEntryRead{
					{TraceID: "t1", TenantID: tenantID, TimestampUTC: now, UserID: "alice", DecisionMode: "engine_live_acl_fail_closed", ACLDecision: "allowed", Reason: "allowed"},
					{TraceID: "t2", TenantID: tenantID, TimestampUTC: now.Add(-time.Minute), UserID: "bob", DecisionMode: "engine_live_acl_fail_closed", ACLDecision: "denied", Reason: "acl_denied"},
				},
				NextCursor: "cursor-xyz",
			}, nil
		},
	}
	server := newAuditServer(reader)

	req, rec := auditRequest(http.MethodGet, "/v1/audit", "gw_test_key")
	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got AuditListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
	}
	if len(got.Entries) != 2 || got.NextCursor != "cursor-xyz" {
		t.Fatalf("entries=%+v cursor=%q", got.Entries, got.NextCursor)
	}
	if got.Entries[0].TraceID != "t1" || got.Entries[1].ACLDecision != "denied" {
		t.Fatalf("payload shape wrong: %+v", got.Entries)
	}
}

func TestAuditList_TenantIsolationCannotBeOverriddenByQueryParam(t *testing.T) {
	var capturedTenant string
	reader := fakeAuditReader{
		listFn: func(ctx context.Context, tenantID string, filter AuditFilter, limit int, cursor string) (AuditPage, error) {
			capturedTenant = tenantID
			return AuditPage{}, nil
		},
	}
	server := newAuditServer(reader)

	// Attempt to inject a tenant_id query param — must be ignored.
	req, rec := auditRequest(http.MethodGet, "/v1/audit?tenant_id=attacker", "gw_test_key")
	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if capturedTenant != "tenant_demo" {
		t.Fatalf("tenant must come from API key context, got %q (tenant_id from query string was honored — BUG)", capturedTenant)
	}
}

func TestAuditList_RequiresAuditScope(t *testing.T) {
	// Bootstrap key has only "query" scope; not "audit".
	backend := NewMemoryBackend()
	apiKeys := NewMemoryAPIKeyResolver("gw_query_only", TenantContext{
		TenantID: "tenant_demo", Region: "uk", KeyName: "query-only", Scopes: []string{"query"},
	})
	s := NewServerWithExecutor(Config{}, backend, apiKeys, &mockExecutor{})
	s.SetAuditReader(fakeAuditReader{
		listFn: func(ctx context.Context, _ string, _ AuditFilter, _ int, _ string) (AuditPage, error) {
			t.Fatal("listFn should never be reached without audit scope")
			return AuditPage{}, nil
		},
	})

	req, rec := auditRequest(http.MethodGet, "/v1/audit", "gw_query_only")
	s.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 insufficient_scope, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAuditList_AdminScopeGrantsAccess(t *testing.T) {
	// hasScope's "admin" override lets admin keys read audit without an
	// explicit audit grant.
	called := false
	backend := NewMemoryBackend()
	apiKeys := NewMemoryAPIKeyResolver("gw_admin", TenantContext{
		TenantID: "tenant_demo", Region: "uk", KeyName: "admin-key", Scopes: []string{"admin"},
	})
	s := NewServerWithExecutor(Config{}, backend, apiKeys, &mockExecutor{})
	s.SetAuditReader(fakeAuditReader{
		listFn: func(ctx context.Context, _ string, _ AuditFilter, _ int, _ string) (AuditPage, error) {
			called = true
			return AuditPage{}, nil
		},
	})

	req, rec := auditRequest(http.MethodGet, "/v1/audit", "gw_admin")
	s.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (admin scope inherits), got %d: %s", rec.Code, rec.Body.String())
	}
	if !called {
		t.Fatal("listFn was not invoked despite admin-scope access")
	}
}

func TestAuditList_Returns503WhenReaderUnset(t *testing.T) {
	backend := NewMemoryBackend()
	apiKeys := NewMemoryAPIKeyResolver("gw_test_key", TenantContext{
		TenantID: "tenant_demo", Region: "uk", Scopes: []string{"audit"},
	})
	s := NewServerWithExecutor(Config{}, backend, apiKeys, &mockExecutor{})
	// Intentionally NOT setting AuditReader.

	req, rec := auditRequest(http.MethodGet, "/v1/audit", "gw_test_key")
	s.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 audit_unavailable, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAuditList_PassesFiltersAndPagination(t *testing.T) {
	var captured AuditFilter
	var captLimit int
	var captCursor string
	reader := fakeAuditReader{
		listFn: func(ctx context.Context, _ string, filter AuditFilter, limit int, cursor string) (AuditPage, error) {
			captured = filter
			captLimit = limit
			captCursor = cursor
			return AuditPage{}, nil
		},
	}
	server := newAuditServer(reader)

	from := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	to := time.Date(2026, 6, 8, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	url := "/v1/audit?" +
		"agent_key_id=42&" +
		"decision_mode=engine_shadow_observe&" +
		"fail_closed=true&" +
		"user_id=alice&" +
		"from=" + from + "&" +
		"to=" + to + "&" +
		"limit=25&" +
		"cursor=ABC"

	req, rec := auditRequest(http.MethodGet, url, "gw_test_key")
	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if captured.AgentKeyID != 42 {
		t.Errorf("AgentKeyID: want 42, got %d", captured.AgentKeyID)
	}
	if captured.DecisionMode != "engine_shadow_observe" {
		t.Errorf("DecisionMode: %q", captured.DecisionMode)
	}
	if captured.FailClosed == nil || *captured.FailClosed != true {
		t.Errorf("FailClosed not true")
	}
	if captured.UserID != "alice" {
		t.Errorf("UserID: %q", captured.UserID)
	}
	if captured.From.IsZero() || captured.To.IsZero() {
		t.Errorf("from/to not parsed: %+v", captured)
	}
	if captLimit != 25 {
		t.Errorf("limit: want 25, got %d", captLimit)
	}
	if captCursor != "ABC" {
		t.Errorf("cursor: want ABC, got %q", captCursor)
	}
}

func TestAuditList_InvalidFiltersReturn400(t *testing.T) {
	reader := fakeAuditReader{
		listFn: func(ctx context.Context, _ string, _ AuditFilter, _ int, _ string) (AuditPage, error) {
			t.Fatal("listFn should not be reached for malformed input")
			return AuditPage{}, nil
		},
	}
	server := newAuditServer(reader)

	cases := []struct {
		url  string
		code string
	}{
		{"/v1/audit?limit=zero", "invalid_limit"},
		{"/v1/audit?limit=-1", "invalid_limit"},
		{"/v1/audit?agent_key_id=abc", "invalid_agent_key_id"},
		{"/v1/audit?agent_key_id=0", "invalid_agent_key_id"},
		{"/v1/audit?fail_closed=yesplease", "invalid_fail_closed"},
		{"/v1/audit?from=not-a-date", "invalid_from_timestamp"},
		{"/v1/audit?to=2026-06-08", "invalid_to_timestamp"}, // missing time component
	}
	for _, tc := range cases {
		req, rec := auditRequest(http.MethodGet, tc.url, "gw_test_key")
		server.Routes().ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: expected 400, got %d: %s", tc.url, rec.Code, rec.Body.String())
			continue
		}
		var body AuditAPIError
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if body.Error != tc.code {
			t.Errorf("%s: error code want %q, got %q", tc.url, tc.code, body.Error)
		}
	}
}

func TestAuditList_LimitCappedAt200(t *testing.T) {
	var capturedLimit int
	reader := fakeAuditReader{
		listFn: func(ctx context.Context, _ string, _ AuditFilter, limit int, _ string) (AuditPage, error) {
			capturedLimit = limit
			return AuditPage{}, nil
		},
	}
	server := newAuditServer(reader)

	req, rec := auditRequest(http.MethodGet, "/v1/audit?limit=9999", "gw_test_key")
	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if capturedLimit != 200 {
		t.Fatalf("limit must be capped at 200, got %d", capturedLimit)
	}
}

func TestAuditList_InvalidCursorReturns400(t *testing.T) {
	reader := fakeAuditReader{
		listFn: func(ctx context.Context, _ string, _ AuditFilter, _ int, cursor string) (AuditPage, error) {
			// Simulate engine surfacing a "invalid cursor: ..." error.
			return AuditPage{}, errors.New("invalid cursor: bad base64")
		},
	}
	server := newAuditServer(reader)

	req, rec := auditRequest(http.MethodGet, "/v1/audit?cursor=!!!", "gw_test_key")
	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 invalid_cursor, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAuditList_ReaderError500(t *testing.T) {
	reader := fakeAuditReader{
		listFn: func(ctx context.Context, _ string, _ AuditFilter, _ int, _ string) (AuditPage, error) {
			return AuditPage{}, errors.New("postgres connection refused")
		},
	}
	server := newAuditServer(reader)

	req, rec := auditRequest(http.MethodGet, "/v1/audit", "gw_test_key")
	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "postgres") {
		t.Fatalf("error message must not leak DB internals: %s", rec.Body.String())
	}
}

// ---------------------------------------------------------------------
// /v1/audit/{trace_id} detail endpoint
// ---------------------------------------------------------------------

func TestAuditGet_ReturnsFullDetailWithDecisions(t *testing.T) {
	now := time.Date(2026, 6, 8, 14, 0, 0, 0, time.UTC)
	reader := fakeAuditReader{
		getFn: func(ctx context.Context, tenantID, traceID string) (AuditEntryRead, error) {
			if tenantID != "tenant_demo" || traceID != "abc123" {
				return AuditEntryRead{}, ErrAuditEntryNotFound
			}
			return AuditEntryRead{
				TraceID: traceID, TenantID: tenantID, TimestampUTC: now,
				UserID: "alice", QueryHash: "qhash", DecisionMode: "engine_live_acl_fail_closed",
				ACLDecision: "allowed", Reason: "allowed",
				ImmutableDigest: "abcd", PreviousHash: "dead",
				AccessDecisions: []AccessDecision{
					{ChunkID: "c1", DocumentID: "d1", Allowed: true, Reason: "allowed", Region: "US"},
					{ChunkID: "c2", DocumentID: "d2", Allowed: false, Reason: "acl_denied", Region: "US"},
				},
			}, nil
		},
	}
	server := newAuditServer(reader)

	req, rec := auditRequest(http.MethodGet, "/v1/audit/abc123", "gw_test_key")
	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got AuditEntryDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.TraceID != "abc123" || got.ImmutableDigest != "abcd" {
		t.Fatalf("payload shape wrong: %+v", got)
	}
	if len(got.AccessDecisions) != 2 || got.AccessDecisions[0].ChunkID != "c1" || got.AccessDecisions[1].Allowed {
		t.Fatalf("access_decisions wrong: %+v", got.AccessDecisions)
	}
}

func TestAuditGet_NotFoundReturns404(t *testing.T) {
	reader := fakeAuditReader{
		getFn: func(ctx context.Context, tenantID, traceID string) (AuditEntryRead, error) {
			return AuditEntryRead{}, ErrAuditEntryNotFound
		},
	}
	server := newAuditServer(reader)

	req, rec := auditRequest(http.MethodGet, "/v1/audit/missing", "gw_test_key")
	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	var body AuditAPIError
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Error != "audit_entry_not_found" {
		t.Fatalf("error code: %q", body.Error)
	}
}

func TestAuditGet_TenantIsolation(t *testing.T) {
	// The reader gets the tenant from the API key context, NOT from the
	// path or query string. A trace_id that belongs to a different
	// tenant must come back as 404 (which is what the reader returns
	// when scoped to the calling tenant).
	var capturedTenant string
	reader := fakeAuditReader{
		getFn: func(ctx context.Context, tenantID, traceID string) (AuditEntryRead, error) {
			capturedTenant = tenantID
			return AuditEntryRead{}, ErrAuditEntryNotFound
		},
	}
	server := newAuditServer(reader)

	req, rec := auditRequest(http.MethodGet, "/v1/audit/foreign-trace", "gw_test_key")
	server.Routes().ServeHTTP(rec, req)

	if capturedTenant != "tenant_demo" {
		t.Fatalf("tenant_id must come from API key, got %q", capturedTenant)
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------
// /v1/audit/stats endpoint
// ---------------------------------------------------------------------

func TestAuditStats_DefaultWindowIs24h(t *testing.T) {
	var capturedFrom, capturedTo time.Time
	reader := fakeAuditReader{
		statsFn: func(ctx context.Context, tenantID string, from, to time.Time) (AuditStats, error) {
			capturedFrom = from
			capturedTo = to
			return AuditStats{
				WindowFrom: from, WindowTo: to,
				TotalQueries:    100,
				FailClosedCount: 3,
				ByDecisionMode:  map[string]int{"engine_live_acl_fail_closed": 95, "engine_fail_closed": 5},
				ByACLDecision:   map[string]int{"allowed": 80, "denied": 15, "fail_closed": 5},
				TopAgents: []AgentStat{
					{AgentKeyID: 1, AgentKeyName: "treasury", Count: 60},
					{AgentKeyID: 2, AgentKeyName: "compliance", Count: 40},
				},
			}, nil
		},
	}
	server := newAuditServer(reader)

	req, rec := auditRequest(http.MethodGet, "/v1/audit/stats", "gw_test_key")
	before := time.Now()
	server.Routes().ServeHTTP(rec, req)
	after := time.Now()

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	// Default window: from = now-24h, to = unset (Server passes zero,
	// engine substitutes now).
	if capturedFrom.IsZero() {
		t.Fatal("default from must not be zero")
	}
	if span := before.Sub(capturedFrom); span < 24*time.Hour-time.Minute || span > 24*time.Hour+time.Minute {
		t.Fatalf("default window from must be ~24h before request, got delta %s", span)
	}
	if !capturedTo.IsZero() {
		t.Fatalf("when 'to' not specified, the handler must pass zero so the impl chooses now, got %v", capturedTo)
	}
	_ = after

	var got AuditStatsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.TotalQueries != 100 || got.FailClosedCount != 3 {
		t.Fatalf("totals wrong: %+v", got)
	}
	if got.ByDecisionMode["engine_live_acl_fail_closed"] != 95 {
		t.Fatalf("decision_mode breakdown wrong: %+v", got.ByDecisionMode)
	}
	if len(got.TopAgents) != 2 || got.TopAgents[0].AgentKeyID != 1 {
		t.Fatalf("top_agents wrong: %+v", got.TopAgents)
	}
}

func TestAuditStats_RejectsInvertedWindow(t *testing.T) {
	reader := fakeAuditReader{
		statsFn: func(ctx context.Context, _ string, _, _ time.Time) (AuditStats, error) {
			t.Fatal("statsFn should not be reached for inverted window")
			return AuditStats{}, nil
		},
	}
	server := newAuditServer(reader)

	from := "2026-06-08T12:00:00Z"
	to := "2026-06-08T11:00:00Z"
	req, rec := auditRequest(http.MethodGet, "/v1/audit/stats?from="+from+"&to="+to, "gw_test_key")
	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	var body AuditAPIError
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Error != "invalid_window_to_before_from" {
		t.Fatalf("error code: %q", body.Error)
	}
}

func TestAuditStats_EmptyAggregatesRenderAsObjects(t *testing.T) {
	// Even a zero-result window must render {} for the breakdown maps,
	// never null — the Dashboard's JSON parser is map-typed.
	reader := fakeAuditReader{
		statsFn: func(ctx context.Context, _ string, _, _ time.Time) (AuditStats, error) {
			return AuditStats{
				WindowFrom:     time.Now().Add(-time.Hour),
				WindowTo:       time.Now(),
				ByDecisionMode: nil,
				ByACLDecision:  nil,
			}, nil
		},
	}
	server := newAuditServer(reader)
	req, rec := auditRequest(http.MethodGet, "/v1/audit/stats", "gw_test_key")
	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"by_decision_mode":{}`) || !strings.Contains(body, `"by_acl_decision":{}`) {
		t.Fatalf("empty maps must render as {}: %s", body)
	}
}

// ---------------------------------------------------------------------
// /v1/audit/verify endpoint
// ---------------------------------------------------------------------

func TestAuditVerify_Clean(t *testing.T) {
	reader := fakeAuditReader{
		verifyFn: func(ctx context.Context, tenantID string) (AuditVerifyResult, error) {
			if tenantID != "tenant_demo" {
				t.Fatalf("tenant_id must come from API key, got %q", tenantID)
			}
			return AuditVerifyResult{Verified: true, EntriesChecked: 42}, nil
		},
	}
	server := newAuditServer(reader)

	req, rec := auditRequest(http.MethodGet, "/v1/audit/verify", "gw_test_key")
	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got AuditVerifyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Verified || got.EntriesChecked != 42 || len(got.Problems) != 0 {
		t.Fatalf("verify response wrong: %+v", got)
	}
}

func TestAuditVerify_ReportsProblems(t *testing.T) {
	reader := fakeAuditReader{
		verifyFn: func(ctx context.Context, _ string) (AuditVerifyResult, error) {
			return AuditVerifyResult{
				Verified:       false,
				EntriesChecked: 10,
				Problems: []AuditChainProblem{
					{Index: 4, TraceID: "trace-tamper", Kind: "digest_mismatch", Detail: "row fields were modified after write"},
					{Index: 5, TraceID: "trace-orphan", Kind: "broken_link", Detail: "previous_hash does not match prior digest"},
				},
			}, nil
		},
	}
	server := newAuditServer(reader)

	req, rec := auditRequest(http.MethodGet, "/v1/audit/verify", "gw_test_key")
	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var got AuditVerifyResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Verified {
		t.Fatalf("verified must be false when problems exist")
	}
	if len(got.Problems) != 2 {
		t.Fatalf("expected 2 problems, got %d: %+v", len(got.Problems), got.Problems)
	}
	if got.Problems[0].Kind != "digest_mismatch" || got.Problems[1].Kind != "broken_link" {
		t.Fatalf("problems shape wrong: %+v", got.Problems)
	}
}

// ---------------------------------------------------------------------
// Cross-cutting: no mutation endpoints
// ---------------------------------------------------------------------

func TestAuditNoMutationEndpoints(t *testing.T) {
	server := newAuditServer(fakeAuditReader{
		listFn:   func(ctx context.Context, _ string, _ AuditFilter, _ int, _ string) (AuditPage, error) { return AuditPage{}, nil },
		getFn:    func(ctx context.Context, _, _ string) (AuditEntryRead, error) { return AuditEntryRead{}, ErrAuditEntryNotFound },
		statsFn:  func(ctx context.Context, _ string, _, _ time.Time) (AuditStats, error) { return AuditStats{}, nil },
		verifyFn: func(ctx context.Context, _ string) (AuditVerifyResult, error) { return AuditVerifyResult{}, nil },
	})

	// Try POST, PUT, PATCH, DELETE on each audit path. ServeMux's
	// method-aware patterns mean these return 405 Method Not Allowed
	// rather than dispatching to the GET handler.
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		for _, path := range []string{"/v1/audit", "/v1/audit/abc", "/v1/audit/stats", "/v1/audit/verify"} {
			req := httptest.NewRequest(method, path, bytes.NewBufferString("{}"))
			req.Header.Set("X-Groundwork-API-Key", "gw_test_key")
			rec := httptest.NewRecorder()
			server.Routes().ServeHTTP(rec, req)
			if rec.Code == http.StatusOK {
				t.Errorf("%s %s returned 200 — audit endpoints must be GET-only", method, path)
			}
		}
	}
}
