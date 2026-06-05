package engine

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"groundwork/query-runtime/internal/runtime"
)

var ErrRetrievalClientUnavailable = errors.New("retrieval_client_unavailable")

type VectorRetrievalClient struct {
	Vector runtime.VectorSearcher
}

func (v VectorRetrievalClient) Retrieve(ctx context.Context, req runtime.QueryRequest, limit int) ([]runtime.Candidate, error) {
	if v.Vector == nil {
		return nil, ErrRetrievalClientUnavailable
	}
	return v.Vector.SearchVector(ctx, req, limit)
}

type RuntimeTraceAuditWriter struct {
	Trace runtime.TraceWriter
}

func (r RuntimeTraceAuditWriter) Write(ctx context.Context, entry AuditEntry) error {
	if r.Trace == nil {
		return errors.New("runtime trace writer unavailable")
	}
	entry.ImmutableDigest = ComputeDigest(entry)
	trace := runtime.RuntimeTrace{
		TraceID:            entry.TraceID,
		TenantID:           entry.TenantID,
		UserID:             entry.UserID,
		Region:             entry.Region,
		StartedAt:          entry.TimestampUTC,
		LatencyMs:          int64(entry.TotalLatencyMs),
		VectorCandidates:   entry.CandidatesRetrieved,
		BlockedByACL:       entry.CandidatesBlocked,
		RerankedCandidates: entry.CandidatesAllowed,
		DecisionMode:       "engine_audit_trace",
		FailureStage:       entry.FailStage,
		ErrorCode:          entry.ErrorCode,
		ErrorMessage:       entry.ErrorMessage,
		ImmutableDigest:    entry.ImmutableDigest,
	}
	return r.Trace.WriteTrace(ctx, trace)
}

func NewPostgresAuditWriter(db *sql.DB) *PostgresAuditWriter {
	return &PostgresAuditWriter{db: db, timeout: 30 * time.Millisecond}
}

// NewPostgresAuditWriterWithTimeout is NewPostgresAuditWriter with a caller-supplied
// per-write timeout. The default constructor uses a tight 30ms budget — fine for the
// in-memory store, but for a real Postgres round-trip (advisory lock + select + insert) it
// can be too small and, because Write derives its own context from this timeout, it silently
// caps the engine's larger AUDIT_TIMEOUT_MS. Integration tests and production deployments
// against managed Postgres should pass a realistic value (e.g. 2s). A non-positive timeout
// falls back to 2s.
func NewPostgresAuditWriterWithTimeout(db *sql.DB, timeout time.Duration) *PostgresAuditWriter {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	return &PostgresAuditWriter{db: db, timeout: timeout}
}
