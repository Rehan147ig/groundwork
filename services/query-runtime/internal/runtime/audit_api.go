package runtime

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// PR #22 Audit Read API.
//
// Four read-only endpoints exposing the immutable audit ledger for the
// Dashboard, Replay engine, and Leak Report. All endpoints:
//   - require the new "audit" API-key scope (admin scope also grants
//     access via the existing hasScope override)
//   - are scoped to the calling tenant from the verified API-key
//     context; tenant_id is NEVER accepted from URL/body
//   - are READ-ONLY; no POST/PUT/PATCH/DELETE
//
// The endpoints, all under /v1/audit:
//   GET /v1/audit               list with cursor pagination + filters
//   GET /v1/audit/{trace_id}    single trace + per-chunk decisions
//   GET /v1/audit/stats         tenant-scoped aggregate counts in a window
//   GET /v1/audit/verify        recompute the hash chain (reuses
//                               engine.VerifyChain via the AuditReader impl)
//
// Package layering: the runtime package defines the AuditReader
// interface and the JSON DTOs here. The Postgres implementation lives
// in the engine package (where the audit_log schema is owned) and is
// wired into Server via SetAuditReader from cmd/query-runtime. This
// keeps runtime from importing engine and preserves the existing
// dependency direction (engine -> runtime).

// AuditReader is the read-side contract the four /v1/audit endpoints
// dispatch through. Implementations live OUTSIDE this package
// (typically in internal/engine) and are wired via SetAuditReader.
// Defining the interface here keeps runtime free of an engine import,
// which would otherwise form a cycle through engine/adapters.go.
type AuditReader interface {
	ListAuditEntries(ctx context.Context, tenantID string, filter AuditFilter, limit int, cursor string) (AuditPage, error)
	GetAuditEntry(ctx context.Context, tenantID, traceID string) (AuditEntryRead, error)
	ListAuditStats(ctx context.Context, tenantID string, from, to time.Time) (AuditStats, error)
	VerifyTenantChain(ctx context.Context, tenantID string) (AuditVerifyResult, error)
}

// ErrAuditEntryNotFound is returned by AuditReader.GetAuditEntry when
// the trace_id is not present in the tenant's audit log. Implementations
// must return exactly this sentinel so the handler maps it to 404.
var ErrAuditEntryNotFound = errors.New("audit_entry_not_found")

// AuditFilter is the parsed query string for GET /v1/audit. Empty or
// zero fields are treated as "no filter". TenantID is never in this
// struct — the calling tenant is sourced from the API-key context and
// passed separately, so a client cannot influence tenant scoping.
type AuditFilter struct {
	AgentKeyID   int64
	DecisionMode string
	FailClosed   *bool
	UserID       string
	From         time.Time
	To           time.Time
}

// AuditPage is one page of summaries plus the cursor for the next
// page. NextCursor is empty when no further rows exist.
type AuditPage struct {
	Entries    []AuditEntryRead
	NextCursor string
}

// AuditEntryRead is the read shape returned by both the list and the
// detail endpoints. The list endpoint zeros out the AccessDecisions
// slice; the detail endpoint populates it. Keeping a single shape means
// implementations don't have to maintain two parallel projections of
// the same DB row.
type AuditEntryRead struct {
	TraceID             string
	TimestampUTC        time.Time
	TenantID            string
	UserID              string
	Region              string
	AgentKeyID          int64
	AgentKeyName        string
	DecisionMode        string
	ACLDecision         string
	Reason              string
	FailClosed          bool
	FailStage           string
	ErrorCode           string
	ErrorMessage        string
	CandidatesRetrieved int
	CandidatesAllowed   int
	CandidatesBlocked   int
	TotalLatencyMs      int
	OpenFGALatencyMs    int
	QdrantLatencyMs     int
	CircuitBreakerState string
	IdentityResolution  string
	PrincipalID         string
	QueryHash           string
	ImmutableDigest     string
	PreviousHash        string
	AccessDecisions     []AccessDecision
}

// AuditStats is the aggregate-count payload returned by
// AuditReader.ListAuditStats. Counters are tenant-scoped and windowed
// by [From, To).
type AuditStats struct {
	WindowFrom      time.Time
	WindowTo        time.Time
	TotalQueries    int
	FailClosedCount int
	ByDecisionMode  map[string]int
	ByACLDecision   map[string]int
	TopAgents       []AgentStat
}

// AgentStat ranks api_keys by audit-entry count in a window.
type AgentStat struct {
	AgentKeyID   int64
	AgentKeyName string
	Count        int
}

// AuditVerifyResult is the read-API wrapper around VerifyChain. The
// Postgres implementation calls engine.LoadAuditChain +
// engine.VerifyChain and converts engine.ChainProblem to
// runtime.AuditChainProblem.
type AuditVerifyResult struct {
	Verified       bool
	EntriesChecked int
	Problems       []AuditChainProblem
}

// AuditChainProblem mirrors engine.ChainProblem in the runtime
// package's vocabulary. Same shape; intentionally a separate type so
// the engine ChainProblem can evolve without breaking the JSON contract.
type AuditChainProblem struct {
	Index   int
	TraceID string
	Kind    string
	Detail  string
}

// ---------------------------------------------------------------------
// JSON contract: HTTP response DTOs. Separate from the in-process
// types above so a future change to the in-process types doesn't
// silently break the wire format.
// ---------------------------------------------------------------------

// AuditAPIError is the consistent error envelope. Plain JSON; no stack
// traces or DB internals leak.
type AuditAPIError struct {
	Error string `json:"error"`
}

// AuditEntrySummary is the slim row shape the list endpoint returns.
type AuditEntrySummary struct {
	TraceID             string    `json:"trace_id"`
	TimestampUTC        time.Time `json:"timestamp_utc"`
	UserID              string    `json:"user_id"`
	Region              string    `json:"region"`
	AgentKeyID          int64     `json:"agent_key_id,omitempty"`
	AgentKeyName        string    `json:"agent_key_name,omitempty"`
	DecisionMode        string    `json:"decision_mode"`
	ACLDecision         string    `json:"acl_decision"`
	Reason              string    `json:"reason"`
	FailClosed          bool      `json:"fail_closed"`
	FailStage           string    `json:"fail_stage,omitempty"`
	ErrorCode           string    `json:"error_code,omitempty"`
	CandidatesRetrieved int       `json:"candidates_retrieved"`
	CandidatesAllowed   int       `json:"candidates_allowed"`
	CandidatesBlocked   int       `json:"candidates_blocked"`
	TotalLatencyMs      int       `json:"total_latency_ms"`
}

// AuditEntryDetail is the full shape the detail endpoint returns.
type AuditEntryDetail struct {
	AuditEntrySummary
	QueryHash           string                  `json:"query_hash"`
	ErrorMessage        string                  `json:"error_message,omitempty"`
	OpenFGALatencyMs    int                     `json:"openfga_latency_ms,omitempty"`
	QdrantLatencyMs     int                     `json:"qdrant_latency_ms,omitempty"`
	CircuitBreakerState string                  `json:"circuit_breaker_state,omitempty"`
	IdentityResolution  string                  `json:"identity_resolution,omitempty"`
	PrincipalID         string                  `json:"principal_id,omitempty"`
	ImmutableDigest     string                  `json:"immutable_digest"`
	PreviousHash        string                  `json:"previous_hash,omitempty"`
	AccessDecisions     []AccessDecisionPayload `json:"access_decisions"`
}

// AccessDecisionPayload is the per-chunk decision the detail endpoint
// returns. Separate type so the JSON contract is independent of
// runtime.AccessDecision's internal shape.
type AccessDecisionPayload struct {
	ChunkID       string `json:"chunk_id"`
	DocumentID    string `json:"document_id,omitempty"`
	Allowed       bool   `json:"allowed"`
	Reason        string `json:"reason,omitempty"`
	RequiredScope string `json:"required_scope,omitempty"`
	Region        string `json:"region,omitempty"`
}

// AuditListResponse is the response shape for GET /v1/audit.
type AuditListResponse struct {
	Entries    []AuditEntrySummary `json:"entries"`
	NextCursor string              `json:"next_cursor,omitempty"`
}

// AuditStatsResponse is the response shape for GET /v1/audit/stats.
type AuditStatsResponse struct {
	WindowFrom      time.Time          `json:"window_from"`
	WindowTo        time.Time          `json:"window_to"`
	TotalQueries    int                `json:"total_queries"`
	FailClosedCount int                `json:"fail_closed_count"`
	ByDecisionMode  map[string]int     `json:"by_decision_mode"`
	ByACLDecision   map[string]int     `json:"by_acl_decision"`
	TopAgents       []AgentStatPayload `json:"top_agents"`
}

// AgentStatPayload is the per-agent count shape on the stats response.
type AgentStatPayload struct {
	AgentKeyID   int64  `json:"agent_key_id"`
	AgentKeyName string `json:"agent_key_name,omitempty"`
	Count        int    `json:"count"`
}

// AuditVerifyResponse is the response shape for GET /v1/audit/verify.
type AuditVerifyResponse struct {
	Verified       bool                  `json:"verified"`
	EntriesChecked int                   `json:"entries_checked"`
	Problems       []ChainProblemPayload `json:"problems,omitempty"`
}

// ChainProblemPayload is the per-issue shape on the verify response.
type ChainProblemPayload struct {
	Index   int    `json:"index"`
	TraceID string `json:"trace_id"`
	Kind    string `json:"kind"`
	Detail  string `json:"detail"`
}

// SetAuditReader wires the read-side AuditReader the audit endpoints
// dispatch through. Nil-safe: when unset, /v1/audit* returns 503
// audit_unavailable.
func (s *Server) SetAuditReader(r AuditReader) { s.auditReader = r }

// auditList handles GET /v1/audit. Query parameters:
//
//	cursor          opaque pagination cursor from a previous response
//	limit           default 50, max 200
//	agent_key_id    int64 — filter by api_keys.id
//	decision_mode   string — exact match
//	fail_closed     true | false
//	user_id         string — exact match
//	from            RFC3339 timestamp, inclusive lower bound
//	to              RFC3339 timestamp, exclusive upper bound
func (s *Server) auditList(w http.ResponseWriter, r *http.Request) {
	if s.auditReader == nil {
		writeAuditError(w, http.StatusServiceUnavailable, "audit_unavailable")
		return
	}
	tenant, ok := tenantFromContext(r.Context())
	if !ok {
		writeAuditError(w, http.StatusUnauthorized, "missing_tenant_context")
		return
	}
	q := r.URL.Query()

	limit := 50
	if v := strings.TrimSpace(q.Get("limit")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			writeAuditError(w, http.StatusBadRequest, "invalid_limit")
			return
		}
		if n > 200 {
			n = 200
		}
		limit = n
	}

	filter := AuditFilter{}
	if v := strings.TrimSpace(q.Get("agent_key_id")); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			writeAuditError(w, http.StatusBadRequest, "invalid_agent_key_id")
			return
		}
		filter.AgentKeyID = n
	}
	if v := strings.TrimSpace(q.Get("decision_mode")); v != "" {
		filter.DecisionMode = v
	}
	if v := strings.TrimSpace(q.Get("fail_closed")); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			writeAuditError(w, http.StatusBadRequest, "invalid_fail_closed")
			return
		}
		filter.FailClosed = &b
	}
	if v := strings.TrimSpace(q.Get("user_id")); v != "" {
		filter.UserID = v
	}
	if v := strings.TrimSpace(q.Get("from")); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeAuditError(w, http.StatusBadRequest, "invalid_from_timestamp")
			return
		}
		filter.From = t
	}
	if v := strings.TrimSpace(q.Get("to")); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeAuditError(w, http.StatusBadRequest, "invalid_to_timestamp")
			return
		}
		filter.To = t
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	page, err := s.auditReader.ListAuditEntries(ctx, tenant.TenantID, filter, limit, strings.TrimSpace(q.Get("cursor")))
	if err != nil {
		if strings.HasPrefix(err.Error(), "invalid cursor") {
			writeAuditError(w, http.StatusBadRequest, "invalid_cursor")
			return
		}
		writeAuditError(w, http.StatusInternalServerError, "audit_query_failed")
		return
	}

	resp := AuditListResponse{
		Entries:    make([]AuditEntrySummary, 0, len(page.Entries)),
		NextCursor: page.NextCursor,
	}
	for _, e := range page.Entries {
		resp.Entries = append(resp.Entries, summaryFromRead(e))
	}
	writeJSON(w, http.StatusOK, resp)
}

// auditGet handles GET /v1/audit/{trace_id}.
func (s *Server) auditGet(w http.ResponseWriter, r *http.Request) {
	if s.auditReader == nil {
		writeAuditError(w, http.StatusServiceUnavailable, "audit_unavailable")
		return
	}
	tenant, ok := tenantFromContext(r.Context())
	if !ok {
		writeAuditError(w, http.StatusUnauthorized, "missing_tenant_context")
		return
	}
	traceID := strings.TrimSpace(r.PathValue("trace_id"))
	if traceID == "" {
		writeAuditError(w, http.StatusBadRequest, "invalid_trace_id")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	entry, err := s.auditReader.GetAuditEntry(ctx, tenant.TenantID, traceID)
	if err != nil {
		if errors.Is(err, ErrAuditEntryNotFound) {
			writeAuditError(w, http.StatusNotFound, "audit_entry_not_found")
			return
		}
		writeAuditError(w, http.StatusInternalServerError, "audit_query_failed")
		return
	}
	resp := AuditEntryDetail{
		AuditEntrySummary:   summaryFromRead(entry),
		QueryHash:           entry.QueryHash,
		ErrorMessage:        entry.ErrorMessage,
		OpenFGALatencyMs:    entry.OpenFGALatencyMs,
		QdrantLatencyMs:     entry.QdrantLatencyMs,
		CircuitBreakerState: entry.CircuitBreakerState,
		IdentityResolution:  entry.IdentityResolution,
		PrincipalID:         entry.PrincipalID,
		ImmutableDigest:     entry.ImmutableDigest,
		PreviousHash:        entry.PreviousHash,
		AccessDecisions:     make([]AccessDecisionPayload, 0, len(entry.AccessDecisions)),
	}
	for _, d := range entry.AccessDecisions {
		resp.AccessDecisions = append(resp.AccessDecisions, AccessDecisionPayload{
			ChunkID:       d.ChunkID,
			DocumentID:    d.DocumentID,
			Allowed:       d.Allowed,
			Reason:        d.Reason,
			RequiredScope: d.RequiredScope,
			Region:        d.Region,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// auditStats handles GET /v1/audit/stats. Defaults to the last 24h
// when from/to are unspecified.
func (s *Server) auditStats(w http.ResponseWriter, r *http.Request) {
	if s.auditReader == nil {
		writeAuditError(w, http.StatusServiceUnavailable, "audit_unavailable")
		return
	}
	tenant, ok := tenantFromContext(r.Context())
	if !ok {
		writeAuditError(w, http.StatusUnauthorized, "missing_tenant_context")
		return
	}
	q := r.URL.Query()
	var from, to time.Time
	if v := strings.TrimSpace(q.Get("from")); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeAuditError(w, http.StatusBadRequest, "invalid_from_timestamp")
			return
		}
		from = t
	} else {
		from = time.Now().UTC().Add(-24 * time.Hour)
	}
	if v := strings.TrimSpace(q.Get("to")); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeAuditError(w, http.StatusBadRequest, "invalid_to_timestamp")
			return
		}
		to = t
	}
	if !to.IsZero() && to.Before(from) {
		writeAuditError(w, http.StatusBadRequest, "invalid_window_to_before_from")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	stats, err := s.auditReader.ListAuditStats(ctx, tenant.TenantID, from, to)
	if err != nil {
		writeAuditError(w, http.StatusInternalServerError, "audit_stats_failed")
		return
	}
	resp := AuditStatsResponse{
		WindowFrom:      stats.WindowFrom,
		WindowTo:        stats.WindowTo,
		TotalQueries:    stats.TotalQueries,
		FailClosedCount: stats.FailClosedCount,
		ByDecisionMode:  stats.ByDecisionMode,
		ByACLDecision:   stats.ByACLDecision,
		TopAgents:       make([]AgentStatPayload, 0, len(stats.TopAgents)),
	}
	for _, a := range stats.TopAgents {
		resp.TopAgents = append(resp.TopAgents, AgentStatPayload{
			AgentKeyID:   a.AgentKeyID,
			AgentKeyName: a.AgentKeyName,
			Count:        a.Count,
		})
	}
	if resp.ByDecisionMode == nil {
		resp.ByDecisionMode = map[string]int{}
	}
	if resp.ByACLDecision == nil {
		resp.ByACLDecision = map[string]int{}
	}
	writeJSON(w, http.StatusOK, resp)
}

// auditVerify handles GET /v1/audit/verify. Reuses engine.VerifyChain
// + engine.LoadAuditChain via the AuditReader implementation; this
// endpoint introduces NO new verification logic.
//
// Intentionally NOT paginated: chain verification requires linear
// traversal in order, and VerifyChain's contract is "give me an
// ordered slice oldest-first". For tenants with very large logs a
// future PR can add a checkpoint mechanism (verify the last N entries
// and trust an earlier verified anchor); pilot scale is fine.
func (s *Server) auditVerify(w http.ResponseWriter, r *http.Request) {
	if s.auditReader == nil {
		writeAuditError(w, http.StatusServiceUnavailable, "audit_unavailable")
		return
	}
	tenant, ok := tenantFromContext(r.Context())
	if !ok {
		writeAuditError(w, http.StatusUnauthorized, "missing_tenant_context")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	result, err := s.auditReader.VerifyTenantChain(ctx, tenant.TenantID)
	if err != nil {
		writeAuditError(w, http.StatusInternalServerError, "audit_verify_failed")
		return
	}
	resp := AuditVerifyResponse{
		Verified:       result.Verified,
		EntriesChecked: result.EntriesChecked,
	}
	for _, p := range result.Problems {
		resp.Problems = append(resp.Problems, ChainProblemPayload{
			Index:   p.Index,
			TraceID: p.TraceID,
			Kind:    p.Kind,
			Detail:  p.Detail,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// summaryFromRead projects the runtime.AuditEntryRead (returned by the
// AuditReader) onto the JSON-stable summary shape.
func summaryFromRead(e AuditEntryRead) AuditEntrySummary {
	return AuditEntrySummary{
		TraceID:             e.TraceID,
		TimestampUTC:        e.TimestampUTC.UTC(),
		UserID:              e.UserID,
		Region:              e.Region,
		AgentKeyID:          e.AgentKeyID,
		AgentKeyName:        e.AgentKeyName,
		DecisionMode:        e.DecisionMode,
		ACLDecision:         e.ACLDecision,
		Reason:              e.Reason,
		FailClosed:          e.FailClosed,
		FailStage:           e.FailStage,
		ErrorCode:           e.ErrorCode,
		CandidatesRetrieved: e.CandidatesRetrieved,
		CandidatesAllowed:   e.CandidatesAllowed,
		CandidatesBlocked:   e.CandidatesBlocked,
		TotalLatencyMs:      e.TotalLatencyMs,
	}
}

func writeAuditError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, AuditAPIError{Error: code})
}

// auditScope is the API-key scope required for any of the audit
// endpoints. hasScope's existing "admin" override grants access too;
// separation lets operators issue audit-only keys for read-only
// dashboards.
const auditScope = "audit"
