package msgraph

import (
	"context"
	"testing"
)

// TestCatalogWriterUpsert: the in-memory writer upserts the same key without
// growing — second write replaces the first, count stays at 1. This
// structural property is what the Postgres writer mirrors via ON CONFLICT
// (tested live against a real DB by the smoke gate, not here).
func TestCatalogWriterUpsert(t *testing.T) {
	ctx := context.Background()
	w := NewInMemoryCatalogWriter()
	tenant := "tenant_x"

	// Principal upsert: insert, then re-insert with changed name. Count stays 1.
	first := Principal{EntraOID: "u1", DisplayName: "Alice", AccountEnabled: true}
	second := Principal{EntraOID: "u1", DisplayName: "Alice Renamed", AccountEnabled: true}
	if err := w.UpsertPrincipal(ctx, tenant, first); err != nil {
		t.Fatalf("UpsertPrincipal first: %v", err)
	}
	if err := w.UpsertPrincipal(ctx, tenant, second); err != nil {
		t.Fatalf("UpsertPrincipal second: %v", err)
	}
	if n, _ := w.PrincipalCount(ctx, tenant); n != 1 {
		t.Fatalf("PrincipalCount: want 1, got %d", n)
	}
	got, ok := w.Principal(tenant, "u1")
	if !ok || got.DisplayName != "Alice Renamed" {
		t.Fatalf("expected upsert to overwrite DisplayName, got %+v ok=%v", got, ok)
	}

	// Group upsert
	if err := w.UpsertGroup(ctx, tenant, Group{EntraGroupID: "g1", DisplayName: "Finance"}); err != nil {
		t.Fatalf("UpsertGroup: %v", err)
	}
	if err := w.UpsertGroup(ctx, tenant, Group{EntraGroupID: "g1", DisplayName: "Finance Renamed"}); err != nil {
		t.Fatalf("UpsertGroup second: %v", err)
	}
	if n, _ := w.GroupCount(ctx, tenant); n != 1 {
		t.Fatalf("GroupCount: want 1, got %d", n)
	}
	g, _ := w.Group(tenant, "g1")
	if g.DisplayName != "Finance Renamed" {
		t.Fatalf("expected upsert to overwrite group DisplayName, got %q", g.DisplayName)
	}

	// Membership upsert
	m := Membership{GroupID: "g1", MemberID: "u1", MemberType: "user"}
	if err := w.UpsertMembership(ctx, tenant, m); err != nil {
		t.Fatalf("UpsertMembership: %v", err)
	}
	if err := w.UpsertMembership(ctx, tenant, m); err != nil {
		t.Fatalf("UpsertMembership second: %v", err)
	}
	if n, _ := w.MembershipCount(ctx, tenant); n != 1 {
		t.Fatalf("MembershipCount: want 1, got %d", n)
	}
}

// TestCatalogWriterPerTenantIsolation: writes to one tenant must not show up
// in another tenant's counts. This mirrors the Postgres composite-PK design
// (tenant_id is always part of every primary key).
func TestCatalogWriterPerTenantIsolation(t *testing.T) {
	ctx := context.Background()
	w := NewInMemoryCatalogWriter()
	if err := w.UpsertPrincipal(ctx, "tenant_a", Principal{EntraOID: "u1"}); err != nil {
		t.Fatal(err)
	}
	if err := w.UpsertPrincipal(ctx, "tenant_b", Principal{EntraOID: "u1"}); err != nil {
		t.Fatal(err)
	}
	if n, _ := w.PrincipalCount(ctx, "tenant_a"); n != 1 {
		t.Fatalf("tenant_a count: want 1, got %d", n)
	}
	if n, _ := w.PrincipalCount(ctx, "tenant_b"); n != 1 {
		t.Fatalf("tenant_b count: want 1, got %d", n)
	}
}

// TestPendingCanonicalIDFormat: the canonical-id placeholder used by the
// Postgres writer follows the "entra:<oid>" alias-key form. PR #20+ replaces
// these via a SELECT ... WHERE gw_canonical_id LIKE 'entra:%' pass, so the
// format is a contract worth pinning down with a test.
func TestPendingCanonicalIDFormat(t *testing.T) {
	if got := pendingCanonicalID("aaaa-bbbb-cccc"); got != "entra:aaaa-bbbb-cccc" {
		t.Fatalf("pendingCanonicalID: want %q, got %q", "entra:aaaa-bbbb-cccc", got)
	}
}
