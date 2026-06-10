package engine

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"groundwork/query-runtime/internal/runtime"
)

type AuditEntry struct {
	TraceID             string
	TenantID            string
	UserID              string
	QueryHash           string
	TimestampUTC        time.Time
	Region              string
	CandidatesRetrieved int
	CandidatesAllowed   int
	CandidatesBlocked   int
	FailClosed          bool
	FailStage           string
	ErrorCode           string
	ErrorMessage        string
	OpenFGALatencyMs    int
	QdrantLatencyMs     int
	TotalLatencyMs      int
	CircuitBreakerState string
	DecisionMode        string
	ACLDecision         string
	Reason              string
	// IdentityResolution records how the query-time identity was resolved when
	// canonical identity is enabled: "" (canonical off / not applicable), "skipped",
	// "resolved", or "unresolved". PrincipalID is the canonical principal UUID when
	// the identity resolved to user:principal:<uuid> (empty otherwise). Both feed the
	// immutable digest so an attacker cannot rewrite whose permissions were used.
	IdentityResolution string
	PrincipalID        string
	ImmutableDigest    string
	PreviousHash       string

	// AgentKeyID is the STABLE foreign-key identity of the API key that
	// made the call (TenantContext.KeyID = api_keys.id). PR #21 addition.
	// Used as the group-by key for Dashboard L2 and replay correlation;
	// never changes once the key is created. Zero when the writer is
	// invoked outside an API-key context (e.g. embedded test paths).
	AgentKeyID int64

	// AgentKeyName is the DISPLAY snapshot of the API key's name
	// (TenantContext.KeyName = api_keys.name) at write time. Because
	// api_keys.name is mutable (operators can rename keys), historical
	// audit rows preserve the name as it was when the call landed; the
	// Dashboard joins on AgentKeyID for grouping but renders AgentKeyName
	// for display.
	//
	// Both AgentKey* fields are deliberately NOT in the digest payload
	// (see ComputeDigest below). Pre-PR21 rows have both columns NULL
	// and must keep verifying under the old formula. The Audit Read API
	// and Dashboard L2 are the consumers; if tamper-evidence on caller
	// attribution ever matters we'll bump the digest payload to v2 and
	// gate verification by row version.
	AgentKeyName string

	// AccessDecisions is the per-chunk authorization outcome captured in
	// the trace. PR #21 addition. Stored two ways inside the same
	// advisory-locked transaction so the two views cannot disagree:
	//   - audit_log.access_decisions  (JSONB, denormalised single-row reads
	//     for Replay)
	//   - audit_log_decisions          (normalised one-row-per-chunk, for
	//     the Leak Report's per-document fan-in)
	//
	// Like the AgentKey* fields, deliberately NOT in the digest payload.
	// This means a tampering attacker with table-write privileges who
	// can defeat audit_log_decisions's no_update_audit_decisions rule
	// could rewrite a chunk's allowed flag without invalidating the
	// digest chain. Accepted tradeoff in PR #21 — the rules make the
	// attack surface match what audit_log already has.
	AccessDecisions []runtime.AccessDecision
}

func ComputeDigest(entry AuditEntry) string {
	entry.ImmutableDigest = ""
	payload := strings.Join([]string{
		entry.TraceID,
		entry.TenantID,
		entry.UserID,
		entry.QueryHash,
		entry.TimestampUTC.UTC().Format(time.RFC3339Nano),
		entry.Region,
		fmt.Sprintf("%d", entry.CandidatesRetrieved),
		fmt.Sprintf("%d", entry.CandidatesAllowed),
		fmt.Sprintf("%d", entry.CandidatesBlocked),
		fmt.Sprintf("%t", entry.FailClosed),
		entry.FailStage,
		entry.ErrorCode,
		entry.ErrorMessage,
		fmt.Sprintf("%d", entry.OpenFGALatencyMs),
		fmt.Sprintf("%d", entry.QdrantLatencyMs),
		fmt.Sprintf("%d", entry.TotalLatencyMs),
		entry.CircuitBreakerState,
		entry.DecisionMode,
		entry.ACLDecision,
		entry.Reason,
		entry.IdentityResolution,
		entry.PrincipalID,
		entry.PreviousHash,
	}, "\x1f")
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

type PostgresAuditWriter struct {
	db      *sql.DB
	timeout time.Duration
}

func (p *PostgresAuditWriter) Write(ctx context.Context, entry AuditEntry) error {
	if p == nil || p.db == nil {
		return fmt.Errorf("postgres audit writer unavailable")
	}
	timeout := p.timeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	writeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if entry.TimestampUTC.IsZero() {
		entry.TimestampUTC = time.Now()
	}
	// Normalize to microseconds (PostgreSQL TIMESTAMPTZ precision) so the digest
	// computed here at write time matches the digest the verifier recomputes after
	// reading the row back. Without this, sub-microsecond nanoseconds would be lost
	// on round-trip and every row would look "tampered".
	entry.TimestampUTC = entry.TimestampUTC.UTC().Truncate(time.Microsecond)

	tx, err := p.db.BeginTx(writeCtx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Serialize appends per tenant so the previous_hash read and the insert are one
	// atomic step. Without this lock two concurrent writers could read the same latest
	// digest and fork the chain. The advisory lock auto-releases when the tx ends.
	//
	// PR #22 HA review — TODO future scale lever: this lock is the per-tenant
	// write ceiling. For a tenant pushing past it, stripe the lock key by a
	// time bucket (e.g. UTC minute):
	//   pg_advisory_xact_lock(hashtext($1 || ':' || floor(extract(epoch from now())/60)))
	// That preserves causal ordering WITHIN a bucket (concurrent writers in the
	// same minute still serialize) but lets cross-bucket writers proceed in
	// parallel. The chain then has minute-granularity strict order with
	// intra-minute parallelism — fine for ledger semantics, ~60x throughput
	// per tenant. One-line writer change, zero schema change.
	//
	// Not implemented today because:
	//   - the current per-tenant ceiling (~thousands of writes/sec) is far
	//     above any agent fleet's per-tenant volume;
	//   - LoadAuditChain's ORDER BY (timestamp_utc, id) tolerates intra-bucket
	//     reordering; chain verification still works.
	//
	// Do NOT replace with an async outbox — that breaks the synchronous
	// fail-closed contract (TestAuditWrite_FailsEngine).
	if _, err := tx.ExecContext(writeCtx, `SELECT pg_advisory_xact_lock(hashtext($1))`, entry.TenantID); err != nil {
		return err
	}

	var prevHash sql.NullString
	if err := tx.QueryRowContext(writeCtx,
		`SELECT immutable_digest FROM audit_log WHERE tenant_id = $1 ORDER BY timestamp_utc DESC, id DESC LIMIT 1`,
		entry.TenantID,
	).Scan(&prevHash); err != nil && err != sql.ErrNoRows {
		return err
	}
	if prevHash.Valid {
		entry.PreviousHash = prevHash.String
	}

	entry.ImmutableDigest = ComputeDigest(entry)

	// PR #21: access_decisions ships as a JSONB blob co-located on the
	// audit_log row. Marshal under the lock so the blob and the normalised
	// audit_log_decisions rows go in atomically. nil/empty slices marshal
	// to "null" / "[]"; we coerce empty -> NULL via the writer below.
	decisionsJSON, err := json.Marshal(entry.AccessDecisions)
	if err != nil {
		return fmt.Errorf("marshal access_decisions: %w", err)
	}
	if _, err := tx.ExecContext(writeCtx, `
		INSERT INTO audit_log (
			trace_id,
			tenant_id,
			user_id,
			query_hash,
			timestamp_utc,
			region,
			candidates_retrieved,
			candidates_allowed,
			candidates_blocked,
			fail_closed,
			fail_stage,
			error_code,
			error_message,
			openfga_latency_ms,
			qdrant_latency_ms,
			total_latency_ms,
			circuit_breaker_state,
			decision_mode,
			acl_decision,
			reason,
			identity_resolution,
			principal_id,
			immutable_digest,
			previous_hash,
			agent_key_id,
			agent_key_name,
			access_decisions
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27)
	`,
		entry.TraceID,
		entry.TenantID,
		entry.UserID,
		entry.QueryHash,
		entry.TimestampUTC,
		entry.Region,
		entry.CandidatesRetrieved,
		entry.CandidatesAllowed,
		entry.CandidatesBlocked,
		entry.FailClosed,
		nullString(entry.FailStage),
		nullString(entry.ErrorCode),
		nullString(entry.ErrorMessage),
		entry.OpenFGALatencyMs,
		entry.QdrantLatencyMs,
		entry.TotalLatencyMs,
		entry.CircuitBreakerState,
		entry.DecisionMode,
		entry.ACLDecision,
		entry.Reason,
		entry.IdentityResolution,
		entry.PrincipalID,
		entry.ImmutableDigest,
		nullString(entry.PreviousHash),
		nullInt64(entry.AgentKeyID),
		nullString(entry.AgentKeyName),
		nullJSONB(entry.AccessDecisions, decisionsJSON),
	); err != nil {
		return err
	}

	// Bulk-insert one row per AccessDecision. Single round trip even for
	// hundreds of candidates. Ordinal preserves the order Engine.Execute
	// evaluated the chunks — Replay relies on that. We send NULL for
	// document_id when the trace didn't carry one (e.g. fail-closed paths
	// that recorded a single synthetic deny decision with no chunk).
	if len(entry.AccessDecisions) > 0 {
		// 10 columns per row: trace_id, tenant_id, ordinal, chunk_id,
		// document_id, allowed, reason, required_scope, region, score.
		// tenant_id is denormalised here (PR #21 CI-3) so the Leak
		// Report's per-tenant queries don't need to JOIN audit_log
		// just to filter. The writer pins it equal to the parent
		// audit_log.tenant_id under the same advisory lock.
		const colsPerRow = 10
		var sb strings.Builder
		sb.WriteString(`
			INSERT INTO audit_log_decisions (
				trace_id, tenant_id, ordinal, chunk_id, document_id,
				allowed, reason, required_scope, region, score
			) VALUES `)
		args := make([]any, 0, len(entry.AccessDecisions)*colsPerRow)
		for i, d := range entry.AccessDecisions {
			if i > 0 {
				sb.WriteString(", ")
			}
			base := i * colsPerRow
			fmt.Fprintf(&sb,
				"($%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d)",
				base+1, base+2, base+3, base+4, base+5,
				base+6, base+7, base+8, base+9, base+10,
			)
			args = append(args,
				entry.TraceID,
				entry.TenantID,
				i,
				d.ChunkID,
				nullString(d.DocumentID),
				d.Allowed,
				nullString(d.Reason),
				nullString(d.RequiredScope),
				nullString(d.Region),
				// Score is reserved for a future ranker integration that
				// scores per-decision; for now the per-chunk decision
				// doesn't carry a score and we store NULL.
				sql.NullFloat64{},
			)
		}
		if _, err := tx.ExecContext(writeCtx, sb.String(), args...); err != nil {
			return fmt.Errorf("insert audit_log_decisions: %w", err)
		}
	}

	return tx.Commit()
}

// ChainProblem describes a single integrity violation found while verifying an
// audit chain.
type ChainProblem struct {
	Index   int
	TraceID string
	Kind    string // "digest_mismatch" or "broken_link"
	Detail  string
}

// VerifyChain recomputes the digest of every entry and validates the previous_hash
// linkage. Entries must be ordered oldest-first. A non-empty result means the ledger
// was tampered with: "digest_mismatch" means a row's fields were modified after write;
// "broken_link" means the chain was cut, reordered, or a row was deleted.
func VerifyChain(entries []AuditEntry) []ChainProblem {
	var problems []ChainProblem
	prevDigest := ""
	for i, entry := range entries {
		if recomputed := ComputeDigest(entry); recomputed != entry.ImmutableDigest {
			problems = append(problems, ChainProblem{
				Index:   i,
				TraceID: entry.TraceID,
				Kind:    "digest_mismatch",
				Detail:  "stored immutable_digest does not match recomputed digest; row fields were modified after write",
			})
		}
		if i > 0 && entry.PreviousHash != prevDigest {
			problems = append(problems, ChainProblem{
				Index:   i,
				TraceID: entry.TraceID,
				Kind:    "broken_link",
				Detail:  fmt.Sprintf("previous_hash %s does not match prior entry digest %s", shortHash(entry.PreviousHash), shortHash(prevDigest)),
			})
		}
		prevDigest = entry.ImmutableDigest
	}
	return problems
}

func shortHash(h string) string {
	if h == "" {
		return "(none)"
	}
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

// ListAuditTenants returns the distinct tenant IDs present in the audit log.
func ListAuditTenants(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT tenant_id FROM audit_log ORDER BY tenant_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tenants []string
	for rows.Next() {
		var tenant string
		if err := rows.Scan(&tenant); err != nil {
			return nil, err
		}
		tenants = append(tenants, tenant)
	}
	return tenants, rows.Err()
}

// LoadAuditChain loads a tenant's audit entries oldest-first for verification.
func LoadAuditChain(ctx context.Context, db *sql.DB, tenantID string) ([]AuditEntry, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT trace_id, tenant_id, user_id, query_hash, timestamp_utc, region,
		       candidates_retrieved, candidates_allowed, candidates_blocked,
		       fail_closed, fail_stage, error_code, error_message,
		       openfga_latency_ms, qdrant_latency_ms, total_latency_ms,
		       circuit_breaker_state, decision_mode, acl_decision, reason,
		       identity_resolution, principal_id,
		       immutable_digest, previous_hash
		FROM audit_log
		WHERE tenant_id = $1
		ORDER BY timestamp_utc ASC, id ASC
	`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []AuditEntry
	for rows.Next() {
		var e AuditEntry
		var failStage, errorCode, errorMessage, previousHash sql.NullString
		var openfga, qdrant sql.NullInt64
		if err := rows.Scan(
			&e.TraceID, &e.TenantID, &e.UserID, &e.QueryHash, &e.TimestampUTC, &e.Region,
			&e.CandidatesRetrieved, &e.CandidatesAllowed, &e.CandidatesBlocked,
			&e.FailClosed, &failStage, &errorCode, &errorMessage,
			&openfga, &qdrant, &e.TotalLatencyMs,
			&e.CircuitBreakerState, &e.DecisionMode, &e.ACLDecision, &e.Reason,
			&e.IdentityResolution, &e.PrincipalID,
			&e.ImmutableDigest, &previousHash,
		); err != nil {
			return nil, err
		}
		e.FailStage = failStage.String
		e.ErrorCode = errorCode.String
		e.ErrorMessage = errorMessage.String
		e.OpenFGALatencyMs = int(openfga.Int64)
		e.QdrantLatencyMs = int(qdrant.Int64)
		e.PreviousHash = previousHash.String
		e.TimestampUTC = e.TimestampUTC.UTC()
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func nullString(value string) sql.NullString {
	if value == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: value, Valid: true}
}

// nullInt64 returns SQL NULL when the input is zero (so audit rows
// without an API-key context store NULL rather than 0, keeping the
// idx_audit_log_agent_key partial index tight) and the value
// otherwise. api_keys.id is a BIGSERIAL starting at 1, so a real
// AgentKeyID is never zero.
func nullInt64(value int64) sql.NullInt64 {
	if value == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: value, Valid: true}
}

// nullJSONB returns NULL when the input slice is empty (so audit rows with
// no decisions store NULL rather than "null" or "[]" JSONB literals) and
// the supplied JSON bytes otherwise. Returned as `any` so the caller can
// pass it straight into ExecContext.
func nullJSONB(decisions []runtime.AccessDecision, marshalled []byte) any {
	if len(decisions) == 0 {
		return nil
	}
	return marshalled
}
