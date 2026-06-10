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

// TestComputeDigest_PR21FieldsNotChained pins the invariant that the
// PR #21 additions (AgentKeyID + AgentKeyName + AccessDecisions) DO NOT
// participate in the digest payload. If this test fails, every audit
// row written before PR #21 will stop verifying — the existing
// TestVerifyChain* tests above would catch it indirectly, but this
// test makes the contract explicit so a future change to ComputeDigest
// fails loudly with a single message.
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
	enriched.AgentKeyID = 42
	enriched.AgentKeyName = "service-account-treasury"
	enriched.AccessDecisions = []runtime.AccessDecision{
		{ChunkID: "c1", DocumentID: "d1", Allowed: true, Reason: "allowed", Region: "US"},
		{ChunkID: "c2", DocumentID: "d2", Allowed: false, Reason: "acl_denied", Region: "US"},
	}
	if ComputeDigest(base) != ComputeDigest(enriched) {
		t.Fatalf("digest payload must not depend on AgentKey* or AccessDecisions; " +
			"if it does, all pre-PR21 audit rows stop verifying")
	}
}

// TestVerifyChain_AcrossPR21Boundary builds a chain where the first
// entry is shaped like a pre-PR21 row (no agent key, no decisions) and
// the second carries the new fields, then verifies the chain links.
// The point is: a deployment that upgrades MID-CHAIN must keep
// verifying without a re-anchor.
func TestVerifyChain_AcrossPR21Boundary(t *testing.T) {
	preBaseline := sampleEntry("pre", "alice") // no agent key, no decisions
	postUpgrade := sampleEntry("post", "bob")
	postUpgrade.AgentKeyID = 42
	postUpgrade.AgentKeyName = "agent_treasury"
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
	entry.AgentKeyID = 42
	entry.AgentKeyName = "agent_pr21"
	entry.AccessDecisions = []runtime.AccessDecision{
		{ChunkID: "c1", DocumentID: "doc_a", Allowed: true, Reason: "allowed", Region: "US"},
		{ChunkID: "c2", DocumentID: "doc_b", Allowed: false, Reason: "acl_denied", Region: "US", RequiredScope: "RestrictedDocs"},
	}
	if err := writer.Write(context.Background(), entry); err != nil {
		t.Fatalf("write entry: %v", err)
	}

	// (1) JSONB blob exists, agent_key columns round-trip, decisions
	// deserialise back to the same two records.
	var blob []byte
	var keyID sql.NullInt64
	var keyName sql.NullString
	if err := db.QueryRow(`SELECT agent_key_id, agent_key_name, access_decisions FROM audit_log WHERE trace_id = $1`, entry.TraceID).Scan(&keyID, &keyName, &blob); err != nil {
		t.Fatalf("read audit_log row: %v", err)
	}
	if !keyID.Valid || keyID.Int64 != 42 {
		t.Fatalf("agent_key_id round-trip: want 42, got %v", keyID)
	}
	if !keyName.Valid || keyName.String != "agent_pr21" {
		t.Fatalf("agent_key_name round-trip: want 'agent_pr21', got %v", keyName)
	}
	var roundTripped []runtime.AccessDecision
	if err := json.Unmarshal(blob, &roundTripped); err != nil {
		t.Fatalf("unmarshal access_decisions: %v (raw=%s)", err, string(blob))
	}
	if len(roundTripped) != 2 {
		t.Fatalf("JSONB decisions: want 2, got %d (raw=%s)", len(roundTripped), string(blob))
	}

	// (2) Normalised rows: two rows in audit_log_decisions, in ordinal
	// order, with tenant_id propagated from the parent.
	rows, err := db.Query(`
		SELECT ordinal, tenant_id, chunk_id, document_id, allowed, reason, required_scope, region
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
		tenant  string
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
		if err := rows.Scan(&r.ord, &r.tenant, &r.chunk, &r.doc, &r.allowed, &r.reason, &r.scope, &r.region); err != nil {
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
	// PR #21 CI-3 contract: tenant_id is denormalised onto every
	// decision row and pinned equal to the parent's tenant_id by
	// the writer.
	for i, r := range got {
		if r.tenant != entry.TenantID {
			t.Fatalf("row %d tenant_id mismatch: want %q, got %q (parent.tenant_id pinning broken)", i, entry.TenantID, r.tenant)
		}
	}
}

// TestPostgresAuditWriter_DecisionRowsAreImmutable verifies CI-2's
// mitigation: the no_update / no_delete RULES on audit_log_decisions
// match audit_log's, so per-chunk decisions cannot be silently
// rewritten by anyone with table-write privileges. TEST_DATABASE_URL-
// gated.
func TestPostgresAuditWriter_DecisionRowsAreImmutable(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping decision immutability test")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	installAuditMigration(t, db)
	writer := NewPostgresAuditWriter(db)

	entry := testAuditEntry("trace-pr21-immutable")
	entry.AccessDecisions = []runtime.AccessDecision{
		{ChunkID: "c1", DocumentID: "doc_a", Allowed: false, Reason: "acl_denied", Region: "US"},
	}
	if err := writer.Write(context.Background(), entry); err != nil {
		t.Fatalf("write entry: %v", err)
	}

	// UPDATE must be a no-op (RULE … DO INSTEAD NOTHING). No error,
	// no row mutation.
	if _, err := db.Exec(`UPDATE audit_log_decisions SET allowed = true WHERE trace_id = $1`, entry.TraceID); err != nil {
		t.Fatalf("UPDATE should be silently dropped by rule, got error: %v", err)
	}
	var allowed bool
	if err := db.QueryRow(`SELECT allowed FROM audit_log_decisions WHERE trace_id = $1`, entry.TraceID).Scan(&allowed); err != nil {
		t.Fatalf("read decision row: %v", err)
	}
	if allowed {
		t.Fatalf("decision row was mutated through UPDATE — no_update_audit_decisions rule missing")
	}

	// DELETE must be a no-op.
	if _, err := db.Exec(`DELETE FROM audit_log_decisions WHERE trace_id = $1`, entry.TraceID); err != nil {
		t.Fatalf("DELETE should be silently dropped by rule, got error: %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_log_decisions WHERE trace_id = $1`, entry.TraceID).Scan(&count); err != nil {
		t.Fatalf("count decision rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("decision row deleted — no_delete_audit_decisions rule missing (count=%d)", count)
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
	// No AgentKey* set, no AccessDecisions set — the "writer running
	// without an API key context" path.
	if err := writer.Write(context.Background(), entry); err != nil {
		t.Fatalf("write entry: %v", err)
	}
	var keyID sql.NullInt64
	var keyName sql.NullString
	var blob sql.NullString
	if err := db.QueryRow(`SELECT agent_key_id, agent_key_name, access_decisions FROM audit_log WHERE trace_id = $1`, entry.TraceID).Scan(&keyID, &keyName, &blob); err != nil {
		t.Fatalf("read audit_log row: %v", err)
	}
	if keyID.Valid {
		t.Fatalf("agent_key_id should be NULL when unset, got %d", keyID.Int64)
	}
	if keyName.Valid {
		t.Fatalf("agent_key_name should be NULL when unset, got %q", keyName.String)
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
