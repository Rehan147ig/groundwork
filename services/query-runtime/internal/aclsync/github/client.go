// Package github is the GitHub source-of-truth connector for Groundwork.
//
// It reads an organization's teams, team memberships, repositories, and the
// team→repo access grants, and projects them onto Groundwork's connector-
// agnostic permission model (aclsync.PermissionSet): a GitHub team becomes a
// Group, a repository becomes a Document, and a team's access to a repo
// becomes a group viewer grant. From there the existing
// aclsync.PermissionSetToTuples writes the OpenFGA tuples — no
// authorization-model changes are required, which is the whole point: every
// SaaS permission system reduces to {principals, groups, containers, leaves}.
//
// Read-only by construction. The HTTP client requires only `read:org` and
// `repo` (read) PAT scopes; it never mutates GitHub state.
package github

import "context"

// Team is a GitHub org team. Slug is the URL-safe identifier
// (e.g. "finance-team") used as the Groundwork group id.
type Team struct {
	Slug string
	Name string
}

// Repo is a GitHub repository. Name is the repo name within the org
// (e.g. "finance-budget"), used as the Groundwork document id. Private
// indicates whether the repo is private — public repos are a leak-report
// signal (overexposure), not an access-control input.
type Repo struct {
	Name    string
	Private bool
}

// RepoTeamAccess is one team's access grant to a repository. Permission is
// GitHub's access level ("pull", "triage", "push", "maintain", "admin").
// Any level ≥ pull grants read visibility, which is what Groundwork models
// as a viewer grant — the runtime authorizes *retrieval*, so write-vs-read
// distinctions above "can see it" don't change the chunk-level decision.
type RepoTeamAccess struct {
	TeamSlug   string
	Permission string
}

// Client reads org structure from GitHub. The HTTP implementation
// (NewHTTPClient) talks to the GitHub REST API with a PAT; unit tests use a
// fake implementing this interface, exactly as the msgraph connector fakes
// GraphClient. Live behavior is exercised in integration, not unit tests.
type Client interface {
	// ListTeams returns every team in the org.
	ListTeams(ctx context.Context) ([]Team, error)
	// ListTeamMembers returns the login names of the members of a team.
	ListTeamMembers(ctx context.Context, teamSlug string) ([]string, error)
	// ListRepos returns every repository in the org.
	ListRepos(ctx context.Context) ([]Repo, error)
	// ListRepoTeams returns the teams that have access to a repository,
	// with the access level each holds.
	ListRepoTeams(ctx context.Context, repo string) ([]RepoTeamAccess, error)
}
