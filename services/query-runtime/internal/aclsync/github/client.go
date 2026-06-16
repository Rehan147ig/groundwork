package github

import (
	"context"
)

// DTOs for GitHub API
type GitHubUser struct {
	ID    int    `json:"id"`
	Login string `json:"login"`
}

type GitHubTeam struct {
	ID   int    `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

type GitHubRepo struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	FullName    string `json:"full_name"`
	Private     bool   `json:"private"`
	Description string `json:"description"`
}

type GitHubTeamRepo struct {
	GitHubRepo
	Permissions map[string]bool `json:"permissions"`
}

// GitHubClient is a fakeable interface for the GitHub API.
type GitHubClient interface {
	ListOrgMembers(ctx context.Context, org string) ([]GitHubUser, error)
	ListTeams(ctx context.Context, org string) ([]GitHubTeam, error)
	ListTeamMembers(ctx context.Context, org string, teamSlug string) ([]GitHubUser, error)
	ListTeamRepos(ctx context.Context, org string, teamSlug string) ([]GitHubTeamRepo, error)
	ListRepos(ctx context.Context, org string) ([]GitHubRepo, error)
}
