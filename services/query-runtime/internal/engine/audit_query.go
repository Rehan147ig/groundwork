package engine

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"groundwork/query-runtime/internal/runtime"
)

// PR #22 Audit Read API — Postgres implementation of runtime.AuditReader.
//
// The runtime package defines the AuditReader interface and the read
// shapes (runtime.AuditEntryRead / AuditFilter / AuditPage /
// AuditStats / AuditVerifyResult). This file converts the engine-
// owned write shapes (engine.AuditEntry / ChainProblem) to those
// runtime shapes at the package boundary.
//
// Why here instead of in runtime: the engine package owns the
// audit_log schema and the LoadAuditChain / VerifyChain primitives.
// Putting the DB queries here means runtime never needs to import
// engine (which would cycle through engine/adapters.go's runtime
// import). Wiring happens in cmd/query-runtime: NewPostgresAuditReader
// -> server.SetAuditReader.

// PostgresAuditReader is the runtime.AuditReader implementation backed
// by the immutable audit_log table.
type PostgresAuditReader struct {
	db *sql.DB
}

// NewPostgresAuditReader wraps a *sql.DB. The DB must point at the
// same Postgres that holds the audit_log + audit_log_decisions tables
// — typically the same handle PostgresAuditWriter uses on the write
// path.
func NewPostgresAuditReader(db *sql.DB) *PostgresAuditReader {
	return &PostgresAuditReader{db: db}
}

// Assert compile-time conformance to the runtime contract so any
// future change to runtime.AuditReader fails the build here, not at
// the wiring site in cmd/query-runtime.
var _ runtime.AuditReader = (*PostgresAuditReader)(nil)

// ListAuditEntries returns up to `limit` audit rows for the tenant
// matching `filter`, ordered (timestamp_utc DESC, id DESC). The cursor
// is the opaque base64 string emitted by a previous call; empty string
// for the first page. NextCursor on the returned page is empty when no
// further rows match the filter.
//
// AccessDecisions are NOT loaded here — the list view is intentionally
// the lightweight shape. Use GetAuditEntry for full detail.
func (p *PostgresAuditReader) ListAuditEntries(ctx context.Context, tenantID string, filter runtime.AuditFilter, limit int, cursor string) (runtime.AuditPage, error) {
	if p == nil || p.db == nil {
		return runtime.AuditPage{}, errors.New("audit db unavailable")
	}
	if tenantID == "" {
		return runtime.AuditPage{}, errors.New("tenant_id is required")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	var (
		args  []any
		where []string
	)
	args = append(args, tenantID)
	where = append(where, "tenant_id = $1")

	if cursor != "" {
		curTS, curID, err := decodeAuditCursor(cursor)
		if err != nil {
			return runtime.AuditPage{}, fmt.Errorf("invalid cursor: %w", err)
		}
		args = append(args, curTS, curID)
		where = append(where, fmt.Sprintf("(timestamp_utc, id) < ($%d, $%d)", len(args)-1, len(args)))
	}
	if filter.AgentKeyID > 0 {
		args = append(args, filter.AgentKeyID)
		where = append(where, fmt.Sprintf("agent_key_id = $%d", len(args)))
	}
	if filter.DecisionMode != "" {
		args = append(args, filter.DecisionMode)
		where = append(where, fmt.Sprintf("decision_mode = $%d", len(args)))
	}
	if filter.FailClosed != nil {
		args = append(args, *filter.FailClosed)
		where = append(where, fmt.Sprintf("fail_closed = $%d", len(args)))
	}
	if filter.UserID != "" {
		args = append(args, filter.UserID)
		where = append(where, fmt.Sprintf("user_id = $%d", len(args)))
	}
	if !filter.From.IsZero() {
		args = append(args, filter.From.UTC())
		where = append(where, fmt.Sprintf("timestamp_utc >= $%d", len(args)))
	}
	if !filter.To.IsZero() {
		args = append(args, filter.To.UTC())
		where = append(where, fmt.Sprintf("timestamp_utc < $%d", len(args)))
	}
	args = append(args, limit+1) // fetch one extra to detect "has next page"

	query := `
		SELECT trace_id, tenant_id, user_id, query_hash, timestamp_utc, region,
		       candidates_retrieved, candidates_allowed, candidates_blocked,
		       fail_closed, fail_stage, error_code, error_message,
		       openfga_latency_ms, qdrant_latency_ms, total_latency_ms,
		       circuit_breaker_state, decision_mode, acl_decision, reason,
		       identity_resolution, principal_id,
		       agent_key_id, agent_key_name,
		       immutable_digest, previous_hash,
		       id
		FROM audit_log
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY timestamp_utc DESC, id DESC
		LIMIT $` + fmt.Sprintf("%d", len(args))

	rows, err := p.db.QueryContext(ctx, query, args...)
	if err != nil {
		return runtime.AuditPage{}, fmt.Errorf("list audit_log: %w", err)
	}
	defer rows.Close()

	entries := make([]runtime.AuditEntryRead, 0, limit+1)
	ids := make([]string, 0, limit+1)
	for rows.Next() {
		read, id, err := scanReadRow(rows)
		if err != nil {
			return runtime.AuditPage{}, err
		}
		entries = append(entries, read)
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return runtime.AuditPage{}, fmt.Errorf("iterate audit rows: %w", err)
	}

	page := runtime.AuditPage{Entries: entries}
	if len(entries) > limit {
		page.Entries = entries[:limit]
		last := page.Entries[limit-1]
		page.NextCursor = encodeAuditCursor(last.TimestampUTC, ids[limit-1])
	}
	return page, nil
}

// GetAuditEntry returns one full audit row by trace_id, scoped to the
// tenant. AccessDecisions are populated preferentially from the
// normalised audit_log_decisions table (ordered by ordinal); for
// pre-PR21 rows (no normalised rows) the JSONB blob is parsed as a
// fallback.
func (p *PostgresAuditReader) GetAuditEntry(ctx context.Context, tenantID, traceID string) (runtime.AuditEntryRead, error) {
	if p == nil || p.db == nil {
		return runtime.AuditEntryRead{}, errors.New("audit db unavailable")
	}
	if tenantID == "" || traceID == "" {
		return runtime.AuditEntryRead{}, errors.New("tenant_id and trace_id are required")
	}
	row := p.db.QueryRowContext(ctx, `
		SELECT trace_id, tenant_id, user_id, query_hash, timestamp_utc, region,
		       candidates_retrieved, candidates_allowed, candidates_blocked,
		       fail_closed, fail_stage, error_code, error_message,
		       openfga_latency_ms, qdrant_latency_ms, total_latency_ms,
		       circuit_breaker_state, decision_mode, acl_decision, reason,
		       identity_resolution, principal_id,
		       agent_key_id, agent_key_name,
		       immutable_digest, previous_hash,
		       access_decisions
		FROM audit_log
		WHERE tenant_id = $1 AND trace_id = $2
		LIMIT 1
	`, tenantID, traceID)

	var (
		read                                                                       runtime.AuditEntryRead
		failStage, errorCode, errorMessage, previousHash, agentKeyName, regionCol  sql.NullString
		decisionMode, aclDecision, reason, identityResolution, principalID         sql.NullString
		circuitBreakerState                                                        sql.NullString
		openfga, qdrant, agentKeyID                                                sql.NullInt64
		decisionsBlob                                                              sql.NullString
		ts                                                                         time.Time
	)
	err := row.Scan(
		&read.TraceID, &read.TenantID, &read.UserID, &read.QueryHash, &ts, &regionCol,
		&read.CandidatesRetrieved, &read.CandidatesAllowed, &read.CandidatesBlocked,
		&read.FailClosed, &failStage, &errorCode, &errorMessage,
		&openfga, &qdrant, &read.TotalLatencyMs,
		&circuitBreakerState, &decisionMode, &aclDecision, &reason,
		&identityResolution, &principalID,
		&agentKeyID, &agentKeyName,
		&read.ImmutableDigest, &previousHash,
		&decisionsBlob,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return runtime.AuditEntryRead{}, runtime.ErrAuditEntryNotFound
	}
	if err != nil {
		return runtime.AuditEntryRead{}, fmt.Errorf("get audit_log row: %w", err)
	}
	read.TimestampUTC = ts.UTC()
	read.Region = regionCol.String
	read.FailStage = failStage.String
	read.ErrorCode = errorCode.String
	read.ErrorMessage = errorMessage.String
	read.OpenFGALatencyMs = int(openfga.Int64)
	read.QdrantLatencyMs = int(qdrant.Int64)
	read.CircuitBreakerState = circuitBreakerState.String
	read.DecisionMode = decisionMode.String
	read.ACLDecision = aclDecision.String
	read.Reason = reason.String
	read.IdentityResolution = identityResolution.String
	read.PrincipalID = principalID.String
	read.PreviousHash = previousHash.String
	read.AgentKeyID = agentKeyID.Int64
	read.AgentKeyName = agentKeyName.String

	decisions, err := p.loadDecisions(ctx, tenantID, traceID)
	if err != nil {
		return runtime.AuditEntryRead{}, err
	}
	if len(decisions) == 0 && decisionsBlob.Valid && decisionsBlob.String != "" {
		// Fall back to the JSONB blob for pre-PR21 rows (no normalised
		// rows) or fail-closed paths that recorded a single synthetic
		// deny decision without a chunk.
		var fromJSON []runtime.AccessDecision
		if err := json.Unmarshal([]byte(decisionsBlob.String), &fromJSON); err == nil {
			decisions = fromJSON
		}
	}
	read.AccessDecisions = decisions
	return read, nil
}

// loadDecisions pulls audit_log_decisions for one trace ordered by
// ordinal. tenant_id filter is defense-in-depth (the PR #21 writer
// pins it to the parent's tenant_id, but filtering here catches any
// future writer bug).
func (p *PostgresAuditReader) loadDecisions(ctx context.Context, tenantID, traceID string) ([]runtime.AccessDecision, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT chunk_id, document_id, allowed, reason, required_scope, region
		FROM audit_log_decisions
		WHERE tenant_id = $1 AND trace_id = $2
		ORDER BY ordinal ASC
	`, tenantID, traceID)
	if err != nil {
		return nil, fmt.Errorf("query audit_log_decisions: %w", err)
	}
	defer rows.Close()
	var out []runtime.AccessDecision
	for rows.Next() {
		var d runtime.AccessDecision
		var docID, reason, scope, region sql.NullString
		if err := rows.Scan(&d.ChunkID, &docID, &d.Allowed, &reason, &scope, &region); err != nil {
			return nil, fmt.Errorf("scan decision row: %w", err)
		}
		d.DocumentID = docID.String
		d.Reason = reason.String
		d.RequiredScope = scope.String
		d.Region = region.String
		out = append(out, d)
	}
	return out, rows.Err()
}

// ListAuditStats returns aggregate counts for the tenant in the window
// [from, to). from=zero means "from the beginning of time"; to=zero
// means "up to now". TopAgents is capped at 10 entries by count DESC.
func (p *PostgresAuditReader) ListAuditStats(ctx context.Context, tenantID string, from, to time.Time) (runtime.AuditStats, error) {
	if p == nil || p.db == nil {
		return runtime.AuditStats{}, errors.New("audit db unavailable")
	}
	if tenantID == "" {
		return runtime.AuditStats{}, errors.New("tenant_id is required")
	}
	if to.IsZero() {
		to = time.Now().UTC()
	}
	stats := runtime.AuditStats{
		WindowFrom:     from.UTC(),
		WindowTo:       to.UTC(),
		ByDecisionMode: map[string]int{},
		ByACLDecision:  map[string]int{},
	}

	args := []any{tenantID, to.UTC()}
	where := "tenant_id = $1 AND timestamp_utc < $2"
	if !from.IsZero() {
		args = append(args, from.UTC())
		where += fmt.Sprintf(" AND timestamp_utc >= $%d", len(args))
	}

	if err := p.db.QueryRowContext(ctx,
		`SELECT COUNT(*), COUNT(*) FILTER (WHERE fail_closed) FROM audit_log WHERE `+where,
		args...,
	).Scan(&stats.TotalQueries, &stats.FailClosedCount); err != nil {
		return runtime.AuditStats{}, fmt.Errorf("query totals: %w", err)
	}

	if err := p.aggregate(ctx, `decision_mode`, where, args, stats.ByDecisionMode); err != nil {
		return runtime.AuditStats{}, err
	}
	if err := p.aggregate(ctx, `acl_decision`, where, args, stats.ByACLDecision); err != nil {
		return runtime.AuditStats{}, err
	}

	rows, err := p.db.QueryContext(ctx,
		`SELECT agent_key_id, MAX(agent_key_name), COUNT(*) AS c
		 FROM audit_log
		 WHERE `+where+` AND agent_key_id IS NOT NULL
		 GROUP BY agent_key_id
		 ORDER BY c DESC, agent_key_id ASC
		 LIMIT 10`,
		args...,
	)
	if err != nil {
		return runtime.AuditStats{}, fmt.Errorf("query top_agents: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var a runtime.AgentStat
		var keyName sql.NullString
		if err := rows.Scan(&a.AgentKeyID, &keyName, &a.Count); err != nil {
			return runtime.AuditStats{}, fmt.Errorf("scan top_agents: %w", err)
		}
		a.AgentKeyName = keyName.String
		stats.TopAgents = append(stats.TopAgents, a)
	}
	return stats, rows.Err()
}

func (p *PostgresAuditReader) aggregate(ctx context.Context, column, where string, args []any, into map[string]int) error {
	q := fmt.Sprintf(`SELECT %s, COUNT(*) FROM audit_log WHERE %s GROUP BY %s`, column, where, column)
	rows, err := p.db.QueryContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("aggregate by %s: %w", column, err)
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var count int
		if err := rows.Scan(&key, &count); err != nil {
			return fmt.Errorf("scan aggregate %s: %w", column, err)
		}
		into[key] = count
	}
	return rows.Err()
}

// VerifyTenantChain is the read-API wrapper around LoadAuditChain +
// VerifyChain. Reuses both VERBATIM — this method introduces NO new
// verification logic.
func (p *PostgresAuditReader) VerifyTenantChain(ctx context.Context, tenantID string) (runtime.AuditVerifyResult, error) {
	if p == nil || p.db == nil {
		return runtime.AuditVerifyResult{}, errors.New("audit db unavailable")
	}
	if tenantID == "" {
		return runtime.AuditVerifyResult{}, errors.New("tenant_id is required")
	}
	entries, err := LoadAuditChain(ctx, p.db, tenantID)
	if err != nil {
		return runtime.AuditVerifyResult{}, fmt.Errorf("load chain: %w", err)
	}
	problems := VerifyChain(entries)
	result := runtime.AuditVerifyResult{
		Verified:       len(problems) == 0,
		EntriesChecked: len(entries),
	}
	for _, prob := range problems {
		result.Problems = append(result.Problems, runtime.AuditChainProblem{
			Index:   prob.Index,
			TraceID: prob.TraceID,
			Kind:    prob.Kind,
			Detail:  prob.Detail,
		})
	}
	return result, nil
}

// ---------------------------------------------------------------------
// internal helpers
// ---------------------------------------------------------------------

// scanReadRow scans one audit_log row into a runtime.AuditEntryRead.
// Returns the row's id column separately so the caller can encode it
// into the pagination cursor without re-querying.
func scanReadRow(rows *sql.Rows) (runtime.AuditEntryRead, string, error) {
	var (
		read                                                                              runtime.AuditEntryRead
		failStage, errorCode, errorMessage, previousHash, agentKeyName, regionCol         sql.NullString
		decisionMode, aclDecision, reason, identityResolution, principalID                sql.NullString
		circuitBreakerState                                                               sql.NullString
		openfga, qdrant, agentKeyID                                                       sql.NullInt64
		ts                                                                                time.Time
		id                                                                                string
	)
	if err := rows.Scan(
		&read.TraceID, &read.TenantID, &read.UserID, &read.QueryHash, &ts, &regionCol,
		&read.CandidatesRetrieved, &read.CandidatesAllowed, &read.CandidatesBlocked,
		&read.FailClosed, &failStage, &errorCode, &errorMessage,
		&openfga, &qdrant, &read.TotalLatencyMs,
		&circuitBreakerState, &decisionMode, &aclDecision, &reason,
		&identityResolution, &principalID,
		&agentKeyID, &agentKeyName,
		&read.ImmutableDigest, &previousHash,
		&id,
	); err != nil {
		return runtime.AuditEntryRead{}, "", fmt.Errorf("scan audit row: %w", err)
	}
	read.TimestampUTC = ts.UTC()
	read.Region = regionCol.String
	read.FailStage = failStage.String
	read.ErrorCode = errorCode.String
	read.ErrorMessage = errorMessage.String
	read.OpenFGALatencyMs = int(openfga.Int64)
	read.QdrantLatencyMs = int(qdrant.Int64)
	read.CircuitBreakerState = circuitBreakerState.String
	read.DecisionMode = decisionMode.String
	read.ACLDecision = aclDecision.String
	read.Reason = reason.String
	read.IdentityResolution = identityResolution.String
	read.PrincipalID = principalID.String
	read.PreviousHash = previousHash.String
	read.AgentKeyID = agentKeyID.Int64
	read.AgentKeyName = agentKeyName.String
	return read, id, nil
}

// ---------------------------------------------------------------------
// Cursor encoding. Opaque to clients; stable across processes.
// JSON payload so future fields (sort order, etc.) are backward
// compatible. RawURLEncoding so the cursor is safe in URL params.
// ---------------------------------------------------------------------

type auditCursor struct {
	Timestamp string `json:"ts"`
	ID        string `json:"id"`
}

func encodeAuditCursor(ts time.Time, id string) string {
	payload, _ := json.Marshal(auditCursor{
		Timestamp: ts.UTC().Format(time.RFC3339Nano),
		ID:        id,
	})
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeAuditCursor(s string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("base64: %w", err)
	}
	var c auditCursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return time.Time{}, "", fmt.Errorf("json: %w", err)
	}
	ts, err := time.Parse(time.RFC3339Nano, c.Timestamp)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("ts: %w", err)
	}
	if c.ID == "" {
		return time.Time{}, "", errors.New("empty id")
	}
	return ts, c.ID, nil
}
