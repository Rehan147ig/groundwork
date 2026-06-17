package runtime

import (
	"context"
	"net/http"
	"time"
)

// PR: GitHub connector + leak-report HTTP surface for the V1 console.
//
// As with AuditReader, the runtime package owns the interface + JSON DTOs
// while the implementation lives in a leaf package (internal/connectorsvc)
// that may import github/leakreport/aclsync — runtime cannot import those
// (aclsync imports runtime, so it would cycle). cmd/query-runtime wires the
// implementation via SetGitHubService.

// GitHubService is the connector-backed surface the console drives:
//   - Sync re-reads the org and writes the resulting OpenFGA tuples
//     (the "Connect" action).
//   - LeakReport runs the exposure analysis over the same connector output.
//
// Implementations are tenant-scoped by the caller (tenant comes from the
// API-key context, never the body).
type GitHubService interface {
	Sync(ctx context.Context, tenantID string) (SyncResult, error)
	LeakReport(ctx context.Context, tenantID string) (LeakResult, error)
}

// SyncResult is the synced permission graph summary.
type SyncResult struct {
	Org       string   `json:"org"`
	Teams     []string `json:"teams"`
	Documents []string `json:"documents"`
	Tuples    int      `json:"tuples"`
}

// LeakResult is the exposure-scan output.
type LeakResult struct {
	Findings []LeakFinding `json:"findings"`
}

// LeakFinding mirrors the console's expected shape.
type LeakFinding struct {
	Kind     string `json:"kind"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Detail   string `json:"detail"`
}

// SetGitHubService wires the connector-backed service. Nil-safe: when unset,
// the endpoints return 503 connector_unavailable.
func (s *Server) SetGitHubService(svc GitHubService) { s.githubSvc = svc }

// connectGitHub handles POST /v1/connect/github — re-syncs the org and
// writes tuples. Requires the "admin" scope (it mutates the OpenFGA store).
func (s *Server) connectGitHub(w http.ResponseWriter, r *http.Request) {
	if s.githubSvc == nil {
		writeAuditError(w, http.StatusServiceUnavailable, "connector_unavailable")
		return
	}
	tenant, ok := tenantFromContext(r.Context())
	if !ok {
		writeAuditError(w, http.StatusUnauthorized, "missing_tenant_context")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	res, err := s.githubSvc.Sync(ctx, tenant.TenantID)
	if err != nil {
		writeAuditError(w, http.StatusBadGateway, "github_sync_failed")
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// leakReport handles GET /v1/leak-report. Read-only; requires the "audit"
// scope (admin inherits).
func (s *Server) leakReport(w http.ResponseWriter, r *http.Request) {
	if s.githubSvc == nil {
		writeAuditError(w, http.StatusServiceUnavailable, "connector_unavailable")
		return
	}
	tenant, ok := tenantFromContext(r.Context())
	if !ok {
		writeAuditError(w, http.StatusUnauthorized, "missing_tenant_context")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	res, err := s.githubSvc.LeakReport(ctx, tenant.TenantID)
	if err != nil {
		writeAuditError(w, http.StatusBadGateway, "leak_report_failed")
		return
	}
	if res.Findings == nil {
		res.Findings = []LeakFinding{}
	}
	writeJSON(w, http.StatusOK, res)
}
