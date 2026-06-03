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
		timeout = 30 * time.Millisecond
	}
	writeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if entry.TimestampUTC.IsZero() {
		entry.TimestampUTC = time.Now().UTC()
	}

	// Fetch the previous entry's hash to form the chain
	var prevHash sql.NullString
	_ = p.db.QueryRowContext(writeCtx,
		`SELECT immutable_digest FROM audit_log WHERE tenant_id = $1 ORDER BY timestamp_utc DESC LIMIT 1`,
		entry.TenantID,
	).Scan(&prevHash)
	if prevHash.Valid {
		entry.PreviousHash = prevHash.String
	}

	entry.ImmutableDigest = ComputeDigest(entry)
	_, err := p.db.ExecContext(writeCtx, `
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
			immutable_digest,
			previous_hash
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
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
		entry.ImmutableDigest,
		nullString(entry.PreviousHash),
	)
	return err
}

func nullString(value string) sql.NullString {
	if value == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: value, Valid: true}
}
