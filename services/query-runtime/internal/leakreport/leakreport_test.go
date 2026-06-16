package leakreport

import (
	"context"
	"testing"

	"groundwork/query-runtime/internal/aclsync"
	"groundwork/query-runtime/internal/aclsync/github"
)

// acmeOwners is the rightful owner of each demo repo. Used to distinguish a
// legitimate grant (owner views own repo) from a cross-department leak.
var acmeOwners = map[string]string{
	"gh:finance-budget":       "finance-team",
	"gh:payroll-system":       "engineering-team",
	"gh:engineering-platform": "engineering-team",
	"gh:security-audit":       "security-team",
	"gh:executive-strategy":   "executive-team",
}

func doc(id string, viewerGroups, viewerUsers []string) aclsync.Document {
	return aclsync.Document{ID: id, ViewerGroups: viewerGroups, ViewerUsers: viewerUsers}
}

func mkPS(tenant string, docs ...aclsync.Document) aclsync.PermissionSet {
	return aclsync.PermissionSet{TenantID: tenant, Documents: docs}
}

func TestAnalyze_CatchesEngineeringFinanceLeak(t *testing.T) {
	conn := github.NewConnector(github.NewMockClient(), "acme-financial", nil)
	ps, err := conn.Snapshot(context.Background(), "acme-financial")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	rep := Analyze(ps, acmeOwners)

	// The planted leak: engineering-team can view gh:finance-budget (owned
	// by finance-team) — must be reported as cross_department_access.
	var found bool
	for _, f := range rep.Findings {
		if f.Kind == KindCrossDepartment && f.DocumentID == "gh:finance-budget" && f.Group == "engineering-team" {
			found = true
			if f.Owner != "finance-team" || f.Severity != SeverityHigh {
				t.Fatalf("leak finding wrong shape: %+v", f)
			}
		}
	}
	if !found {
		t.Fatalf("expected cross-department finding for engineering-team -> gh:finance-budget; got %+v", rep.Findings)
	}
}

func TestAnalyze_LegitimateGrantsNotFlagged(t *testing.T) {
	conn := github.NewConnector(github.NewMockClient(), "acme-financial", nil)
	ps, _ := conn.Snapshot(context.Background(), "acme-financial")
	rep := Analyze(ps, acmeOwners)

	// finance-team viewing its own gh:finance-budget must NOT be a
	// cross-department finding.
	for _, f := range rep.Findings {
		if f.Kind == KindCrossDepartment && f.Group == "finance-team" && f.DocumentID == "gh:finance-budget" {
			t.Fatalf("owner grant wrongly flagged as cross-department: %+v", f)
		}
		// security-audit is single-owner; must not be overexposed.
		if f.Kind == KindOverexposed && f.DocumentID == "gh:security-audit" {
			t.Fatalf("single-owner repo wrongly flagged overexposed: %+v", f)
		}
	}
}

func TestAnalyze_OverexposedReportedForMultiGroupRepo(t *testing.T) {
	conn := github.NewConnector(github.NewMockClient(), "acme-financial", nil)
	ps, _ := conn.Snapshot(context.Background(), "acme-financial")
	rep := Analyze(ps, acmeOwners)

	// finance-budget is viewable by finance-team AND engineering-team -> overexposed.
	var found bool
	for _, f := range rep.Findings {
		if f.Kind == KindOverexposed && f.DocumentID == "gh:finance-budget" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected gh:finance-budget overexposed (2 groups); findings: %+v", rep.Findings)
	}
}

func TestAnalyze_WorldReadable(t *testing.T) {
	// A document granting user:* must be flagged world-readable. Built
	// directly since the GitHub mock has no public repo.
	ps := mkPS("acme", doc("public-wiki", nil, []string{"user:*"}))
	rep := Analyze(ps, nil)
	if len(rep.Findings) != 1 || rep.Findings[0].Kind != KindWorldReadable {
		t.Fatalf("expected one world_readable finding, got %+v", rep.Findings)
	}
}

func TestAnalyze_Orphaned(t *testing.T) {
	ps := mkPS("acme", doc("stranded", nil, nil))
	rep := Analyze(ps, nil)
	if len(rep.Findings) != 1 || rep.Findings[0].Kind != KindOrphaned {
		t.Fatalf("expected one orphaned finding, got %+v", rep.Findings)
	}
}

func TestCountBySeverity(t *testing.T) {
	conn := github.NewConnector(github.NewMockClient(), "acme-financial", nil)
	ps, _ := conn.Snapshot(context.Background(), "acme-financial")
	rep := Analyze(ps, acmeOwners)
	counts := rep.CountBySeverity()
	if counts[SeverityHigh] == 0 {
		t.Fatalf("expected at least one high-severity finding, got %v", counts)
	}
}
