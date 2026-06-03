// Package aclsync syncs enterprise source-of-truth permissions (Microsoft Entra /
// SharePoint, Okta, etc.) into OpenFGA so Groundwork stops relying on manually seeded
// permissions. It defines a connector interface, a mock enterprise connector, a sync
// worker that translates source permissions into OpenFGA tuples, and drift detection.
//
// It writes tuples that conform to the authorization model in
// internal/runtime/openfga.go (user / nested group / folder / document-with-inheritance).
// It does NOT change the query-time ACL check — query enforcement still runs through the
// unchanged engine + OpenFGA "user viewer document" check; this package only feeds OpenFGA.
package aclsync

import "context"

// Group is a source-of-truth group. MemberGroups models nested groups: every member of
// a sub-group is also a member of this group.
type Group struct {
	ID           string
	MemberUsers  []string
	MemberGroups []string
}

// Folder carries viewer grants that documents under it inherit.
type Folder struct {
	ID           string
	ViewerUsers  []string
	ViewerGroups []string
}

// Document inherits its parent folder's viewers and may have direct viewers too.
type Document struct {
	ID           string
	FolderID     string
	ViewerUsers  []string
	ViewerGroups []string
}

// PermissionSet is a full snapshot of a tenant's source permissions.
type PermissionSet struct {
	TenantID  string
	Users     []string
	Groups    []Group
	Folders   []Folder
	Documents []Document
}

// DocumentPermissions describes who may view a single document (direct grants plus the
// grants inherited from its folder).
type DocumentPermissions struct {
	DocumentID   string
	FolderID     string
	ViewerUsers  []string
	ViewerGroups []string
}

// ChangeType enumerates source permission-change events.
type ChangeType string

const (
	ChangeAddGroupMember       ChangeType = "add_group_member"
	ChangeRevokeGroupMember    ChangeType = "revoke_group_member"
	ChangeRevokeFolderViewer   ChangeType = "revoke_folder_viewer"
	ChangeRevokeDocumentViewer ChangeType = "revoke_document_viewer"
)

// PermissionChange is a single source permission change (e.g. a revocation). Subject and
// Object are OpenFGA-style identifiers, e.g. Subject "user:finance_user", Object
// "group:finance".
type PermissionChange struct {
	Type    ChangeType
	Subject string
	Object  string
}

// Connector reads permissions from an enterprise source of truth. Real connectors
// (Microsoft Graph / SharePoint, Okta, Google) implement this interface later; the mock
// connector implements it now.
//
// The required operations are:
//   - ListDocuments / GetDocumentPermissions — granular reads
//   - WatchPermissionChanges — a stream of revocations/grants for incremental sync
//   - Snapshot — a full read used by the sync worker
//
// (The fourth required operation, SyncToOpenFGA, belongs to the Syncer, which consumes a
// Connector — see sync.go.)
type Connector interface {
	ListDocuments(ctx context.Context, tenantID string) ([]Document, error)
	GetDocumentPermissions(ctx context.Context, tenantID, documentID string) (DocumentPermissions, error)
	WatchPermissionChanges(ctx context.Context, tenantID string) (<-chan PermissionChange, error)
	Snapshot(ctx context.Context, tenantID string) (PermissionSet, error)
}
