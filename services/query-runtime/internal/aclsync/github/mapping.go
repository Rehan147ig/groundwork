package github

import (
	"context"
	"fmt"
	"sort"

	"groundwork/query-runtime/internal/aclsync"
)

// grantsRead reports whether a GitHub access level confers at least read
// visibility. Every standard level does ("pull" is the lowest), but we
// guard explicitly so an unexpected/empty permission string fails closed
// (no viewer grant) rather than silently granting access.
func grantsRead(permission string) bool {
	switch permission {
	case "pull", "triage", "push", "maintain", "admin":
		return true
	default:
		return false
	}
}

// BuildPermissionSet reads the org's teams, memberships, repos, and
// team→repo grants from the Client and projects them onto an
// aclsync.PermissionSet:
//
//   - each Team      -> Group{ID: slug, MemberUsers: logins}
//   - each Repo      -> Document{ID: repo name, ViewerGroups: teams with read}
//   - Users          -> the de-duplicated union of all team members
//
// Repos are modeled as Documents (leaves) rather than Folders because a
// GitHub repo is the unit a retrieval agent reads from; there is no GitHub
// sub-repo container that would need folder-style inheritance. If a repo's
// chunks are later sub-scoped (e.g. per-path CODEOWNERS), that becomes a
// folder/parent refinement without changing this mapping's shape.
//
// Identity note: the logins here are GitHub usernames (e.g. "alice-gh").
// The canonical-principal resolver maps them to user:principal:<uuid> via
// the gh:<login> alias, so a single human is enforced consistently across
// GitHub, Slack, and Atlassian. This function deals only in raw GitHub
// logins; canonicalization happens downstream, the same way the msgraph
// connector emits entra:<oid> placeholders.
//
// The GitHub connector produces a PermissionSet directly and does not
// implement the heavier aclsync.Connector (Watch / GetDocumentPermissions)
// surface, mirroring how msgraph's EnumerateDirectory bypasses
// Service.RunOnce: a full snapshot is all the demo and the initial sync
// need; incremental watch is a later refinement.
func BuildPermissionSet(ctx context.Context, c Client, tenantID string) (aclsync.PermissionSet, error) {
	if c == nil {
		return aclsync.PermissionSet{}, fmt.Errorf("github client is nil")
	}

	teams, err := c.ListTeams(ctx)
	if err != nil {
		return aclsync.PermissionSet{}, fmt.Errorf("list teams: %w", err)
	}

	userSet := map[string]struct{}{}
	groups := make([]aclsync.Group, 0, len(teams))
	for _, t := range teams {
		members, err := c.ListTeamMembers(ctx, t.Slug)
		if err != nil {
			return aclsync.PermissionSet{}, fmt.Errorf("list members of team %s: %w", t.Slug, err)
		}
		for _, m := range members {
			userSet[m] = struct{}{}
		}
		groups = append(groups, aclsync.Group{ID: t.Slug, MemberUsers: dedupeSorted(members)})
	}

	repos, err := c.ListRepos(ctx)
	if err != nil {
		return aclsync.PermissionSet{}, fmt.Errorf("list repos: %w", err)
	}
	documents := make([]aclsync.Document, 0, len(repos))
	for _, r := range repos {
		access, err := c.ListRepoTeams(ctx, r.Name)
		if err != nil {
			return aclsync.PermissionSet{}, fmt.Errorf("list teams for repo %s: %w", r.Name, err)
		}
		var viewerGroups []string
		for _, a := range access {
			if grantsRead(a.Permission) {
				viewerGroups = append(viewerGroups, a.TeamSlug)
			}
		}
		documents = append(documents, aclsync.Document{
			ID:           r.Name,
			ViewerGroups: dedupeSorted(viewerGroups),
		})
	}

	return aclsync.PermissionSet{
		TenantID:  tenantID,
		Users:     dedupeSortedSet(userSet),
		Groups:    groups,
		Documents: documents,
	}, nil
}

func dedupeSorted(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func dedupeSortedSet(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
