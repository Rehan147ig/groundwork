package main

import (
	"context"

	"groundwork/query-runtime/internal/aclsync/msgraph"
)

// directoryOnlyGraphClient wraps a msgraph.GraphClient and stubs out the
// drive operations. Used in PR #18 of the Microsoft Graph pilot so that
// Connector.Snapshot reads users + groups + memberships and skips
// SharePoint enumeration entirely.
//
// All directory-side methods (ListUsers, ListGroups, ListGroupMembers) pass
// through to the wrapped client unchanged. The drive-side methods
// (ListDriveItems, ListItemPermissions, DeltaDriveItems) return empty
// slices and nil errors, so Snapshot iterates over zero items.
//
// This decorator lives in the binary package so the embedded library
// (internal/aclsync/msgraph) does not need to grow a "skip drives"
// configuration flag for what is a scaffold-only behavior.
type directoryOnlyGraphClient struct {
	inner msgraph.GraphClient
}

func newDirectoryOnlyGraphClient(inner msgraph.GraphClient) *directoryOnlyGraphClient {
	return &directoryOnlyGraphClient{inner: inner}
}

func (d *directoryOnlyGraphClient) ListUsers(ctx context.Context) ([]msgraph.GraphUser, error) {
	return d.inner.ListUsers(ctx)
}

func (d *directoryOnlyGraphClient) ListGroups(ctx context.Context) ([]msgraph.GraphGroup, error) {
	return d.inner.ListGroups(ctx)
}

func (d *directoryOnlyGraphClient) ListGroupMembers(ctx context.Context, groupID string) ([]msgraph.GraphMember, error) {
	return d.inner.ListGroupMembers(ctx, groupID)
}

func (d *directoryOnlyGraphClient) ListDriveItems(_ context.Context) ([]msgraph.GraphDriveItem, error) {
	return nil, nil
}

func (d *directoryOnlyGraphClient) ListItemPermissions(_ context.Context, _ string) ([]msgraph.GraphPermission, error) {
	return nil, nil
}

func (d *directoryOnlyGraphClient) DeltaDriveItems(_ context.Context, _ string) ([]msgraph.GraphDeltaItem, string, error) {
	return nil, "", nil
}

var _ msgraph.GraphClient = (*directoryOnlyGraphClient)(nil)
