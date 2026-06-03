package msgraph

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"groundwork/query-runtime/internal/aclsync"
	"groundwork/query-runtime/internal/runtime"
)

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// fakeGraph is an in-memory GraphClient seeded with an Entra/SharePoint-style dataset.
type fakeGraph struct {
	users   []GraphUser
	groups  []GraphGroup
	members map[string][]GraphMember
	items   []GraphDriveItem
	perms   map[string][]GraphPermission
	delta   []GraphDeltaItem
	failOn  string // method name to fail with ErrAuthFailed
}

func (f *fakeGraph) ListUsers(context.Context) ([]GraphUser, error) {
	if f.failOn == "ListUsers" {
		return nil, ErrAuthFailed
	}
	return f.users, nil
}
func (f *fakeGraph) ListGroups(context.Context) ([]GraphGroup, error) {
	if f.failOn == "ListGroups" {
		return nil, ErrAuthFailed
	}
	return f.groups, nil
}
func (f *fakeGraph) ListGroupMembers(_ context.Context, groupID string) ([]GraphMember, error) {
	if f.failOn == "ListGroupMembers" {
		return nil, ErrAuthFailed
	}
	return f.members[groupID], nil
}
func (f *fakeGraph) ListDriveItems(context.Context) ([]GraphDriveItem, error) {
	if f.failOn == "ListDriveItems" {
		return nil, ErrAuthFailed
	}
	return f.items, nil
}
func (f *fakeGraph) ListItemPermissions(_ context.Context, itemID string) ([]GraphPermission, error) {
	if f.failOn == "ListItemPermissions" {
		return nil, ErrAuthFailed
	}
	return f.perms[itemID], nil
}
func (f *fakeGraph) DeltaDriveItems(context.Context, string) ([]GraphDeltaItem, string, error) {
	if f.failOn == "DeltaDriveItems" {
		return nil, "", ErrAuthFailed
	}
	return f.delta, "deltaLink-next", nil
}

func seededGraph() *fakeGraph {
	return &fakeGraph{
		users: []GraphUser{
			{ID: "u-fin", Mail: "finance_user"},
			{ID: "u-gen", Mail: "general_user"},
			{ID: "u-exec", Mail: "executive_user"},
		},
		groups: []GraphGroup{{ID: "finance"}, {ID: "executives"}, {ID: "employees"}},
		members: map[string][]GraphMember{
			"finance":    {{ID: "u-fin", Mail: "finance_user", Type: MemberUser}},
			"executives": {{ID: "u-exec", Mail: "executive_user", Type: MemberUser}},
			"employees": {
				{ID: "u-gen", Mail: "general_user", Type: MemberUser},
				{ID: "finance", Type: MemberGroup},    // nested
				{ID: "executives", Type: MemberGroup}, // nested
			},
		},
		items: []GraphDriveItem{
			{ID: "finance-folder", IsFolder: true},
			{ID: "public-folder", IsFolder: true},
			{ID: "executive-folder", IsFolder: true},
			{ID: "security-policy", ParentID: "finance-folder"},
			{ID: "handbook", ParentID: "public-folder"},
			{ID: "board-minutes", ParentID: "executive-folder"},
			{ID: "personal-note", ParentID: "executive-folder"}, // direct user grant; folder is executives-only
		},
		perms: map[string][]GraphPermission{
			"finance-folder":   {{Roles: []string{"read"}, Grantee: GraphGrantee{GroupID: "finance"}}},
			"public-folder":    {{Roles: []string{"read"}, Grantee: GraphGrantee{GroupID: "employees"}}},
			"executive-folder": {{Roles: []string{"read"}, Grantee: GraphGrantee{GroupID: "executives"}}},
			"personal-note":    {{Roles: []string{"read"}, Grantee: GraphGrantee{UserID: "u-gen"}}},
		},
		delta: []GraphDeltaItem{
			{ID: "handbook", ParentID: "public-folder"},
			{ID: "old-file", Deleted: true},
		},
	}
}

func TestMapUsersAndGroups(t *testing.T) {
	conn := NewConnector(seededGraph(), Config{}, discard(), nil)
	ps, err := conn.Snapshot(context.Background(), "tenant_demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(ps.Users) != 3 {
		t.Fatalf("expected 3 users, got %v", ps.Users)
	}
	var employees *aclsync.Group
	for i := range ps.Groups {
		if ps.Groups[i].ID == "employees" {
			employees = &ps.Groups[i]
		}
	}
	if employees == nil {
		t.Fatal("employees group missing")
	}
	if len(employees.MemberGroups) != 2 { // nested finance + executives
		t.Fatalf("expected employees to nest 2 sub-groups, got %v", employees.MemberGroups)
	}
	if len(employees.MemberUsers) != 1 || employees.MemberUsers[0] != "general_user" {
		t.Fatalf("expected employees direct member general_user, got %v", employees.MemberUsers)
	}
}

func TestSnapshotAccessMatrix(t *testing.T) {
	ctx := context.Background()
	conn := NewConnector(seededGraph(), Config{}, discard(), nil)
	ps, err := conn.Snapshot(ctx, "tenant_demo")
	if err != nil {
		t.Fatal(err)
	}
	fga := aclsync.NewMemoryFGA()
	if err := fga.WriteTuples(ctx, "tenant_demo", aclsync.PermissionSetToTuples(ps)); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		user, doc string
		want      bool
	}{
		{"finance_user", "security-policy", true},    // group finance -> finance-folder -> doc
		{"general_user", "security-policy", false},   // not in finance
		{"executive_user", "security-policy", false}, // executives != finance
		{"general_user", "handbook", true},           // employee (direct)
		{"finance_user", "handbook", true},           // nested finance -> employees
		{"executive_user", "handbook", true},         // nested executives -> employees
		{"executive_user", "board-minutes", true},
		{"finance_user", "board-minutes", false},
		{"general_user", "personal-note", true}, // direct user grant resolved via object id -> mail
		{"finance_user", "personal-note", false},
	}
	for _, c := range cases {
		got := fga.Check("tenant_demo", "user:"+c.user, "viewer", "document:"+c.doc)
		if got != c.want {
			t.Fatalf("Check(%s viewer document:%s)=%v want %v", c.user, c.doc, got, c.want)
		}
	}
}

// TestCanonicalSnapshotUsesCanonicalPrincipals proves that with canonical identity enabled,
// the Graph connector (a) pre-provisions a principal + aliases for every directory user, and
// (b) emits user:principal:<uuid> tuples for EVERY user reference — group membership AND
// direct document grants — never raw mail keys. It also proves the synced principal matches
// what the query-time resolver returns for a verified token (entra:id == Graph object id).
func TestCanonicalSnapshotUsesCanonicalPrincipals(t *testing.T) {
	ctx := context.Background()
	resolver := runtime.NewMemoryPrincipalResolver()
	conn := NewConnector(seededGraph(), Config{}, discard(), nil)
	conn.SetCanonicalIdentity(resolver, true)

	ps, err := conn.Snapshot(ctx, "tenant_demo")
	if err != nil {
		t.Fatal(err)
	}

	// No raw user key survives canonicalization: every user reference is "principal:<uuid>".
	assertCanonical := func(where string, users []string) {
		for _, u := range users {
			if !strings.HasPrefix(u, "principal:") {
				t.Fatalf("%s: user reference %q is not a canonical principal", where, u)
			}
		}
	}
	for _, g := range ps.Groups {
		assertCanonical("group "+g.ID+" members", g.MemberUsers)
	}
	for _, f := range ps.Folders {
		assertCanonical("folder "+f.ID+" viewers", f.ViewerUsers)
	}
	for _, d := range ps.Documents {
		assertCanonical("document "+d.ID+" viewers", d.ViewerUsers)
	}

	// The synced principal for finance_user (entra:id=u-fin) is exactly what the query-time
	// resolver returns for a verified token presenting oid=u-fin.
	resolved, err := resolver.Resolve(ctx, "tenant_demo",
		runtime.AssertionsFromIdentity(runtime.Identity{OID: "u-fin", Verified: true}))
	if err != nil {
		t.Fatalf("finance_user must resolve after sync pre-provisioned its alias: %v", err)
	}
	finSubject := "user:" + runtime.PrincipalUserID(resolved.ID)

	fga := aclsync.NewMemoryFGA()
	if err := fga.WriteTuples(ctx, "tenant_demo", aclsync.PermissionSetToTuples(ps)); err != nil {
		t.Fatal(err)
	}

	// (a) GROUP MEMBERSHIP tuple uses the canonical principal; the raw mail key does not.
	if !fga.Check("tenant_demo", finSubject, "member", "group:finance") {
		t.Fatalf("expected %s member group:finance", finSubject)
	}
	if fga.Check("tenant_demo", "user:finance_user", "member", "group:finance") {
		t.Fatal("raw user:finance_user must NOT remain a group member after canonicalization")
	}
	// End-to-end: finance_user (as principal) still reaches security-policy via finance-folder.
	if !fga.Check("tenant_demo", finSubject, "viewer", "document:security-policy") {
		t.Fatalf("expected %s viewer document:security-policy (group->folder->doc)", finSubject)
	}

	// (b) DIRECT DOCUMENT VIEWER tuple uses the canonical principal: general_user (u-gen) has a
	// direct grant on personal-note.
	gen, err := resolver.Resolve(ctx, "tenant_demo",
		runtime.AssertionsFromIdentity(runtime.Identity{OID: "u-gen", Verified: true}))
	if err != nil {
		t.Fatal(err)
	}
	genSubject := "user:" + runtime.PrincipalUserID(gen.ID)
	if !fga.Check("tenant_demo", genSubject, "viewer", "document:personal-note") {
		t.Fatalf("expected direct grant %s viewer document:personal-note", genSubject)
	}
	if fga.Check("tenant_demo", "user:general_user", "viewer", "document:personal-note") {
		t.Fatal("raw user:general_user direct grant must NOT remain after canonicalization")
	}
}

// TestNonCanonicalSnapshotUnchanged guards the demo/local path: with canonical OFF (the
// default), the connector still emits raw user-key tuples exactly as before.
func TestNonCanonicalSnapshotUnchanged(t *testing.T) {
	ctx := context.Background()
	conn := NewConnector(seededGraph(), Config{}, discard(), nil) // canonical not set -> off
	ps, err := conn.Snapshot(ctx, "tenant_demo")
	if err != nil {
		t.Fatal(err)
	}
	fga := aclsync.NewMemoryFGA()
	if err := fga.WriteTuples(ctx, "tenant_demo", aclsync.PermissionSetToTuples(ps)); err != nil {
		t.Fatal(err)
	}
	if !fga.Check("tenant_demo", "user:finance_user", "member", "group:finance") {
		t.Fatal("non-canonical mode must keep raw user:finance_user group membership")
	}
}

func TestRevokeChangeMapping(t *testing.T) {
	byID := map[string]string{"u-gen": "general_user"}

	// Group permission removed from a folder.
	folder := GraphDriveItem{ID: "finance-folder", IsFolder: true}
	ch, ok := revokeChange(folder, GraphPermission{Grantee: GraphGrantee{GroupID: "finance"}}, byID)
	if !ok || ch.Type != aclsync.ChangeRevokeFolderViewer ||
		ch.Subject != "group:finance#member" || ch.Object != "folder:finance-folder" {
		t.Fatalf("unexpected folder group revoke: %+v ok=%v", ch, ok)
	}

	// User permission removed from a document (object id resolved to mail).
	doc := GraphDriveItem{ID: "personal-note"}
	ch, ok = revokeChange(doc, GraphPermission{Grantee: GraphGrantee{UserID: "u-gen"}}, byID)
	if !ok || ch.Type != aclsync.ChangeRevokeDocumentViewer ||
		ch.Subject != "user:general_user" || ch.Object != "document:personal-note" {
		t.Fatalf("unexpected document user revoke: %+v ok=%v", ch, ok)
	}
}

func TestGraphAuthFailureDoesNotDeleteTuples(t *testing.T) {
	ctx := context.Background()
	fga := aclsync.NewMemoryFGA()
	// Pre-populate OpenFGA (as a prior good sync would have).
	seed := []aclsync.Tuple{
		{User: "user:finance_user", Relation: "member", Object: "group:finance"},
		{User: "group:finance#member", Relation: "viewer", Object: "folder:finance-folder"},
		{User: "folder:finance-folder", Relation: "parent", Object: "document:security-policy"},
	}
	if err := fga.WriteTuples(ctx, "tenant_demo", seed); err != nil {
		t.Fatal(err)
	}
	before, _ := fga.ListTuples(ctx, "tenant_demo")

	// Auth fails -> Snapshot errors -> Syncer must NOT delete anything.
	failing := NewConnector(&fakeGraph{failOn: "ListUsers"}, Config{}, discard(), nil)
	syncer := aclsync.NewSyncer(failing, fga, discard())
	if _, err := syncer.SyncToOpenFGA(ctx, "tenant_demo"); err == nil {
		t.Fatal("expected sync to fail on Graph auth error")
	}
	after, _ := fga.ListTuples(ctx, "tenant_demo")
	if len(after) != len(before) {
		t.Fatalf("auth failure must not delete tuples: before=%d after=%d", len(before), len(after))
	}
}

func TestDeltaClassifyAndTokenStores(t *testing.T) {
	deleted, changed := classifyDelta(seededGraph().delta)
	if len(deleted) != 1 || deleted[0] != "old-file" {
		t.Fatalf("expected 1 deleted (old-file), got %v", deleted)
	}
	if len(changed) != 1 || changed[0] != "handbook" {
		t.Fatalf("expected 1 changed (handbook), got %v", changed)
	}

	ctx := context.Background()
	// File-backed durable token store round-trips.
	fs := NewFileDeltaTokenStore(t.TempDir())
	if v, _ := fs.Load(ctx, "tenant_demo"); v != "" {
		t.Fatalf("expected empty token initially, got %q", v)
	}
	if err := fs.Save(ctx, "tenant_demo", "deltaLink-123"); err != nil {
		t.Fatal(err)
	}
	if v, _ := fs.Load(ctx, "tenant_demo"); v != "deltaLink-123" {
		t.Fatalf("expected persisted token, got %q", v)
	}
}
