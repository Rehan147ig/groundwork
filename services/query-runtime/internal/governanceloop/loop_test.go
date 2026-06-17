// Package governanceloop holds the V1 end-to-end test: it wires the REAL
// code paths of the whole Groundwork loop together and asserts the
// behavior a YC partner / security buyer is promised —
//
//	GitHub connector  →  OpenFGA tuples  →  engine enforcement (fail-closed)
//	→  hash-chained audit  →  chain verification  →  leak report
//
// Nothing here is stubbed except the vector-retrieval layer (Qdrant), which
// is replaced by a fixed candidate set — because the thing under test is the
// authorization + audit loop, not nearest-neighbor search. The ACL evaluator
// is the real aclsync.MemoryFGA (which resolves group→viewer inheritance the
// same way the production OpenFGA model does), the connector is the real
// github.Connector over the Acme MockClient, the audit writer is the real
// PostgresAuditWriter, and VerifyChain is the real chain verifier.
//
// The pure-in-memory assertions run on every `go test`. The audit-chain
// assertions additionally require TEST_DATABASE_URL (a real Postgres), the
// same convention as the rest of the suite.
package governanceloop

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"groundwork/query-runtime/internal/aclsync"
	"groundwork/query-runtime/internal/aclsync/github"
	"groundwork/query-runtime/internal/engine"
	"groundwork/query-runtime/internal/leakreport"
	"groundwork/query-runtime/internal/runtime"
)

const (
	tenant = "acme-financial"
	region = "US"
)

// fixedRetrieval is the stand-in for Qdrant: it returns one candidate chunk
// for a named document, tenant- and region-tagged so the engine's residency
// gate passes and only the ACL decision varies by user.
type fixedRetrieval struct{ docID string }

func (f fixedRetrieval) Retrieve(_ context.Context, _ runtime.QueryRequest, _ int) ([]runtime.Candidate, error) {
	return []runtime.Candidate{{
		Chunk: runtime.Chunk{
			TenantID: tenant, Region: region,
			DocumentID: f.docID, ChunkID: f.docID + "_c1", ChunkHash: "h1",
			Text: "synthetic content for " + f.docID, FreshnessScore: 1,
		},
		Score: 0.9, Rank: 1,
	}}, nil
}

// acmeFGA builds the real OpenFGA tuple set from the real GitHub connector
// over the Acme mock org, loaded into the in-memory evaluator.
func acmeFGA(t *testing.T) *aclsync.MemoryFGA {
	t.Helper()
	conn := github.NewConnector(github.NewMockClient(), tenant, nil)
	ps, err := conn.Snapshot(context.Background(), tenant)
	if err != nil {
		t.Fatalf("connector snapshot: %v", err)
	}
	fga := aclsync.NewMemoryFGA()
	if err := fga.WriteTuples(context.Background(), tenant, aclsync.PermissionSetToTuples(ps)); err != nil {
		t.Fatalf("write tuples: %v", err)
	}
	return fga
}

func newEngine(fga *aclsync.MemoryFGA, docID string, auditor engine.AuditWriter) *engine.Engine {
	return &engine.Engine{
		Config: engine.TimeoutConfig{
			Total: 2 * time.Second, Embedding: time.Second,
			QdrantSearch: time.Second, OpenFGACheck: time.Second, AuditWrite: 2 * time.Second,
		},
		Backend: fixedRetrieval{docID: docID},
		ACL:     fga, // *MemoryFGA implements runtime.ACLChecker directly
		Auditor: auditor,
	}
}

type capturingAudit struct{ entries []engine.AuditEntry }

func (c *capturingAudit) Write(_ context.Context, e engine.AuditEntry) error {
	c.entries = append(c.entries, e)
	return nil
}

// TestGovernanceLoop_EnforcesPerUser is the headline V1 proof, fully
// in-memory: the same document, the same agent, different users → opposite
// decisions, fail-closed, with the decision recorded on the audit entry.
func TestGovernanceLoop_EnforcesPerUser(t *testing.T) {
	fga := acmeFGA(t)

	cases := []struct {
		name            string
		user            string
		docID           string
		wantCitations   int
		wantACLDecision string
	}{
		{"Eve sees executive-strategy", "eve", "gh:executive-strategy", 1, "allowed"},
		{"Bob blocked from executive-strategy", "bob", "gh:executive-strategy", 0, "denied"},
		{"Alice sees finance-budget", "alice", "gh:finance-budget", 1, "allowed"},
		{"Carol blocked from payroll-system (eng repo)", "carol", "gh:payroll-system", 0, "denied"},
		{"Dave sees security-audit", "dave", "gh:security-audit", 1, "allowed"},
		// The planted leak: engineering CAN see finance-budget. Enforcement
		// honors the (mis)configuration; the leak report (below) flags it.
		{"Bob sees finance-budget (the planted leak)", "bob", "gh:finance-budget", 1, "allowed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			audit := &capturingAudit{}
			eng := newEngine(fga, tc.docID, audit)
			resp := eng.Execute(context.Background(), runtime.QueryRequest{
				TenantID: tenant, Region: region, UserID: tc.user, Question: "summarize " + tc.docID,
			})
			if len(resp.Citations) != tc.wantCitations {
				t.Fatalf("citations: want %d got %d", tc.wantCitations, len(resp.Citations))
			}
			if len(audit.entries) != 1 {
				t.Fatalf("expected exactly one audit entry, got %d", len(audit.entries))
			}
			if got := audit.entries[0].ACLDecision; got != tc.wantACLDecision {
				t.Fatalf("acl_decision: want %q got %q", tc.wantACLDecision, got)
			}
			// Fail-closed guarantee: a deny returns ZERO content.
			if tc.wantCitations == 0 && len(resp.Citations) != 0 {
				t.Fatalf("fail-closed violated: deny returned %d citations", len(resp.Citations))
			}
		})
	}
}

// TestGovernanceLoop_LeakReport proves the pre-emptive scan catches the
// engineering→finance-budget overexposure from the same connector output
// that drives enforcement — one source of truth, two surfaces.
func TestGovernanceLoop_LeakReport(t *testing.T) {
	conn := github.NewConnector(github.NewMockClient(), tenant, nil)
	ps, _ := conn.Snapshot(context.Background(), tenant)
	owners := map[string]string{
		"gh:finance-budget":       "finance-team",
		"gh:payroll-system":       "engineering-team",
		"gh:engineering-platform": "engineering-team",
		"gh:security-audit":       "security-team",
		"gh:executive-strategy":   "executive-team",
	}
	rep := leakreport.Analyze(ps, owners)
	var crossDept bool
	for _, f := range rep.Findings {
		if f.Kind == leakreport.KindCrossDepartment && f.DocumentID == "gh:finance-budget" && f.Group == "engineering-team" {
			crossDept = true
		}
	}
	if !crossDept {
		t.Fatalf("leak report missed the planted engineering→finance-budget leak: %+v", rep.Findings)
	}
}

// TestGovernanceLoop_AuditChain runs the full loop against a REAL Postgres:
// multiple governed queries write hash-chained audit rows, then VerifyChain
// confirms tamper-evidence end to end. TEST_DATABASE_URL-gated.
func TestGovernanceLoop_AuditChain(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping live-Postgres chain verification")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	installAuditSchema(t, db)

	fga := acmeFGA(t)
	writer := engine.NewPostgresAuditWriterWithTimeout(db, 2*time.Second)

	// Drive a realistic mix of allow/deny through the engine; each writes a
	// chained audit row.
	queries := []struct {
		user, doc string
	}{
		{"eve", "gh:executive-strategy"},
		{"bob", "gh:executive-strategy"},
		{"alice", "gh:finance-budget"},
		{"carol", "gh:payroll-system"},
		{"dave", "gh:security-audit"},
	}
	for _, q := range queries {
		eng := newEngine(fga, q.doc, writer)
		_ = eng.Execute(context.Background(), runtime.QueryRequest{
			TenantID: tenant, Region: region, UserID: q.user, Question: "q " + q.doc,
		})
	}

	entries, err := engine.LoadAuditChain(context.Background(), db, tenant)
	if err != nil {
		t.Fatalf("load chain: %v", err)
	}
	if len(entries) < len(queries) {
		t.Fatalf("expected >= %d audit entries, got %d", len(queries), len(entries))
	}
	if problems := engine.VerifyChain(entries); len(problems) != 0 {
		t.Fatalf("audit chain failed verification: %+v", problems)
	}

	// Tamper detection: mutate a row in place and confirm the chain breaks.
	if len(entries) > 1 {
		entries[1].UserID = "attacker"
		if problems := engine.VerifyChain(entries); len(problems) == 0 {
			t.Fatal("expected VerifyChain to detect the tampered row")
		}
	}
}

// installAuditSchema applies the audit migrations (003–013) to a clean DB.
func installAuditSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	_, _ = db.Exec(`DROP TABLE IF EXISTS audit_log_decisions CASCADE`)
	_, _ = db.Exec(`DROP TABLE IF EXISTS audit_log CASCADE`)
	migrations := []string{
		"003_create_audit_log.up.sql",
		"004_add_previous_hash.up.sql",
		"005_add_audit_decision_columns.up.sql",
		"007_add_audit_identity_columns.up.sql",
		"011_extend_audit_log.up.sql",
		"013_extend_audit_log_indexes_concurrently.up.sql",
	}
	for _, name := range migrations {
		sqlText, err := os.ReadFile("../../../../migrations/" + name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		runStatements(t, db, name, string(sqlText))
	}
}

// runStatements applies a migration. Migration 013 uses CREATE INDEX
// CONCURRENTLY, which cannot run inside the implicit transaction pgx wraps a
// multi-statement Exec in, so 013 is split and each statement is executed
// on its own (autocommit).
func runStatements(t *testing.T, db *sql.DB, name, sqlText string) {
	t.Helper()
	if name == "013_extend_audit_log_indexes_concurrently.up.sql" {
		for _, stmt := range splitSQL(sqlText) {
			if _, err := db.Exec(stmt); err != nil {
				t.Fatalf("migration %s stmt %q: %v", name, stmt, err)
			}
		}
		return
	}
	if _, err := db.Exec(sqlText); err != nil {
		t.Fatalf("migration %s: %v", name, err)
	}
}

func splitSQL(s string) []string {
	var out []string
	for _, raw := range splitOnSemicolon(s) {
		stmt := stripComments(raw)
		if stmt != "" {
			out = append(out, stmt)
		}
	}
	return out
}

func splitOnSemicolon(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ';' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func stripComments(s string) string {
	var keep []string
	for _, line := range splitLines(s) {
		if i := indexOf(line, "--"); i >= 0 {
			line = line[:i]
		}
		if t := trimSpace(line); t != "" {
			keep = append(keep, t)
		}
	}
	return trimSpace(join(keep, " "))
}

func splitLines(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	out = append(out, cur)
	return out
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
func trimSpace(s string) string {
	i, j := 0, len(s)
	for i < j && (s[i] == ' ' || s[i] == '\t' || s[i] == '\r' || s[i] == '\n') {
		i++
	}
	for j > i && (s[j-1] == ' ' || s[j-1] == '\t' || s[j-1] == '\r' || s[j-1] == '\n') {
		j--
	}
	return s[i:j]
}
func join(xs []string, sep string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += sep
		}
		out += x
	}
	return out
}
