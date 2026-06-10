package msgraph

import (
	"context"
	"errors"
	"testing"
)

// fakeEnumClient is a minimal GraphClient that returns canned users, groups,
// and memberships. The drive methods are intentionally unimplemented and
// would panic if called — asserting that EnumerateDirectory never touches
// SharePoint, which is one of PR #19's explicit non-goals.
type fakeEnumClient struct {
	users   []GraphUser
	groups  []GraphGroup
	members map[string][]GraphMember
	authErr error
}

func (f *fakeEnumClient) ListUsers(_ context.Context) ([]GraphUser, error) {
	if f.authErr != nil {
		return nil, f.authErr
	}
	return f.users, nil
}

func (f *fakeEnumClient) ListGroups(_ context.Context) ([]GraphGroup, error) {
	if f.authErr != nil {
		return nil, f.authErr
	}
	return f.groups, nil
}

func (f *fakeEnumClient) ListGroupMembers(_ context.Context, groupID string) ([]GraphMember, error) {
	if f.authErr != nil {
		return nil, f.authErr
	}
	return f.members[groupID], nil
}

// Drive methods must NOT be called during directory enumeration.
func (f *fakeEnumClient) ListDriveItems(_ context.Context) ([]GraphDriveItem, error) {
	panic("ListDriveItems must not be called during EnumerateDirectory (PR #19: directory only)")
}

func (f *fakeEnumClient) ListItemPermissions(_ context.Context, _ string) ([]GraphPermission, error) {
	panic("ListItemPermissions must not be called during EnumerateDirectory (PR #19: directory only)")
}

func (f *fakeEnumClient) DeltaDriveItems(_ context.Context, _ string) ([]GraphDeltaItem, string, error) {
	panic("DeltaDriveItems must not be called during EnumerateDirectory (PR #19: directory only)")
}

// connectorWith builds a Connector wired to the supplied fake. Nil logger and
// delta store are fine because PR #19 doesn't traverse the watch/delta path.
func connectorWith(client GraphClient) *Connector {
	return NewConnector(client, Config{}, nil, nil)
}

// TestUserEnumeration: every directory user becomes one row in the catalog,
// keyed by EntraOID. UPN, email, and display name are preserved verbatim.
func TestUserEnumeration(t *testing.T) {
	client := &fakeEnumClient{
		users: []GraphUser{
			{ID: "u1", DisplayName: "Alice", Mail: "alice@example.com", UserPrincipalName: "alice@example.com"},
			{ID: "u2", DisplayName: "Bob", Mail: "", UserPrincipalName: "bob@example.com"},
			{ID: "u3", DisplayName: "Carol", Mail: "carol@example.com", UserPrincipalName: "carol@example.com"},
		},
	}
	w := NewInMemoryCatalogWriter()
	stats, err := connectorWith(client).EnumerateDirectory(context.Background(), "tenant_x", w)
	if err != nil {
		t.Fatalf("EnumerateDirectory: %v", err)
	}
	if stats.PrincipalsUpserted != 3 {
		t.Fatalf("PrincipalsUpserted: want 3, got %d", stats.PrincipalsUpserted)
	}
	if n, _ := w.PrincipalCount(context.Background(), "tenant_x"); n != 3 {
		t.Fatalf("PrincipalCount: want 3, got %d", n)
	}

	// Field fidelity check on one specific user.
	got, ok := w.Principal("tenant_x", "u2")
	if !ok {
		t.Fatal("u2 missing from catalog")
	}
	if got.UPN != "bob@example.com" || got.DisplayName != "Bob" {
		t.Fatalf("u2 field fidelity: %+v", got)
	}
	if got.Email != "" {
		t.Fatalf("u2 email: want empty (Graph returned no mail), got %q", got.Email)
	}
}

// TestGroupEnumeration: every directory group becomes one row.
func TestGroupEnumeration(t *testing.T) {
	client := &fakeEnumClient{
		groups: []GraphGroup{
			{ID: "g1", DisplayName: "Finance"},
			{ID: "g2", DisplayName: "Engineering"},
		},
	}
	w := NewInMemoryCatalogWriter()
	stats, err := connectorWith(client).EnumerateDirectory(context.Background(), "tenant_x", w)
	if err != nil {
		t.Fatalf("EnumerateDirectory: %v", err)
	}
	if stats.GroupsUpserted != 2 {
		t.Fatalf("GroupsUpserted: want 2, got %d", stats.GroupsUpserted)
	}
	if n, _ := w.GroupCount(context.Background(), "tenant_x"); n != 2 {
		t.Fatalf("GroupCount: want 2, got %d", n)
	}
}

// TestMembershipEnumeration: members are recorded with the right MemberType.
// Nested-group membership (a group whose member is another group) is
// captured as member_type='group'.
func TestMembershipEnumeration(t *testing.T) {
	client := &fakeEnumClient{
		groups: []GraphGroup{
			{ID: "g1", DisplayName: "Finance"},
			{ID: "g2", DisplayName: "All Staff"},
		},
		members: map[string][]GraphMember{
			"g1": {
				{ID: "u1", Type: MemberUser},
				{ID: "u2", Type: MemberUser},
			},
			"g2": {
				{ID: "g1", Type: MemberGroup}, // nested: Finance#member is a member of All Staff
				{ID: "u3", Type: MemberUser},
			},
		},
	}
	w := NewInMemoryCatalogWriter()
	stats, err := connectorWith(client).EnumerateDirectory(context.Background(), "tenant_x", w)
	if err != nil {
		t.Fatalf("EnumerateDirectory: %v", err)
	}
	if stats.MembershipsUpserted != 4 {
		t.Fatalf("MembershipsUpserted: want 4, got %d", stats.MembershipsUpserted)
	}
	if n, _ := w.MembershipCount(context.Background(), "tenant_x"); n != 4 {
		t.Fatalf("MembershipCount: want 4, got %d", n)
	}

	// Verify the nested-group membership was captured with the right type.
	var nestedFound bool
	for _, m := range w.Memberships("tenant_x") {
		if m.GroupID == "g2" && m.MemberID == "g1" {
			if m.MemberType != "group" {
				t.Fatalf("nested membership: MemberType want 'group', got %q", m.MemberType)
			}
			nestedFound = true
		}
	}
	if !nestedFound {
		t.Fatal("nested-group membership (g1 ∈ g2) not recorded")
	}
}

// TestIdempotentSync: calling EnumerateDirectory twice against the same
// directory does not grow the catalog. This is the property that makes the
// connector safe to schedule on a cron — re-running is a no-op when the
// source hasn't changed.
func TestIdempotentSync(t *testing.T) {
	client := &fakeEnumClient{
		users: []GraphUser{
			{ID: "u1", DisplayName: "Alice", UserPrincipalName: "alice@example.com"},
			{ID: "u2", DisplayName: "Bob", UserPrincipalName: "bob@example.com"},
		},
		groups: []GraphGroup{{ID: "g1", DisplayName: "Finance"}},
		members: map[string][]GraphMember{
			"g1": {{ID: "u1", Type: MemberUser}},
		},
	}
	w := NewInMemoryCatalogWriter()
	c := connectorWith(client)
	ctx := context.Background()

	if _, err := c.EnumerateDirectory(ctx, "tenant_x", w); err != nil {
		t.Fatalf("first run: %v", err)
	}
	pAfterFirst, _ := w.PrincipalCount(ctx, "tenant_x")
	gAfterFirst, _ := w.GroupCount(ctx, "tenant_x")
	mAfterFirst, _ := w.MembershipCount(ctx, "tenant_x")

	if _, err := c.EnumerateDirectory(ctx, "tenant_x", w); err != nil {
		t.Fatalf("second run: %v", err)
	}
	pAfterSecond, _ := w.PrincipalCount(ctx, "tenant_x")
	gAfterSecond, _ := w.GroupCount(ctx, "tenant_x")
	mAfterSecond, _ := w.MembershipCount(ctx, "tenant_x")

	if pAfterFirst != pAfterSecond || gAfterFirst != gAfterSecond || mAfterFirst != mAfterSecond {
		t.Fatalf("idempotency violated: first=(p=%d,g=%d,m=%d) second=(p=%d,g=%d,m=%d)",
			pAfterFirst, gAfterFirst, mAfterFirst, pAfterSecond, gAfterSecond, mAfterSecond)
	}
	if pAfterFirst != 2 || gAfterFirst != 1 || mAfterFirst != 1 {
		t.Fatalf("first-run counts unexpected: p=%d g=%d m=%d (want 2/1/1)",
			pAfterFirst, gAfterFirst, mAfterFirst)
	}
}

// TestEnumerationAuthFailure: a Graph auth failure surfaces as
// ErrAuthFailed from the first /users call, and propagates out without
// partial persistence. The catalog stays empty.
func TestEnumerationAuthFailure(t *testing.T) {
	client := &fakeEnumClient{authErr: ErrAuthFailed}
	w := NewInMemoryCatalogWriter()
	_, err := connectorWith(client).EnumerateDirectory(context.Background(), "tenant_x", w)
	if err == nil {
		t.Fatal("expected error on auth failure")
	}
	if !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("expected ErrAuthFailed, got: %v", err)
	}
	if n, _ := w.PrincipalCount(context.Background(), "tenant_x"); n != 0 {
		t.Fatalf("catalog must remain empty on auth failure; got %d principals", n)
	}
}
