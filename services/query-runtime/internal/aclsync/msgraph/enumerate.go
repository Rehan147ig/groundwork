package msgraph

import "context"

// DirectoryStats summarises a single EnumerateDirectory run. PrincipalsUpserted
// is the number of users observed in the source (not the number of new rows
// inserted into the catalog — see catalog_postgres.go for the upsert
// semantics). Same shape for Groups and Memberships.
type DirectoryStats struct {
	PrincipalsUpserted  int
	GroupsUpserted      int
	MembershipsUpserted int
}

// EnumerateDirectory reads users + groups + memberships from Microsoft Graph
// and persists them into the supplied CatalogWriter. It is the PR #19
// "visibility only" flow: it does not invoke the runtime's PrincipalResolver,
// it does not write OpenFGA tuples, it does not touch SharePoint, and it does
// not produce an aclsync.PermissionSet. The companion full-snapshot path
// (Connector.Snapshot, used by aclsync.Service.RunOnce in PR #18) is left
// untouched.
//
// The traversal order is deterministic — users first, then for each group its
// members — so that membership tuples can be safely related back to the
// principals they reference even if EnumerateDirectory is interrupted partway
// through (the next run restarts from the top and converges on the same
// catalog state).
//
// Errors are returned eagerly. A Graph auth failure (ErrAuthFailed) propagates
// out without partial persistence guarantees — re-running after credentials
// are fixed converges the catalog. A catalog write failure also propagates;
// the operator should inspect Postgres connectivity before retrying.
func (c *Connector) EnumerateDirectory(ctx context.Context, tenantID string, w CatalogWriter) (DirectoryStats, error) {
	var stats DirectoryStats

	users, err := c.client.ListUsers(ctx)
	if err != nil {
		return stats, err
	}
	for _, u := range users {
		p := Principal{
			EntraOID:    u.ID,
			UPN:         u.UserPrincipalName,
			Email:       u.Mail,
			DisplayName: u.DisplayName,
			// Microsoft Graph's /users $select in this build does not include
			// accountEnabled; PR #20+ widens the select when we need
			// revocation behaviour driven by the disabled flag. For PR #19
			// every observed user is recorded as enabled.
			AccountEnabled: true,
		}
		if err := w.UpsertPrincipal(ctx, tenantID, p); err != nil {
			return stats, err
		}
		stats.PrincipalsUpserted++
	}

	groups, err := c.client.ListGroups(ctx)
	if err != nil {
		return stats, err
	}
	for _, g := range groups {
		if err := w.UpsertGroup(ctx, tenantID, Group{
			EntraGroupID: g.ID,
			DisplayName:  g.DisplayName,
			// GroupType is not populated by /groups $select=id,displayName;
			// we leave it empty here and let PR #20+ widen the select.
		}); err != nil {
			return stats, err
		}
		stats.GroupsUpserted++

		members, err := c.client.ListGroupMembers(ctx, g.ID)
		if err != nil {
			return stats, err
		}
		for _, m := range members {
			memberType := "user"
			if m.Type == MemberGroup {
				memberType = "group"
			}
			if err := w.UpsertMembership(ctx, tenantID, Membership{
				GroupID:    g.ID,
				MemberID:   m.ID,
				MemberType: memberType,
			}); err != nil {
				return stats, err
			}
			stats.MembershipsUpserted++
		}
	}

	return stats, nil
}
