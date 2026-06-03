package aclsync

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"groundwork/query-runtime/internal/engine"
	"groundwork/query-runtime/internal/runtime"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// --- engine wiring helpers for the revocation-SLA test ---

type fakeRetrieval struct{ candidates []runtime.Candidate }

func (f fakeRetrieval) Retrieve(context.Context, runtime.QueryRequest, int) ([]runtime.Candidate, error) {
	return f.candidates, nil
}

type noopAudit struct{}

func (noopAudit) Write(context.Context, engine.AuditEntry) error { return nil }

func engineWithACL(acl *MemoryFGA) *engine.Engine {
	return &engine.Engine{
		Config: engine.TimeoutConfig{
			Total:        2 * time.Second,
			QdrantSearch: time.Second,
			OpenFGACheck: time.Second,
			AuditWrite:   200 * time.Millisecond,
		},
		Backend: fakeRetrieval{candidates: []runtime.Candidate{{
			Chunk: runtime.Chunk{
				TenantID: "tenant_demo", Region: "uk", DocumentID: "security-policy",
				ChunkID: "chk1", ChunkHash: "h", Text: "finance security policy", FreshnessScore: 1,
			},
			Score: 0.9, Rank: 1,
		}}},
		ACL:     acl,
		Auditor: noopAudit{},
	}
}

func TestPermissionSetToTuples(t *testing.T) {
	ps, _ := NewMockConnector().Snapshot(context.Background(), "tenant_demo")
	got := tupleSet(PermissionSetToTuples(ps))
	want := []Tuple{
		{"user:finance_user", "member", "group:finance"},
		{"group:finance#member", "member", "group:employees"},       // nested group
		{"group:executives#member", "member", "group:employees"},    // nested group
		{"group:finance#member", "viewer", "folder:finance-folder"}, // group folder viewer
		{"folder:finance-folder", "parent", "document:security-policy"},
		{"group:employees#member", "viewer", "folder:public-folder"},
	}
	for _, w := range want {
		if !got[w] {
			t.Fatalf("expected tuple: %s", w)
		}
	}
}

func TestAccessMatrixResolvesGroupsAndInheritance(t *testing.T) {
	ctx := context.Background()
	fga := NewMemoryFGA()
	if _, err := NewSyncer(NewMockConnector(), fga, discardLogger()).SyncToOpenFGA(ctx, "tenant_demo"); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		user, doc string
		want      bool
	}{
		{"finance_user", "security-policy", true},    // finance -> finance-folder -> doc
		{"general_user", "security-policy", false},   // not in finance
		{"executive_user", "security-policy", false}, // executives != finance
		{"general_user", "handbook", true},           // employee (direct)
		{"finance_user", "handbook", true},           // nested: finance -> employees
		{"executive_user", "handbook", true},         // nested: executives -> employees
		{"executive_user", "board-minutes", true},
		{"finance_user", "board-minutes", false},
		{"general_user", "board-minutes", false},
	}
	for _, c := range cases {
		got := fga.Check("tenant_demo", "user:"+c.user, "viewer", "document:"+c.doc)
		if got != c.want {
			t.Fatalf("Check(%s viewer document:%s)=%v want %v", c.user, c.doc, got, c.want)
		}
	}
}

func TestSyncIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s := NewSyncer(NewMockConnector(), NewMemoryFGA(), discardLogger())
	r1, err := s.SyncToOpenFGA(ctx, "tenant_demo")
	if err != nil || r1.TuplesWritten == 0 {
		t.Fatalf("first sync should write tuples: %+v err=%v", r1, err)
	}
	r2, err := s.SyncToOpenFGA(ctx, "tenant_demo")
	if err != nil || r2.TuplesWritten != 0 || r2.TuplesDeleted != 0 {
		t.Fatalf("second sync should be a no-op, got %+v err=%v", r2, err)
	}
}

func TestDriftDetection(t *testing.T) {
	ctx := context.Background()

	// Empty sink: every source grant + every document is "missing in FGA".
	empty := NewSyncer(NewMockConnector(), NewMemoryFGA(), discardLogger())
	d0, _ := empty.DetectDrift(ctx, "tenant_demo")
	if len(d0.SourceMissingInFGA) == 0 || len(d0.DocumentsMissingInFGA) == 0 {
		t.Fatalf("empty FGA should report source-missing + docs-missing, got %+v", d0)
	}

	// After a full sync: no drift.
	mock := NewMockConnector()
	fga := NewMemoryFGA()
	s := NewSyncer(mock, fga, discardLogger())
	if _, err := s.SyncToOpenFGA(ctx, "tenant_demo"); err != nil {
		t.Fatal(err)
	}
	if d, _ := s.DetectDrift(ctx, "tenant_demo"); d.HasDrift() {
		t.Fatalf("expected no drift right after sync, got %+v", d)
	}

	// Revoke at the source WITHOUT syncing -> FGA has an extra tuple.
	mock.RevokeGroupMember("finance", "finance_user")
	d1, _ := s.DetectDrift(ctx, "tenant_demo")
	if len(d1.FGAExtraNotInSource) == 0 {
		t.Fatalf("expected FGA-extra drift after an unsynced revoke, got %+v", d1)
	}

	// Inject an orphaned document tuple directly into FGA.
	_ = fga.WriteTuples(ctx, "tenant_demo", []Tuple{{"folder:ghost-folder", "parent", "document:ghost-doc"}})
	d2, _ := s.DetectDrift(ctx, "tenant_demo")
	foundOrphan := false
	for _, doc := range d2.OrphanedFGADocuments {
		if doc == "document:ghost-doc" {
			foundOrphan = true
		}
	}
	if !foundOrphan {
		t.Fatalf("expected orphaned-document drift, got %+v", d2.OrphanedFGADocuments)
	}
}

// TestRevocationSLA proves the end-to-end guarantee: a source revocation, once synced,
// is enforced at QUERY TIME through the unchanged engine.Execute + OpenFGA-style check.
func TestRevocationSLA(t *testing.T) {
	ctx := context.Background()
	mock := NewMockConnector()
	fga := NewMemoryFGA()
	syncer := NewSyncer(mock, fga, discardLogger())
	if _, err := syncer.SyncToOpenFGA(ctx, "tenant_demo"); err != nil {
		t.Fatal(err)
	}

	eng := engineWithACL(fga)
	req := runtime.QueryRequest{TenantID: "tenant_demo", Region: "uk", UserID: "finance_user", Question: "policy"}

	// BEFORE revocation: finance_user can retrieve the finance document.
	before := eng.Execute(ctx, req)
	if len(before.Citations) != 1 {
		t.Fatalf("BEFORE: finance_user should access security-policy, got %d citations (trace=%+v)", len(before.Citations), before.Trace)
	}

	// Revoke finance_user from the finance group at the source, then sync.
	mock.RevokeGroupMember("finance", "finance_user")
	res, err := syncer.SyncToOpenFGA(ctx, "tenant_demo")
	if err != nil {
		t.Fatal(err)
	}
	if res.TuplesDeleted == 0 {
		t.Fatalf("revocation should delete at least one tuple, got %+v", res)
	}

	// AFTER revocation+sync: query-time enforcement denies finance_user.
	after := eng.Execute(ctx, req)
	if len(after.Citations) != 0 {
		t.Fatalf("AFTER: finance_user must be denied at query time, got %d citations", len(after.Citations))
	}
}

func TestWatchPermissionChangesEmitsRevocation(t *testing.T) {
	mock := NewMockConnector()
	ch, _ := mock.WatchPermissionChanges(context.Background(), "tenant_demo")
	mock.RevokeGroupMember("finance", "finance_user")
	select {
	case c := <-ch:
		if c.Type != ChangeRevokeGroupMember || c.Subject != "user:finance_user" || c.Object != "group:finance" {
			t.Fatalf("unexpected change: %+v", c)
		}
	case <-time.After(time.Second):
		t.Fatal("expected a revocation change event")
	}
}

func TestConnectorReads(t *testing.T) {
	ctx := context.Background()
	mock := NewMockConnector()
	docs, _ := mock.ListDocuments(ctx, "tenant_demo")
	if len(docs) != 3 {
		t.Fatalf("expected 3 documents, got %d", len(docs))
	}
	p, _ := mock.GetDocumentPermissions(ctx, "tenant_demo", "security-policy")
	if p.FolderID != "finance-folder" {
		t.Fatalf("expected folder finance-folder, got %q", p.FolderID)
	}
	hasFinance := false
	for _, g := range p.ViewerGroups {
		if g == "finance" {
			hasFinance = true
		}
	}
	if !hasFinance {
		t.Fatalf("expected inherited finance viewer group, got %+v", p.ViewerGroups)
	}
}
