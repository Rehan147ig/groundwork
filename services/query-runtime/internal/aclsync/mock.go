package aclsync

import (
	"context"
	"sync"
)

// MockConnector models a Microsoft Entra / SharePoint-style source of truth in memory:
// users, (nested) groups, folders, and documents that inherit folder permissions. It
// supports revocation so the revocation-SLA test can prove that a sync removes access.
//
// Default dataset (tenant "tenant_demo"):
//
//	users:   finance_user, general_user, executive_user
//	groups:  finance      = {finance_user}
//	         executives   = {executive_user}
//	         employees    = {general_user} + nested {finance#member, executives#member}
//	folders: finance-folder    viewer group:finance
//	         public-folder     viewer group:employees
//	         executive-folder  viewer group:executives
//	docs:    security-policy -> finance-folder   (finance can view)
//	         handbook        -> public-folder     (all employees can view)
//	         board-minutes   -> executive-folder  (executives can view)
type MockConnector struct {
	mu      sync.Mutex
	tenant  string
	set     PermissionSet
	changes chan PermissionChange
}

// NewMockConnector returns a MockConnector seeded with the default enterprise dataset.
func NewMockConnector() *MockConnector {
	tenant := "tenant_demo"
	return &MockConnector{
		tenant:  tenant,
		changes: make(chan PermissionChange, 64),
		set: PermissionSet{
			TenantID: tenant,
			Users:    []string{"finance_user", "general_user", "executive_user"},
			Groups: []Group{
				{ID: "finance", MemberUsers: []string{"finance_user"}},
				{ID: "executives", MemberUsers: []string{"executive_user"}},
				// Nested groups: members of finance and executives are also employees.
				{ID: "employees", MemberUsers: []string{"general_user"}, MemberGroups: []string{"finance", "executives"}},
			},
			Folders: []Folder{
				{ID: "finance-folder", ViewerGroups: []string{"finance"}},
				{ID: "public-folder", ViewerGroups: []string{"employees"}},
				{ID: "executive-folder", ViewerGroups: []string{"executives"}},
			},
			Documents: []Document{
				{ID: "security-policy", FolderID: "finance-folder"},
				{ID: "handbook", FolderID: "public-folder"},
				{ID: "board-minutes", FolderID: "executive-folder"},
			},
		},
	}
}

func (m *MockConnector) Snapshot(_ context.Context, tenantID string) (PermissionSet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cloneLocked(), nil
}

func (m *MockConnector) ListDocuments(_ context.Context, tenantID string) ([]Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Document, len(m.set.Documents))
	copy(out, m.set.Documents)
	return out, nil
}

func (m *MockConnector) GetDocumentPermissions(_ context.Context, tenantID, documentID string) (DocumentPermissions, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.set.Documents {
		if d.ID != documentID {
			continue
		}
		perms := DocumentPermissions{
			DocumentID:   d.ID,
			FolderID:     d.FolderID,
			ViewerUsers:  append([]string{}, d.ViewerUsers...),
			ViewerGroups: append([]string{}, d.ViewerGroups...),
		}
		// Fold in the folder's viewers (the inherited grants).
		for _, f := range m.set.Folders {
			if f.ID == d.FolderID {
				perms.ViewerUsers = append(perms.ViewerUsers, f.ViewerUsers...)
				perms.ViewerGroups = append(perms.ViewerGroups, f.ViewerGroups...)
			}
		}
		return perms, nil
	}
	return DocumentPermissions{}, nil
}

func (m *MockConnector) WatchPermissionChanges(_ context.Context, tenantID string) (<-chan PermissionChange, error) {
	return m.changes, nil
}

// RevokeGroupMember removes a user from a group and emits a revocation event. After a
// re-sync, the user loses every permission that flowed through that group (including
// inherited document access).
func (m *MockConnector) RevokeGroupMember(group, user string) {
	m.mu.Lock()
	for i := range m.set.Groups {
		if m.set.Groups[i].ID != group {
			continue
		}
		kept := m.set.Groups[i].MemberUsers[:0:0]
		for _, u := range m.set.Groups[i].MemberUsers {
			if u != user {
				kept = append(kept, u)
			}
		}
		m.set.Groups[i].MemberUsers = kept
	}
	m.mu.Unlock()
	m.emit(PermissionChange{Type: ChangeRevokeGroupMember, Subject: userRef(user), Object: groupRef(group)})
}

// AddGroupMember adds a user to a group and emits an event (used for completeness/tests).
func (m *MockConnector) AddGroupMember(group, user string) {
	m.mu.Lock()
	for i := range m.set.Groups {
		if m.set.Groups[i].ID == group {
			m.set.Groups[i].MemberUsers = append(m.set.Groups[i].MemberUsers, user)
		}
	}
	m.mu.Unlock()
	m.emit(PermissionChange{Type: ChangeAddGroupMember, Subject: userRef(user), Object: groupRef(group)})
}

// RemoveDocument deletes a document from the source (used for orphan-drift tests).
func (m *MockConnector) RemoveDocument(documentID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := m.set.Documents[:0:0]
	for _, d := range m.set.Documents {
		if d.ID != documentID {
			kept = append(kept, d)
		}
	}
	m.set.Documents = kept
}

func (m *MockConnector) emit(c PermissionChange) {
	select {
	case m.changes <- c:
	default: // never block the caller if no one is watching
	}
}

func (m *MockConnector) cloneLocked() PermissionSet {
	cp := PermissionSet{TenantID: m.set.TenantID}
	cp.Users = append([]string{}, m.set.Users...)
	for _, g := range m.set.Groups {
		cp.Groups = append(cp.Groups, Group{
			ID:           g.ID,
			MemberUsers:  append([]string{}, g.MemberUsers...),
			MemberGroups: append([]string{}, g.MemberGroups...),
		})
	}
	for _, f := range m.set.Folders {
		cp.Folders = append(cp.Folders, Folder{
			ID:           f.ID,
			ViewerUsers:  append([]string{}, f.ViewerUsers...),
			ViewerGroups: append([]string{}, f.ViewerGroups...),
		})
	}
	for _, d := range m.set.Documents {
		cp.Documents = append(cp.Documents, Document{
			ID:           d.ID,
			FolderID:     d.FolderID,
			ViewerUsers:  append([]string{}, d.ViewerUsers...),
			ViewerGroups: append([]string{}, d.ViewerGroups...),
		})
	}
	return cp
}
