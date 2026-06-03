package engine

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
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
	ImmutableDigest     string
	PreviousHash        string
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
			immutable_digest,
			previous_hash
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22)
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
		entry.ImmutableDigest,
		nullString(entry.PreviousHash),
	); err != nil {
		return err
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
