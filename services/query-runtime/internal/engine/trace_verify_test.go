package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"
	"time"

	"groundwork/query-runtime/internal/runtime"
)

// buildChain produces a correctly hash-chained sequence of audit entries the same
// way PostgresAuditWriter.Write would (previous_hash = prior digest).
func buildChain(entries ...AuditEntry) []AuditEntry {
	prev := ""
	out := make([]AuditEntry, 0, len(entries))
	for _, e := range entries {
		if e.TimestampUTC.IsZero() {
			e.TimestampUTC = time.Now().UTC()
		}
		e.PreviousHash = prev
		e.ImmutableDigest = ComputeDigest(e)
		prev = e.ImmutableDigest
		out = append(out, e)
	}
	return out
}

func sampleEntry(trace, user string) AuditEntry {
	return AuditEntry{
		TraceID:             trace,
		TenantID:            "acme",
		UserID:              user,
		QueryHash:           hashText("policy"),
		TimestampUTC:        time.Now().UTC(),
		Region:              "US",
		CandidatesRetrieved: 3,
		CandidatesAllowed:   1,
		CandidatesBlocked:   2,
		TotalLatencyMs:      5,
		CircuitBreakerState: "closed",
		DecisionMode:        "engine_live_acl_fail_closed",
		ACLDecision:         "allowed",
		Reason:              "allowed",
	}
}

func TestVerifyChainCleanIsValid(t *testing.T) {
	chain := buildChain(sampleEntry("t1", "alice"), sampleEntry("t2", "bob"), sampleEntry("t3", "carol"))
	if problems := VerifyChain(chain); len(problems) != 0 {
		t.Fatalf("expected a clean chain to verify, got: %+v", problems)
	}
}

func TestVerifyChainDetectsModifiedRow(t *testing.T) {
	chain := buildChain(sampleEntry("t1", "alice"), sampleEntry("t2", "bob"), sampleEntry("t3", "carol"))
	// Tamper a row in place WITHOUT recomputing its digest, as an attacker editing the DB would.
	chain[1].UserID = "attacker"

	problems := VerifyChain(chain)
	if len(problems) == 0 {
		t.Fatal("expected the verifier to detect the modified row")
	}
	found := false
	for _, p := range problems {
		if p.Index == 1 && p.Kind == "digest_mismatch" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected digest_mismatch at index 1, got: %+v", problems)
	}
}

func TestVerifyChainDetectsBrokenLink(t *testing.T) {
	chain := buildChain(sampleEntry("t1", "alice"), sampleEntry("t2", "bob"), sampleEntry("t3", "carol"))
	// Drop the middle row (deletion / reordering): t3's previous_hash no longer matches.
	broken := []AuditEntry{chain[0], chain[2]}

	problems := VerifyChain(broken)
	found := false
	for _, p := range problems {
		if p.Kind == "broken_link" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a broken_link after deletion, got: %+v", problems)
	}
}

// TestComputeDigest_PR21FieldsNotChained pins the invariant that the two
// PR #21 additions (AgentID + AccessDecisions) DO NOT participate in the
// digest payload. If this test fails, every audit row written before
// PR #21 will stop verifying — the existing TestVerifyChain* tests above
// would catch it indirectly, but this test makes the contract explicit
// so a future change to ComputeDigest fails loudly with a single message.
func TestComputeDigest_PR21FieldsNotChained(t *testing.T) {
	base := AuditEntry{
		TraceID:             "trace-pr21",
		TenantID:            "tenant_acme",
		UserID:              "finance_user",
		QueryHash:           hashText("policy"),
		TimestampUTC:        time.Unix(1735689600, 0).UTC(),
		Region:              "US",
		CandidatesRetrieved: 4,
		CandidatesAllowed:   2,
		CandidatesBlocked:   2,
		TotalLatencyMs:      55,
		CircuitBreakerState: "closed",
		DecisionMode:        "engine_live_acl_fail_closed",
		ACLDecision:         "allowed",
		Reason:              "allowed",
	}
	enriched := base
	enriched.AgentID = "service-account-treasury"
	enriched.AccessDecisions = []runtime.AccessDecision{
		{ChunkID: "c1", DocumentID: "d1", Allowed: true, Reason: "allowed", Region: "US"},
		{ChunkID: "c2", DocumentID: "d2", Allowed: false, Reason: "acl_denied", Region: "US"},
	}
	if ComputeDigest(base) != ComputeDigest(enriched) {
		t.Fatalf("digest payload must not depend on AgentID or AccessDecisions; " +
			"if it does, all pre-PR21 audit rows stop verifying")
	}
}

// TestVerifyChain_AcrossPR21Boundary builds a chain where the first
// entry is shaped like a pre-PR21 row (no AgentID, no decisions) and
// the second carries both new fields, and verifies the chain links.
// The point is: a deployment that upgrades MID-CHAIN must keep
// verifying without a re-anchor.
func TestVerifyChain_AcrossPR21Boundary(t *testing.T) {
	preBaseline := sampleEntry("pre", "alice") // no AgentID, no decisions
	postUpgrade := sampleEntry("post", "bob")
	postUpgrade.AgentID = "agent_treasury"
	postUpgrade.AccessDecisions = []runtime.AccessDecision{
		{ChunkID: "c1", DocumentID: "d1", Allowed: true, Reason: "allowed"},
	}
	chain := buildChain(preBaseline, postUpgrade)
	if problems := VerifyChain(chain); len(problems) != 0 {
		t.Fatalf("chain crossing the PR21 upgrade boundary must verify clean, got: %+v", problems)
	}
}

// TestPostgresAuditWriter_DecisionRoundTrip writes one allow and one
// deny decision through the writer and verifies BOTH storage views:
// the JSONB blob on the audit_log row AND the normalised rows in
// audit_log_decisions. Both views must hold the same data — they are
// written in the same advisory-locked transaction so they cannot
// disagree. TEST_DATABASE_URL-gated.
func TestPostgresAuditWriter_DecisionRoundTrip(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping Postgres decision round-trip")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	installAuditMigration(t, db)
	writer := NewPostgresAuditWriter(db)

	entry := testAuditEntry("trace-pr21-rt")
	entry.AgentID = "agent_pr21"
	entry.AccessDecisions = []runtime.AccessDecision{
		{ChunkID: "c1", DocumentID: "doc_a", Allowed: true, Reason: "allowed", Region: "US"},
		{ChunkID: "c2", DocumentID: "doc_b", Allowed: false, Reason: "acl_denied", Region: "US", RequiredScope: "RestrictedDocs"},
	}
	if err := writer.Write(context.Background(), entry); err != nil {
		t.Fatalf("write entry: %v", err)
	}

	// (1) JSONB blob exists and round-trips the same two decisions.
	var blob []byte
	var agent sql.NullString
	if err := db.QueryRow(`SELECT agent_id, access_decisions FROM audit_log WHERE trace_id = $1`, entry.TraceID).Scan(&agent, &blob); err != nil {
		t.Fatalf("read audit_log row: %v", err)
	}
	if !agent.Valid || agent.String != "agent_pr21" {
		t.Fatalf("agent_id round-trip: want 'agent_pr21', got %v", agent)
	}
	var roundTripped []runtime.AccessDecision
	if err := json.Unmarshal(blob, &roundTripped); err != nil {
		t.Fatalf("unmarshal access_decisions: %v (raw=%s)", err, string(blob))
	}
	if len(roundTripped) != 2 {
		t.Fatalf("JSONB decisions: want 2, got %d (raw=%s)", len(roundTripped), string(blob))
	}

	// (2) Normalised rows: two rows in audit_log_decisions, in ordinal
	// order, with the same payload.
	rows, err := db.Query(`
		SELECT ordinal, chunk_id, document_id, allowed, reason, required_scope, region
		FROM audit_log_decisions
		WHERE trace_id = $1
		ORDER BY ordinal ASC
	`, entry.TraceID)
	if err != nil {
		t.Fatalf("read audit_log_decisions: %v", err)
	}
	defer rows.Close()
	type decisionRow struct {
		ord     int
		chunk   string
		doc     sql.NullString
		allowed bool
		reason  sql.NullString
		scope   sql.NullString
		region  sql.NullString
	}
	var got []decisionRow
	for rows.Next() {
		var r decisionRow
		if err := rows.Scan(&r.ord, &r.chunk, &r.doc, &r.allowed, &r.reason, &r.scope, &r.region); err != nil {
			t.Fatalf("scan row: %v", err)
		}
		got = append(got, r)
	}
	if len(got) != 2 {
		t.Fatalf("decision rows: want 2, got %d", len(got))
	}
	if got[0].ord != 0 || got[0].chunk != "c1" || got[0].doc.String != "doc_a" || !got[0].allowed {
		t.Fatalf("row 0 mismatch: %+v", got[0])
	}
	if got[1].ord != 1 || got[1].chunk != "c2" || got[1].doc.String != "doc_b" || got[1].allowed || got[1].scope.String != "RestrictedDocs" {
		t.Fatalf("row 1 mismatch: %+v", got[1])
	}
}

// TestPostgresAuditWriter_EmptyDecisionsAreNull verifies that the
// no-decisions case (e.g. a pre-PR21-shaped caller that doesn't
// populate the slice) stores NULL in access_decisions rather than
// "null" / "[]" JSONB literals, and inserts zero rows in
// audit_log_decisions. TEST_DATABASE_URL-gated.
func TestPostgresAuditWriter_EmptyDecisionsAreNull(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping empty-decisions test")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	installAuditMigration(t, db)
	writer := NewPostgresAuditWriter(db)

	entry := testAuditEntry("trace-pr21-empty")
	// No AgentID set, no AccessDecisions set — the "writer running
	// without an API key context" path.
	if err := writer.Write(context.Background(), entry); err != nil {
		t.Fatalf("write entry: %v", err)
	}
	var agent sql.NullString
	var blob sql.NullString
	if err := db.QueryRow(`SELECT agent_id, access_decisions FROM audit_log WHERE trace_id = $1`, entry.TraceID).Scan(&agent, &blob); err != nil {
		t.Fatalf("read audit_log row: %v", err)
	}
	if agent.Valid {
		t.Fatalf("agent_id should be NULL when unset, got %q", agent.String)
	}
	if blob.Valid {
		t.Fatalf("access_decisions should be NULL when no decisions, got %q", blob.String)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_log_decisions WHERE trace_id = $1`, entry.TraceID).Scan(&count); err != nil {
		t.Fatalf("count decisions: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected zero normalised decision rows, got %d", count)
	}
}
