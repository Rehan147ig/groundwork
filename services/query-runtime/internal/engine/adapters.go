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
