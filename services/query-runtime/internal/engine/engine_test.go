package engine

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	gwmetrics "groundwork/query-runtime/internal/metrics"
	"groundwork/query-runtime/internal/runtime"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestExecuteFiltersACLConcurrently(t *testing.T) {
	candidates := make([]runtime.Candidate, 10)
	for i := range candidates {
		candidates[i] = runtime.Candidate{
			Chunk: runtime.Chunk{
				TenantID:       "acme",
				Region:         "US",
				DocumentID:     "doc",
				ChunkID:        "chunk",
				ChunkHash:      "hash",
				Text:           "policy text",
				FreshnessScore: 1,
			},
			Score: 0.9,
			Rank:  i + 1,
		}
	}
	e := Engine{
		Config: TimeoutConfig{
			Total:        500 * time.Millisecond,
			QdrantSearch: 80 * time.Millisecond,
			OpenFGACheck: 150 * time.Millisecond,
			AuditWrite:   30 * time.Millisecond,
		},
		Backend: fakeRetrieval{candidates: candidates},
		ACL:     slowACL{delay: 40 * time.Millisecond, allowed: true},
		Auditor: memoryAudit{},
	}

	started := time.Now()
	resp := e.Execute(context.Background(), runtime.QueryRequest{
		TenantID: "acme",
		Region:   "US",
		UserID:   "finance_user",
		Question: "policy",
	})
	elapsed := time.Since(started)

	if resp.Trace.BlockedByACL != 0 {
		t.Fatalf("expected no ACL blocks, got %+v", resp.Trace)
	}
	if len(resp.Citations) == 0 {
		t.Fatalf("expected permitted citations, got %+v", resp)
	}
	if elapsed > 120*time.Millisecond {
		t.Fatalf("ACL checks appear sequential; elapsed %s", elapsed)
	}
}

func TestExecuteAllowsAuthorizedDocument(t *testing.T) {
	e := testEngineWithACL(true)

	resp := e.Execute(context.Background(), runtime.QueryRequest{TenantID: "acme", Region: "US", UserID: "finance_user", Question: "policy"})

	if len(resp.Citations) != 1 {
		t.Fatalf("expected authorized citation returned, got %+v", resp)
	}
	if resp.Trace.BlockedByACL != 0 || resp.Trace.AccessDecisions[0].Reason != "allowed" {
		t.Fatalf("expected allow-path trace, got %+v", resp.Trace)
	}
}

func TestExecuteBlocksCrossTenantCandidate(t *testing.T) {
	candidates := testCandidates(1)
	candidates[0].Chunk.TenantID = "other_tenant"
	e := Engine{
		Config:  testTimeouts(),
		Backend: fakeRetrieval{candidates: candidates},
		ACL:     slowACL{allowed: true},
		Auditor: memoryAudit{},
	}

	resp := e.Execute(context.Background(), runtime.QueryRequest{TenantID: "acme", Region: "US", UserID: "finance_user", Question: "policy"})

	if len(resp.Citations) != 0 {
		t.Fatalf("cross-tenant candidate leaked: %+v", resp.Citations)
	}
	if resp.Trace.BlockedByResidency != 1 || resp.Trace.AccessDecisions[0].Reason != "wrong_tenant" {
		t.Fatalf("expected wrong_tenant decision, got %+v", resp.Trace)
	}
}

func TestClassifyACLErrors(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{runtime.ErrCircuitOpen, "acl_circuit_open"},
		{runtime.ErrACLTimeout, "acl_timeout"},
		{runtime.ErrACLModelMissing, "acl_model_missing"},
		{runtime.ErrACLBackendUnavailable, "acl_backend_unavailable"},
	}
	for _, tc := range cases {
		if got := classifyACLError(tc.err); got != tc.want {
			t.Fatalf("classifyACLError(%v)=%s want %s", tc.err, got, tc.want)
		}
	}
}

func TestExecuteFailsClosedOnRetrievalTimeout(t *testing.T) {
	e := Engine{
		Config: TimeoutConfig{
			Total:        300 * time.Millisecond,
			QdrantSearch: 20 * time.Millisecond,
			OpenFGACheck: 60 * time.Millisecond,
			AuditWrite:   30 * time.Millisecond,
		},
		Backend: slowRetrieval{delay: 80 * time.Millisecond},
		ACL:     slowACL{allowed: true},
		Auditor: memoryAudit{},
	}

	resp := e.Execute(context.Background(), runtime.QueryRequest{
		TenantID: "acme",
		Region:   "US",
		UserID:   "finance_user",
		Question: "policy",
	})

	if resp.Confidence != 0 || len(resp.Citations) != 0 {
		t.Fatalf("expected zero-chunk fail closed response, got %+v", resp)
	}
	if resp.Trace.FailureStage != "qdrant" || resp.Trace.ErrorCode != "qdrant_timeout" {
		t.Fatalf("expected qdrant timeout trace, got %+v", resp.Trace)
	}
}

func TestExecuteFailsClosedWhenACLCircuitOpen(t *testing.T) {
	e := Engine{
		Config: TimeoutConfig{
			Total:        300 * time.Millisecond,
			QdrantSearch: 80 * time.Millisecond,
			OpenFGACheck: 60 * time.Millisecond,
			AuditWrite:   30 * time.Millisecond,
		},
		Backend: fakeRetrieval{candidates: []runtime.Candidate{{
			Chunk: runtime.Chunk{TenantID: "acme", Region: "US", DocumentID: "doc", ChunkID: "chunk", ChunkHash: "hash", Text: "text", FreshnessScore: 1},
			Score: 0.9,
			Rank:  1,
		}}},
		ACL:        errorACL{},
		Auditor:    memoryAudit{},
		ACLCircuit: NewCircuitBreaker(1, time.Second),
	}
	req := runtime.QueryRequest{TenantID: "acme", Region: "US", UserID: "finance_user", Question: "policy"}
	first := e.Execute(context.Background(), req)
	second := e.Execute(context.Background(), req)

	if first.Trace.BlockedByACL != 1 {
		t.Fatalf("expected first ACL failure to block chunk, got %+v", first.Trace)
	}
	if second.Trace.BlockedByACL != 1 || second.Trace.AccessDecisions[0].Reason != "acl_circuit_open_fail_closed" {
		t.Fatalf("expected open ACL circuit to block immediately, got %+v", second.Trace)
	}
}

func TestMetrics_QueryTotal_Allowed(t *testing.T) {
	gwmetrics.RegisterAll()
	before := testutil.ToFloat64(gwmetrics.QueryTotal.WithLabelValues("acme", "allowed"))
	e := testEngineWithACL(true)

	resp := e.Execute(context.Background(), runtime.QueryRequest{TenantID: "acme", Region: "US", UserID: "finance_user", Question: "policy"})

	if len(resp.Citations) == 0 {
		t.Fatalf("expected allowed query, got %+v", resp)
	}
	_ = scrapeMetrics(t)
	after := testutil.ToFloat64(gwmetrics.QueryTotal.WithLabelValues("acme", "allowed"))
	if after-before != 1 {
		t.Fatalf("expected allowed query counter delta 1, got %f", after-before)
	}
}

func TestMetrics_QueryTotal_FailClosed(t *testing.T) {
	gwmetrics.RegisterAll()
	before := testutil.ToFloat64(gwmetrics.QueryTotal.WithLabelValues("acme", "fail_closed"))
	e := Engine{
		Config:  testTimeouts(),
		Backend: slowRetrieval{delay: 80 * time.Millisecond},
		ACL:     slowACL{allowed: true},
		Auditor: memoryAudit{},
	}

	resp := e.Execute(context.Background(), runtime.QueryRequest{TenantID: "acme", Region: "US", UserID: "finance_user", Question: "policy"})

	if resp.Trace.FailureStage == "" {
		t.Fatalf("expected fail closed trace, got %+v", resp.Trace)
	}
	_ = scrapeMetrics(t)
	after := testutil.ToFloat64(gwmetrics.QueryTotal.WithLabelValues("acme", "fail_closed"))
	if after-before != 1 {
		t.Fatalf("expected fail_closed query counter delta 1, got %f", after-before)
	}
}

func TestMetrics_ChunksBlocked(t *testing.T) {
	gwmetrics.RegisterAll()
	before := testutil.ToFloat64(gwmetrics.ChunksBlockedTotal.WithLabelValues("acme", "acl_denied"))
	e := testEngineWithACL(false)

	resp := e.Execute(context.Background(), runtime.QueryRequest{TenantID: "acme", Region: "US", UserID: "general_user", Question: "policy"})

	if resp.Trace.BlockedByACL == 0 {
		t.Fatalf("expected blocked ACL chunks, got %+v", resp.Trace)
	}
	_ = scrapeMetrics(t)
	after := testutil.ToFloat64(gwmetrics.ChunksBlockedTotal.WithLabelValues("acme", "acl_denied"))
	if after <= before {
		t.Fatalf("expected blocked chunks counter to increase, before=%f after=%f", before, after)
	}
}

func TestAuditLog_ImmutableDigest(t *testing.T) {
	entry := AuditEntry{
		TraceID:             "trace-1",
		TenantID:            "11111111-1111-1111-1111-111111111111",
		UserID:              "finance_user",
		QueryHash:           hashText("policy"),
		TimestampUTC:        time.Unix(100, 20).UTC(),
		Region:              "US",
		CandidatesRetrieved: 3,
		CandidatesAllowed:   1,
		CandidatesBlocked:   2,
		FailClosed:          false,
		TotalLatencyMs:      42,
		CircuitBreakerState: "closed",
	}

	first := ComputeDigest(entry)
	entry.ImmutableDigest = "tampered"
	second := ComputeDigest(entry)

	if first == "" || first != second {
		t.Fatalf("expected stable digest excluding ImmutableDigest, first=%q second=%q", first, second)
	}
}

func TestAuditLog_NoUpdateRule(t *testing.T) {
	db := auditTestDB(t)
	installAuditMigration(t, db)
	writer := NewPostgresAuditWriter(db)
	entry := testAuditEntry("trace-update")
	if err := writer.Write(context.Background(), entry); err != nil {
		t.Fatalf("write audit entry: %v", err)
	}
	if _, err := db.Exec(`UPDATE audit_log SET user_id = 'attacker' WHERE trace_id = $1`, entry.TraceID); err != nil {
		t.Fatalf("update should be ignored by rule, got error: %v", err)
	}
	var userID string
	if err := db.QueryRow(`SELECT user_id FROM audit_log WHERE trace_id = $1`, entry.TraceID).Scan(&userID); err != nil {
		t.Fatalf("read audit row: %v", err)
	}
	if userID != entry.UserID {
		t.Fatalf("expected write-once row unchanged, got user_id=%s", userID)
	}
}

func TestAuditLog_NoDeleteRule(t *testing.T) {
	db := auditTestDB(t)
	installAuditMigration(t, db)
	writer := NewPostgresAuditWriter(db)
	entry := testAuditEntry("trace-delete")
	if err := writer.Write(context.Background(), entry); err != nil {
		t.Fatalf("write audit entry: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM audit_log WHERE trace_id = $1`, entry.TraceID); err != nil {
		t.Fatalf("delete should be ignored by rule, got error: %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE trace_id = $1`, entry.TraceID).Scan(&count); err != nil {
		t.Fatalf("count audit row: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected row to survive delete, count=%d", count)
	}
}

func TestAuditWrite_FailsEngine(t *testing.T) {
	e := testEngineWithACL(true)
	e.Auditor = failingAudit{}

	resp := e.Execute(context.Background(), runtime.QueryRequest{TenantID: "acme", Region: "US", UserID: "finance_user", Question: "policy"})

	if resp.Trace.FailureStage != "audit" || resp.Trace.ErrorCode != "audit_write_failed" {
		t.Fatalf("expected audit write fail closed, got %+v", resp.Trace)
	}
	if len(resp.Citations) != 0 {
		t.Fatalf("audit failure must return zero chunks, got %+v", resp.Citations)
	}
}

func testEngineWithACL(allowed bool) Engine {
	return Engine{
		Config:  testTimeouts(),
		Backend: fakeRetrieval{candidates: testCandidates(1)},
		ACL:     slowACL{allowed: allowed},
		Auditor: memoryAudit{},
	}
}

func testTimeouts() TimeoutConfig {
	return TimeoutConfig{
		Total:        300 * time.Millisecond,
		QdrantSearch: 20 * time.Millisecond,
		OpenFGACheck: 60 * time.Millisecond,
		AuditWrite:   30 * time.Millisecond,
	}
}

func testCandidates(count int) []runtime.Candidate {
	candidates := make([]runtime.Candidate, count)
	for i := range candidates {
		candidates[i] = runtime.Candidate{
			Chunk: runtime.Chunk{
				TenantID:       "acme",
				Region:         "US",
				DocumentID:     "doc",
				ChunkID:        "chunk",
				ChunkHash:      "hash",
				Text:           "policy text",
				FreshnessScore: 1,
			},
			Score: 0.9,
			Rank:  i + 1,
		}
	}
	return candidates
}

func scrapeMetrics(t *testing.T) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	promhttp.Handler().ServeHTTP(rec, req)
	body, err := io.ReadAll(rec.Body)
	if err != nil {
		t.Fatalf("read metrics body: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected metrics scrape 200, got %d: %s", rec.Code, string(body))
	}
	return string(body)
}

func auditTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping Postgres audit rule integration test")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func installAuditMigration(t *testing.T, db *sql.DB) {
	t.Helper()
	sqlText, err := os.ReadFile("../../../../migrations/003_create_audit_log.up.sql")
	if err != nil {
		sqlText, err = os.ReadFile("../../../migrations/003_create_audit_log.up.sql")
	}
	if err != nil {
		t.Fatalf("read audit migration: %v", err)
	}
	_, _ = db.Exec(`DROP TABLE IF EXISTS audit_log CASCADE`)
	if _, err := db.Exec(string(sqlText)); err != nil {
		t.Fatalf("execute audit migration: %v", err)
	}
}

func testAuditEntry(traceID string) AuditEntry {
	return AuditEntry{
		TraceID:             traceID,
		TenantID:            "11111111-1111-1111-1111-111111111111",
		UserID:              "finance_user",
		QueryHash:           hashText("policy"),
		TimestampUTC:        time.Now().UTC(),
		Region:              "US",
		CandidatesRetrieved: 3,
		CandidatesAllowed:   1,
		CandidatesBlocked:   2,
		TotalLatencyMs:      42,
		CircuitBreakerState: "closed",
	}
}

type fakeRetrieval struct {
	candidates []runtime.Candidate
	err        error
}

func (f fakeRetrieval) Retrieve(ctx context.Context, req runtime.QueryRequest, limit int) ([]runtime.Candidate, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.candidates, nil
}

type slowRetrieval struct {
	delay time.Duration
}

func (s slowRetrieval) Retrieve(ctx context.Context, req runtime.QueryRequest, limit int) ([]runtime.Candidate, error) {
	select {
	case <-time.After(s.delay):
		return nil, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type slowACL struct {
	delay   time.Duration
	allowed bool
}

func (s slowACL) CanAccess(ctx context.Context, req runtime.QueryRequest, chunk runtime.Chunk) (bool, error) {
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	return s.allowed, nil
}

type errorACL struct{}

func (errorACL) CanAccess(context.Context, runtime.QueryRequest, runtime.Chunk) (bool, error) {
	return false, errors.New("openfga unavailable")
}

type memoryAudit struct{}

func (memoryAudit) Write(context.Context, AuditEntry) error {
	return nil
}

type failingAudit struct{}

func (failingAudit) Write(context.Context, AuditEntry) error {
	return errors.New("audit insert failed")
}
