package github

import (
	"groundwork/query-runtime/internal/aclsync"
)

func userKey(login string) string {
	return login
}

func teamKey(slug string) string {
	return slug
}

func repoKey(name string) string {
	return "gh:" + name
}

func mapUsers(users []GitHubUser) []string {
	out := make([]string, 0, len(users))
	for _, u := range users {
		out = append(out, userKey(u.Login))
	}
	return out
}

func mapTeamToGroup(team GitHubTeam, members []GitHubUser) aclsync.Group {
	grp := aclsync.Group{ID: teamKey(team.Slug)}
	for _, m := range members {
		grp.MemberUsers = append(grp.MemberUsers, userKey(m.Login))
	}
	return grp
}

func mapRepoToDocument(repo GitHubRepo, teams []string) aclsync.Document {
	return aclsync.Document{
		ID:           repoKey(repo.Name),
		ViewerGroups: teams,
	}
}

func grantsRead(perms map[string]bool) bool {
	if perms == nil {
		return false
	}
	return perms["pull"] || perms["push"] || perms["admin"] || perms["maintain"] || perms["triage"]
}
