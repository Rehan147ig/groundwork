package msgraph

import (
	"strings"

	"groundwork/query-runtime/internal/aclsync"
)

// userKey is the canonical Groundwork user identifier derived from Graph identity, in
// priority order: mail, then userPrincipalName, then object id. This MUST match the
// effective user_id the query-time identity token yields (sub/email/preferred_username);
// see docs/microsoft-graph-connector.md.
func userKey(mail, upn, id string) string {
	if v := strings.TrimSpace(mail); v != "" {
		return v
	}
	if v := strings.TrimSpace(upn); v != "" {
		return v
	}
	return id
}

func userKeyOfUser(u GraphUser) string     { return userKey(u.Mail, u.UserPrincipalName, u.ID) }
func userKeyOfMember(m GraphMember) string { return userKey(m.Mail, m.UserPrincipalName, m.ID) }

// Groups are keyed by Entra object id (stable; permissions reference groups by id, so
// keying by id keeps membership and permission tuples consistent).
func groupKey(id string) string { return id }

// userKeyByID maps Graph object id -> canonical user key, so SharePoint permissions
// (which reference users by object id) resolve to the same key used for identity.
func userKeyByID(users []GraphUser) map[string]string {
	m := make(map[string]string, len(users))
	for _, u := range users {
		m[u.ID] = userKeyOfUser(u)
	}
	return m
}

func mapUsers(users []GraphUser) []string {
	out := make([]string, 0, len(users))
	for _, u := range users {
		out = append(out, userKeyOfUser(u))
	}
	return out
}

// mapGroup builds an aclsync.Group from a Graph group and its members (users + nested groups).
func mapGroup(g GraphGroup, members []GraphMember) aclsync.Group {
	grp := aclsync.Group{ID: groupKey(g.ID)}
	for _, m := range members {
		if m.Type == MemberGroup {
			grp.MemberGroups = append(grp.MemberGroups, groupKey(m.ID))
		} else {
			grp.MemberUsers = append(grp.MemberUsers, userKeyOfMember(m))
		}
	}
	return grp
}

func mapFolder(it GraphDriveItem, perms []GraphPermission, userByID map[string]string) aclsync.Folder {
	users, groups := granteesFromPerms(perms, userByID)
	return aclsync.Folder{ID: it.ID, ViewerUsers: users, ViewerGroups: groups}
}

func mapDocument(it GraphDriveItem, perms []GraphPermission, userByID map[string]string) aclsync.Document {
	users, groups := granteesFromPerms(perms, userByID)
	// FolderID gives folder->document inheritance in the OpenFGA model; direct grants
	// (incl. those Graph reports as inherited on the item) are captured as viewers too.
	return aclsync.Document{ID: it.ID, FolderID: it.ParentID, ViewerUsers: users, ViewerGroups: groups}
}

// granteesFromPerms maps read-granting SharePoint permissions to viewer users/groups.
func granteesFromPerms(perms []GraphPermission, userByID map[string]string) (users, groups []string) {
	for _, p := range perms {
		if !grantsRead(p.Roles) {
			continue
		}
		switch {
		case p.Grantee.GroupID != "":
			groups = append(groups, groupKey(p.Grantee.GroupID))
		case p.Grantee.UserID != "":
			key := userByID[p.Grantee.UserID]
			if key == "" {
				key = userKey(p.Grantee.UserMail, p.Grantee.UserUPN, p.Grantee.UserID)
			}
			users = append(users, key)
		}
	}
	return users, groups
}

// grantsRead reports whether a permission's roles confer at least read access.
func grantsRead(roles []string) bool {
	for _, r := range roles {
		l := strings.ToLower(r)
		if strings.Contains(l, "read") || strings.Contains(l, "write") ||
			strings.Contains(l, "owner") || strings.Contains(l, "contribute") ||
			strings.Contains(l, "fullcontrol") || strings.Contains(l, "full control") {
			return true
		}
	}
	return false
}

// revokeChange maps a removed SharePoint permission to the precise aclsync revoke event
// that deletes the corresponding OpenFGA tuple at query time. Returns ok=false when the
// grantee is unrecognized.
func revokeChange(item GraphDriveItem, removed GraphPermission, userByID map[string]string) (aclsync.PermissionChange, bool) {
	objectPrefix := "document:"
	changeType := aclsync.ChangeRevokeDocumentViewer
	if item.IsFolder {
		objectPrefix = "folder:"
		changeType = aclsync.ChangeRevokeFolderViewer
	}
	switch {
	case removed.Grantee.GroupID != "":
		return aclsync.PermissionChange{
			Type:    changeType,
			Subject: "group:" + groupKey(removed.Grantee.GroupID) + "#member",
			Object:  objectPrefix + item.ID,
		}, true
	case removed.Grantee.UserID != "":
		key := userByID[removed.Grantee.UserID]
		if key == "" {
			key = userKey(removed.Grantee.UserMail, removed.Grantee.UserUPN, removed.Grantee.UserID)
		}
		return aclsync.PermissionChange{
			Type:    changeType,
			Subject: "user:" + key,
			Object:  objectPrefix + item.ID,
		}, true
	}
	return aclsync.PermissionChange{}, false
}

// classifyDelta splits a Graph delta page into added/modified/deleted item ids (used for
// change detection + logging in watch mode).
func classifyDelta(items []GraphDeltaItem) (deleted, changed []string) {
	for _, it := range items {
		if it.Deleted {
			deleted = append(deleted, it.ID)
		} else {
			changed = append(changed, it.ID)
		}
	}
	return deleted, changed
}
