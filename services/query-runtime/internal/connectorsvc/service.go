// Package connectorsvc implements runtime.GitHubService: the connector-backed
// Sync (write tuples) and LeakReport (exposure scan) operations the V1
// console drives. It lives outside the runtime package because it imports
// github/leakreport/aclsync, and aclsync imports runtime — so putting this
// in runtime would create an import cycle. cmd/query-runtime constructs it
// and wires it via runtime.Server.SetGitHubService.
package connectorsvc

import (
	"context"
	"fmt"
	"strings"

	"groundwork/query-runtime/internal/aclsync"
	"groundwork/query-runtime/internal/aclsync/github"
	"groundwork/query-runtime/internal/leakreport"
	"groundwork/query-runtime/internal/runtime"
)

// acmeOwners maps each demo document to its rightful owning group, so the
// leak report can distinguish a legitimate grant from cross-department
// exposure. For a real org this would be derived from CODEOWNERS / repo
// metadata; for the Acme demo it is fixed.
var acmeOwners = map[string]string{
	"gh:finance-budget":       "finance-team",
	"gh:payroll-system":       "engineering-team",
	"gh:engineering-platform": "engineering-team",
	"gh:security-audit":       "security-team",
	"gh:executive-strategy":   "executive-team",
}

// Service runs the GitHub connector and writes its tuples to OpenFGA. The
// client is github.NewMockClient() for the offline Acme demo, or
// github.NewHTTPClient(token) against a real org.
type Service struct {
	client     github.GitHubClient
	org        string
	openfgaURL string
	storeName  string
}

// New builds a Service. openfgaURL may be empty (Sync will then return a
// clear error, since it has nowhere to write tuples); LeakReport works
// regardless because it only reads the connector snapshot.
func New(client github.GitHubClient, org, openfgaURL, storeName string) *Service {
	return &Service{client: client, org: org, openfgaURL: openfgaURL, storeName: storeName}
}

func (s *Service) snapshot(ctx context.Context, tenantID string) (aclsync.PermissionSet, error) {
	return github.NewConnector(s.client, s.org, nil).Snapshot(ctx, tenantID)
}

// Sync re-reads the org and writes the resulting tuples into the OpenFGA
// store the runtime enforces against. Idempotent (OpenFGA writes are
// upserts of identical tuples). Returns a summary of the synced graph.
func (s *Service) Sync(ctx context.Context, tenantID string) (runtime.SyncResult, error) {
	ps, err := s.snapshot(ctx, tenantID)
	if err != nil {
		return runtime.SyncResult{}, fmt.Errorf("connector snapshot: %w", err)
	}
	tuples := aclsync.PermissionSetToTuples(ps)
	if s.openfgaURL == "" {
		return runtime.SyncResult{}, fmt.Errorf("OPENFGA_URL not configured; cannot write tuples")
	}
	if err := aclsync.NewOpenFGASink(s.openfgaURL, s.storeName).WriteTuples(ctx, tenantID, tuples); err != nil {
		return runtime.SyncResult{}, fmt.Errorf("write tuples: %w", err)
	}

	res := runtime.SyncResult{Org: s.org, Tuples: len(tuples)}
	for _, g := range ps.Groups {
		res.Teams = append(res.Teams, g.ID)
	}
	for _, d := range ps.Documents {
		res.Documents = append(res.Documents, d.ID)
	}
	return res, nil
}

// LeakReport runs the exposure analysis over the connector snapshot.
func (s *Service) LeakReport(ctx context.Context, tenantID string) (runtime.LeakResult, error) {
	ps, err := s.snapshot(ctx, tenantID)
	if err != nil {
		return runtime.LeakResult{}, fmt.Errorf("connector snapshot: %w", err)
	}
	rep := leakreport.Analyze(ps, acmeOwners)
	out := runtime.LeakResult{Findings: make([]runtime.LeakFinding, 0, len(rep.Findings))}
	for _, f := range rep.Findings {
		out.Findings = append(out.Findings, runtime.LeakFinding{
			Kind:     string(f.Kind),
			Severity: string(f.Severity),
			Title:    humanize(string(f.Kind)),
			Detail:   f.Detail,
		})
	}
	return out, nil
}

// humanize turns "cross_department_access" into "Cross department access"
// for the console's finding title.
func humanize(kind string) string {
	words := strings.Split(kind, "_")
	if len(words) == 0 {
		return kind
	}
	words[0] = strings.ToUpper(words[0][:1]) + words[0][1:]
	return strings.Join(words, " ")
}

var _ runtime.GitHubService = (*Service)(nil)
