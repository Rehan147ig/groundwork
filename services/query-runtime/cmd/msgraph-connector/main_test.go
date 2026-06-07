package main

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"groundwork/query-runtime/internal/aclsync"
	"groundwork/query-runtime/internal/aclsync/msgraph"
)

// --- env-validation tests (carried forward from PR #17 unchanged) -----------

func TestMissingEnvVars(t *testing.T) {
	missing := validate(func(string) string { return "" })
	if len(missing) != len(requiredEnv) {
		t.Fatalf("expected all %d env vars missing, got %d: %v",
			len(requiredEnv), len(missing), missing)
	}
}

func TestValidConfig(t *testing.T) {
	env := map[string]string{
		"MSGRAPH_TENANT_ID":     "tenant-id-value",
		"MSGRAPH_CLIENT_ID":     "client-id-value",
		"MSGRAPH_CLIENT_SECRET": "client-secret-value",
		"OPENFGA_STORE_ID":      "store-id-value",
	}
	missing := validate(func(k string) string { return env[k] })
	if len(missing) != 0 {
		t.Fatalf("expected no missing env vars with valid config, got: %v", missing)
	}
}

func TestPartiallyMissing(t *testing.T) {
	env := map[string]string{
		"MSGRAPH_TENANT_ID": "tenant-id-value",
		"MSGRAPH_CLIENT_ID": "client-id-value",
	}
	missing := validate(func(k string) string { return env[k] })
	if len(missing) != 2 {
		t.Fatalf("expected 2 missing env vars, got %d: %v", len(missing), missing)
	}
}

// --- connector startup tests ------------------------------------------------

// fakeGraphClient implements msgraph.GraphClient with in-memory canned data
// and an optional auth error. Tests use it to drive the wiring without
// requiring live Microsoft credentials.
type fakeGraphClient struct {
	users   []msgraph.GraphUser
	groups  []msgraph.GraphGroup
	members map[string][]msgraph.GraphMember
	authErr error
}

func (f *fakeGraphClient) ListUsers(_ context.Context) ([]msgraph.GraphUser, error) {
	if f.authErr != nil {
		return nil, f.authErr
	}
	return f.users, nil
}

func (f *fakeGraphClient) ListGroups(_ context.Context) ([]msgraph.GraphGroup, error) {
	if f.authErr != nil {
		return nil, f.authErr
	}
	return f.groups, nil
}

func (f *fakeGraphClient) ListGroupMembers(_ context.Context, groupID string) ([]msgraph.GraphMember, error) {
	if f.authErr != nil {
		return nil, f.authErr
	}
	return f.members[groupID], nil
}

// Drive operations should never be invoked once the directory-only decorator
// is in place. The tests assert this by failing if they are.
func (f *fakeGraphClient) ListDriveItems(_ context.Context) ([]msgraph.GraphDriveItem, error) {
	panic("ListDriveItems must not be called in directory-only mode")
}

func (f *fakeGraphClient) ListItemPermissions(_ context.Context, _ string) ([]msgraph.GraphPermission, error) {
	panic("ListItemPermissions must not be called in directory-only mode")
}

func (f *fakeGraphClient) DeltaDriveItems(_ context.Context, _ string) ([]msgraph.GraphDeltaItem, string, error) {
	panic("DeltaDriveItems must not be called in directory-only mode")
}

// runOnceWithFake encapsulates the wiring main() performs against a Graph
// client, so the same pipeline can be exercised under test.
func runOnceWithFake(t *testing.T, client msgraph.GraphClient) (*aclsync.DiscardSink, error) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(testWriter{t}, nil))
	cfg := msgraph.Config{TenantID: "tenant-test"}
	directoryOnly := newDirectoryOnlyGraphClient(client)
	connector := msgraph.NewConnector(directoryOnly, cfg, logger, nil)
	sink := aclsync.NewDiscardSink(logger)
	syncer := aclsync.NewSyncer(connector, sink, logger)
	service := aclsync.NewService(connector, syncer, aclsync.Config{
		Mode:     aclsync.ModeOnce,
		TenantID: cfg.TenantID,
	}, logger, nil)
	return sink, service.RunOnce(context.Background())
}

// testWriter routes slog output to the test's Log so debug noise only shows
// up on failure.
type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) { w.t.Log(string(p)); return len(p), nil }

// TestConnectorStartup: a happy-path fake Graph client produces a successful
// Service.RunOnce against the DiscardSink, with the expected number of
// observed tuples (group-membership tuples only, no folder/document tuples
// since drive ops are stubbed out).
func TestConnectorStartup(t *testing.T) {
	client := &fakeGraphClient{
		users: []msgraph.GraphUser{
			{ID: "u1", DisplayName: "Alice", UserPrincipalName: "alice@example.com"},
			{ID: "u2", DisplayName: "Bob", UserPrincipalName: "bob@example.com"},
		},
		groups: []msgraph.GraphGroup{{ID: "g1", DisplayName: "Finance"}},
		members: map[string][]msgraph.GraphMember{
			"g1": {{ID: "u1", Type: msgraph.MemberUser}},
		},
	}
	sink, err := runOnceWithFake(t, client)
	if err != nil {
		t.Fatalf("RunOnce failed: %v", err)
	}
	// One group with one member -> one user→member→group tuple. Drive ops are
	// stubbed, so there are no folder/document tuples.
	if got := sink.WrittenCount(); got == 0 {
		t.Fatalf("WrittenCount: expected at least one tuple observed, got 0")
	}
	t.Logf("connector observed %d tuples (DiscardSink)", sink.WrittenCount())
}

// TestAuthenticationFailure: a Graph client that returns ErrAuthFailed
// propagates the error all the way out of Service.RunOnce. The Service's
// destructive-delete guard ensures no tuples are deleted; here the Sink is
// DiscardSink which doesn't reach OpenFGA at all, but the error path is
// what main() exits 3 on.
func TestAuthenticationFailure(t *testing.T) {
	client := &fakeGraphClient{authErr: msgraph.ErrAuthFailed}
	_, err := runOnceWithFake(t, client)
	if err == nil {
		t.Fatalf("expected RunOnce to fail on auth error")
	}
	if !errors.Is(err, msgraph.ErrAuthFailed) {
		t.Fatalf("expected ErrAuthFailed, got: %v", err)
	}
}

// TestDirectoryOnlyDecoratorSkipsDrives: the decorator must return empty
// slices from drive operations. If it ever forwards a drive call to the
// underlying client, the fake will panic — which is the assertion's whole
// point. PR #19 will remove this decorator when SharePoint enumeration goes
// live.
func TestDirectoryOnlyDecoratorSkipsDrives(t *testing.T) {
	d := newDirectoryOnlyGraphClient(&fakeGraphClient{})
	items, err := d.ListDriveItems(context.Background())
	if err != nil {
		t.Fatalf("ListDriveItems must not error in directory-only mode: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("ListDriveItems must return empty slice, got %d items", len(items))
	}
	perms, err := d.ListItemPermissions(context.Background(), "any-item")
	if err != nil || len(perms) != 0 {
		t.Fatalf("ListItemPermissions must return (nil, nil), got (%v, %v)", perms, err)
	}
	delta, next, err := d.DeltaDriveItems(context.Background(), "")
	if err != nil || len(delta) != 0 || next != "" {
		t.Fatalf("DeltaDriveItems must return (nil, \"\", nil), got (%v, %q, %v)", delta, next, err)
	}
}
