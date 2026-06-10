package engine

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"groundwork/query-runtime/internal/runtime"
)

// auditQueryTestDB returns a *sql.DB pointing at TEST_DATABASE_URL with
// the PR #17/#21 audit migrations applied. Skips if the env var is
// unset. Same pattern as engine/engine_test.go's installAuditMigration.
func auditQueryTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping audit-query Postgres tests")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	installAuditMigration(t, db)
	return db
}

// seedAuditEntries writes N audit entries spanning a time range, with
// alternating fail_closed flags, two agent ids, and two decision
// modes. Returns the entries written in write order (oldest first).
//
// Trace ids embed the tenant id and a fresh per-call nonce so two
// invocations in the same wall-clock second (e.g. seeding two tenants
// back-to-back in one test) don't collide against
// audit_log_trace_id_key.
func seedAuditEntries(t *testing.T, db *sql.DB, tenantID string, n int) []AuditEntry {
	t.Helper()
	writer := NewPostgresAuditWriter(db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	nonce := time.Now().UnixNano()
	out := make([]AuditEntry, 0, n)
	for i := 0; i < n; i++ {
		entry := AuditEntry{
			TraceID:             traceKey(tenantID, nonce, i),
			TenantID:            tenantID,
			UserID:              "alice",
			QueryHash:           hashText("policy"),
			TimestampUTC:        now.Add(-time.Duration(n-i) * time.Minute),
			Region:              "US",
			CandidatesRetrieved: 5,
			CandidatesAllowed:   3,
			CandidatesBlocked:   2,
			FailClosed:          i%3 == 0, // every 3rd entry is fail_closed
			TotalLatencyMs:      40 + i,
			CircuitBreakerState: "closed",
			DecisionMode:        "engine_live_acl_fail_closed",
			ACLDecision:         "allowed",
			Reason:              "allowed",
			AgentKeyID:          int64(1 + i%2), // 1 or 2
			AgentKeyName:        agentName(i % 2),
			AccessDecisions: []runtime.AccessDecision{
				{ChunkID: chunkKey(tenantID, nonce, i*2), DocumentID: "doc_a", Allowed: true, Reason: "allowed", Region: "US"},
				{ChunkID: chunkKey(tenantID, nonce, i*2+1), DocumentID: "doc_b", Allowed: false, Reason: "acl_denied", Region: "US"},
			},
		}
		if entry.FailClosed {
			entry.DecisionMode = "engine_fail_closed"
			entry.ACLDecision = "fail_closed"
			entry.Reason = "qdrant_timeout"
		}
		if err := writer.Write(context.Background(), entry); err != nil {
			t.Fatalf("seed entry %d: %v", i, err)
		}
		out = append(out, entry)
	}
	return out
}

func traceKey(tenantID string, nonce int64, idx int) string {
	return "trace_" + tenantID + "_" + intToString64(nonce) + "_" + intToString(idx)
}
func chunkKey(tenantID string, nonce int64, idx int) string {
	return "chunk_" + tenantID + "_" + intToString64(nonce) + "_" + intToString(idx)
}
func intToString(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
func intToString64(n int64) string {
	return intToString(int(n))
}
func agentName(idx int) string {
	if idx == 0 {
		return "treasury-agent"
	}
	return "compliance-agent"
}

// resetAuditTables wipes audit rows for a tenant between subtests so
// the seeder counts are deterministic. Direct DELETEs bypass the
// no_delete rules — we use TRUNCATE which the rules don't intercept.
func resetAuditTables(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`TRUNCATE audit_log_decisions, audit_log RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate audit tables: %v", err)
	}
}

// ---------------------------------------------------------------------
// ListAuditEntries
// ---------------------------------------------------------------------

func TestPostgresAuditReader_ListAuditEntries_Pagination(t *testing.T) {
	db := auditQueryTestDB(t)
	resetAuditTables(t, db)
	written := seedAuditEntries(t, db, "tenant_a", 7)

	reader := NewPostgresAuditReader(db)
	page1, err := reader.ListAuditEntries(context.Background(), "tenant_a", runtime.AuditFilter{}, 3, "")
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if len(page1.Entries) != 3 {
		t.Fatalf("page 1: want 3 entries, got %d", len(page1.Entries))
	}
	if page1.NextCursor == "" {
		t.Fatal("page 1: cursor must be non-empty when more pages exist")
	}
	// Newest-first ordering: page 1 must start with the most recently
	// written entry (the last in `written`).
	if page1.Entries[0].TraceID != written[len(written)-1].TraceID {
		t.Fatalf("page 1: newest-first violated. first=%q want=%q", page1.Entries[0].TraceID, written[len(written)-1].TraceID)
	}

	page2, err := reader.ListAuditEntries(context.Background(), "tenant_a", runtime.AuditFilter{}, 3, page1.NextCursor)
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if len(page2.Entries) != 3 {
		t.Fatalf("page 2: want 3 entries, got %d", len(page2.Entries))
	}
	page3, err := reader.ListAuditEntries(context.Background(), "tenant_a", runtime.AuditFilter{}, 3, page2.NextCursor)
	if err != nil {
		t.Fatalf("page 3: %v", err)
	}
	if len(page3.Entries) != 1 {
		t.Fatalf("page 3: want 1 entry, got %d", len(page3.Entries))
	}
	if page3.NextCursor != "" {
		t.Fatalf("page 3: cursor must be empty on last page, got %q", page3.NextCursor)
	}
	// Round-trip: every entry appears exactly once across the three pages.
	seen := map[string]bool{}
	for _, p := range []runtime.AuditPage{page1, page2, page3} {
		for _, e := range p.Entries {
			if seen[e.TraceID] {
				t.Fatalf("trace_id %s appeared twice across pages", e.TraceID)
			}
			seen[e.TraceID] = true
		}
	}
	if len(seen) != 7 {
		t.Fatalf("expected 7 unique entries across pages, got %d", len(seen))
	}
}

func TestPostgresAuditReader_ListAuditEntries_TenantIsolation(t *testing.T) {
	db := auditQueryTestDB(t)
	resetAuditTables(t, db)
	seedAuditEntries(t, db, "tenant_a", 3)
	seedAuditEntries(t, db, "tenant_b", 5)

	reader := NewPostgresAuditReader(db)
	pageA, err := reader.ListAuditEntries(context.Background(), "tenant_a", runtime.AuditFilter{}, 100, "")
	if err != nil {
		t.Fatalf("list a: %v", err)
	}
	if len(pageA.Entries) != 3 {
		t.Fatalf("tenant_a: want 3 entries, got %d (cross-tenant leak)", len(pageA.Entries))
	}
	for _, e := range pageA.Entries {
		if e.TenantID != "tenant_a" {
			t.Fatalf("cross-tenant row leaked: %+v", e)
		}
	}
	pageB, err := reader.ListAuditEntries(context.Background(), "tenant_b", runtime.AuditFilter{}, 100, "")
	if err != nil {
		t.Fatalf("list b: %v", err)
	}
	if len(pageB.Entries) != 5 {
		t.Fatalf("tenant_b: want 5 entries, got %d", len(pageB.Entries))
	}
}

func TestPostgresAuditReader_ListAuditEntries_AppliesFilters(t *testing.T) {
	db := auditQueryTestDB(t)
	resetAuditTables(t, db)
	seedAuditEntries(t, db, "tenant_a", 12) // 4 fail-closed (every 3rd), 8 not; 6 agent_key_id=1, 6 agent_key_id=2

	reader := NewPostgresAuditReader(db)
	ctx := context.Background()

	// agent_key_id filter
	pageAgent, err := reader.ListAuditEntries(ctx, "tenant_a", runtime.AuditFilter{AgentKeyID: 1}, 100, "")
	if err != nil {
		t.Fatalf("filter agent: %v", err)
	}
	if len(pageAgent.Entries) != 6 {
		t.Fatalf("agent_key_id=1: want 6, got %d", len(pageAgent.Entries))
	}
	for _, e := range pageAgent.Entries {
		if e.AgentKeyID != 1 {
			t.Fatalf("agent filter leaked agent_key_id=%d", e.AgentKeyID)
		}
	}

	// fail_closed filter
	yes := true
	pageFC, err := reader.ListAuditEntries(ctx, "tenant_a", runtime.AuditFilter{FailClosed: &yes}, 100, "")
	if err != nil {
		t.Fatalf("filter fc: %v", err)
	}
	for _, e := range pageFC.Entries {
		if !e.FailClosed {
			t.Fatalf("fail_closed=true filter leaked a non-fail-closed row")
		}
	}
	if len(pageFC.Entries) == 0 {
		t.Fatal("expected some fail-closed rows; seeder writes every 3rd")
	}

	// decision_mode filter
	pageMode, err := reader.ListAuditEntries(ctx, "tenant_a", runtime.AuditFilter{DecisionMode: "engine_fail_closed"}, 100, "")
	if err != nil {
		t.Fatalf("filter mode: %v", err)
	}
	for _, e := range pageMode.Entries {
		if e.DecisionMode != "engine_fail_closed" {
			t.Fatalf("decision_mode filter leaked: %q", e.DecisionMode)
		}
	}
}

// ---------------------------------------------------------------------
// GetAuditEntry
// ---------------------------------------------------------------------

func TestPostgresAuditReader_GetAuditEntry_LoadsDecisions(t *testing.T) {
	db := auditQueryTestDB(t)
	resetAuditTables(t, db)
	written := seedAuditEntries(t, db, "tenant_a", 1)

	reader := NewPostgresAuditReader(db)
	got, err := reader.GetAuditEntry(context.Background(), "tenant_a", written[0].TraceID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.TraceID != written[0].TraceID || got.TenantID != "tenant_a" {
		t.Fatalf("got wrong row: %+v", got)
	}
	if len(got.AccessDecisions) != 2 {
		t.Fatalf("decisions: want 2 (one allow + one deny), got %d", len(got.AccessDecisions))
	}
	if got.AccessDecisions[0].Allowed != true || got.AccessDecisions[1].Allowed != false {
		t.Fatalf("decisions ordering by ordinal broken: %+v", got.AccessDecisions)
	}
}

func TestPostgresAuditReader_GetAuditEntry_TenantIsolation(t *testing.T) {
	db := auditQueryTestDB(t)
	resetAuditTables(t, db)
	written := seedAuditEntries(t, db, "tenant_a", 1)

	reader := NewPostgresAuditReader(db)
	// Same trace_id exists in tenant_a but is being requested under
	// tenant_b — must return NotFound. (UNIQUE(trace_id) on audit_log
	// means there's only one row globally; the tenant scope prevents
	// disclosure.)
	_, err := reader.GetAuditEntry(context.Background(), "tenant_b", written[0].TraceID)
	if !errors.Is(err, runtime.ErrAuditEntryNotFound) {
		t.Fatalf("expected ErrAuditEntryNotFound for cross-tenant lookup, got %v", err)
	}
}

func TestPostgresAuditReader_GetAuditEntry_MissingReturnsNotFound(t *testing.T) {
	db := auditQueryTestDB(t)
	resetAuditTables(t, db)
	reader := NewPostgresAuditReader(db)
	_, err := reader.GetAuditEntry(context.Background(), "tenant_a", "does-not-exist")
	if !errors.Is(err, runtime.ErrAuditEntryNotFound) {
		t.Fatalf("expected ErrAuditEntryNotFound, got %v", err)
	}
}

// ---------------------------------------------------------------------
// ListAuditStats
// ---------------------------------------------------------------------

func TestPostgresAuditReader_ListAuditStats(t *testing.T) {
	db := auditQueryTestDB(t)
	resetAuditTables(t, db)
	seedAuditEntries(t, db, "tenant_a", 12)
	seedAuditEntries(t, db, "tenant_b", 5)

	reader := NewPostgresAuditReader(db)
	stats, err := reader.ListAuditStats(context.Background(), "tenant_a", time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.TotalQueries != 12 {
		t.Fatalf("tenant_a total: want 12, got %d (cross-tenant leak)", stats.TotalQueries)
	}
	// fail_closed every 3rd: indices 0, 3, 6, 9 → 4 entries.
	if stats.FailClosedCount != 4 {
		t.Fatalf("fail_closed count: want 4, got %d", stats.FailClosedCount)
	}
	if stats.ByDecisionMode["engine_fail_closed"] != 4 {
		t.Fatalf("decision_mode breakdown: %+v", stats.ByDecisionMode)
	}
	if stats.ByDecisionMode["engine_live_acl_fail_closed"] != 8 {
		t.Fatalf("decision_mode breakdown: %+v", stats.ByDecisionMode)
	}
	if len(stats.TopAgents) != 2 {
		t.Fatalf("top_agents: want 2 agents (1,2), got %d (%+v)", len(stats.TopAgents), stats.TopAgents)
	}
	if stats.TopAgents[0].Count+stats.TopAgents[1].Count != 12 {
		t.Fatalf("top_agents total mismatch: %+v", stats.TopAgents)
	}
}

// ---------------------------------------------------------------------
// VerifyTenantChain
// ---------------------------------------------------------------------

func TestPostgresAuditReader_VerifyTenantChain_Clean(t *testing.T) {
	db := auditQueryTestDB(t)
	resetAuditTables(t, db)
	seedAuditEntries(t, db, "tenant_a", 5)

	reader := NewPostgresAuditReader(db)
	result, err := reader.VerifyTenantChain(context.Background(), "tenant_a")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !result.Verified {
		t.Fatalf("chain must verify clean, got problems: %+v", result.Problems)
	}
	if result.EntriesChecked != 5 {
		t.Fatalf("entries_checked: want 5, got %d", result.EntriesChecked)
	}
}

func TestPostgresAuditReader_VerifyTenantChain_TenantIsolation(t *testing.T) {
	db := auditQueryTestDB(t)
	resetAuditTables(t, db)
	seedAuditEntries(t, db, "tenant_a", 3)
	seedAuditEntries(t, db, "tenant_b", 7)

	reader := NewPostgresAuditReader(db)
	resultA, _ := reader.VerifyTenantChain(context.Background(), "tenant_a")
	resultB, _ := reader.VerifyTenantChain(context.Background(), "tenant_b")
	if resultA.EntriesChecked != 3 {
		t.Fatalf("tenant_a entries_checked: want 3, got %d", resultA.EntriesChecked)
	}
	if resultB.EntriesChecked != 7 {
		t.Fatalf("tenant_b entries_checked: want 7, got %d", resultB.EntriesChecked)
	}
}

// ---------------------------------------------------------------------
// Cursor encoding round-trip (no DB needed)
// ---------------------------------------------------------------------

func TestAuditCursor_RoundTrip(t *testing.T) {
	ts := time.Date(2026, 6, 8, 14, 5, 30, 123_456_000, time.UTC)
	id := "01HF8X-pretend-uuid"
	c := encodeAuditCursor(ts, id)
	gotTS, gotID, err := decodeAuditCursor(c)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !gotTS.Equal(ts) {
		t.Fatalf("ts: want %v, got %v", ts, gotTS)
	}
	if gotID != id {
		t.Fatalf("id: want %q, got %q", id, gotID)
	}
}

func TestAuditCursor_RejectsGarbage(t *testing.T) {
	if _, _, err := decodeAuditCursor("!!!"); err == nil {
		t.Fatal("expected error for non-base64 cursor")
	}
	if _, _, err := decodeAuditCursor("aGVsbG8"); err == nil {
		t.Fatal("expected error for non-JSON payload")
	}
}
