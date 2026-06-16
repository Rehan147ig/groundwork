package github

import (
	"context"
	"fmt"
)

type MockClient struct {
	Org    string
	Users  []GitHubUser
	Teams  []GitHubTeam
	Repos  []GitHubRepo
	Member map[string][]GitHubUser     // teamSlug -> users
	Grants map[string][]GitHubTeamRepo // teamSlug -> repos
}

func NewMockClient() *MockClient {
	m := &MockClient{
		Org: "acme-financial",
		Users: []GitHubUser{
			{1, "alice"}, {2, "bob"}, {3, "carol"}, {4, "dave"}, {5, "eve"},
		},
		Teams: []GitHubTeam{
			{1, "finance-team", "Finance Team"},
			{2, "engineering-team", "Engineering Team"},
			{3, "hr-team", "HR Team"},
			{4, "security-team", "Security Team"},
			{5, "executive-team", "Executive Team"},
		},
		Repos: []GitHubRepo{
			{1, "finance-budget", "acme-financial/finance-budget", true, ""},
			{2, "payroll-system", "acme-financial/payroll-system", true, ""},
			{3, "engineering-platform", "acme-financial/engineering-platform", true, ""},
			{4, "security-audit", "acme-financial/security-audit", true, ""},
			{5, "executive-strategy", "acme-financial/executive-strategy", true, ""},
		},
		Member: make(map[string][]GitHubUser),
		Grants: make(map[string][]GitHubTeamRepo),
	}

	m.Member["finance-team"] = []GitHubUser{{1, "alice"}}
	m.Member["engineering-team"] = []GitHubUser{{2, "bob"}}
	m.Member["hr-team"] = []GitHubUser{{3, "carol"}}
	m.Member["security-team"] = []GitHubUser{{4, "dave"}}
	m.Member["executive-team"] = []GitHubUser{{5, "eve"}}

	// Repo grants
	readPerms := map[string]bool{"pull": true}

	m.Grants["finance-team"] = []GitHubTeamRepo{
		{m.Repos[0], readPerms}, // finance-budget
	}
	m.Grants["engineering-team"] = []GitHubTeamRepo{
		{m.Repos[1], readPerms}, // payroll-system
		{m.Repos[2], readPerms}, // engineering-platform
		{m.Repos[0], readPerms}, // finance-budget (leak scenario)
	}
	m.Grants["security-team"] = []GitHubTeamRepo{
		{m.Repos[3], readPerms}, // security-audit
	}
	m.Grants["executive-team"] = []GitHubTeamRepo{
		{m.Repos[4], readPerms}, // executive-strategy
	}
	// HR has no repos

	return m
}

func (m *MockClient) ListOrgMembers(ctx context.Context, org string) ([]GitHubUser, error) {
	if org != m.Org {
		return nil, fmt.Errorf("org not found")
	}
	return m.Users, nil
}

func (m *MockClient) ListTeams(ctx context.Context, org string) ([]GitHubTeam, error) {
	if org != m.Org {
		return nil, fmt.Errorf("org not found")
	}
	return m.Teams, nil
}

func (m *MockClient) ListTeamMembers(ctx context.Context, org string, teamSlug string) ([]GitHubUser, error) {
	if org != m.Org {
		return nil, fmt.Errorf("org not found")
	}
	return m.Member[teamSlug], nil
}

func (m *MockClient) ListTeamRepos(ctx context.Context, org string, teamSlug string) ([]GitHubTeamRepo, error) {
	if org != m.Org {
		return nil, fmt.Errorf("org not found")
	}
	return m.Grants[teamSlug], nil
}

func (m *MockClient) ListRepos(ctx context.Context, org string) ([]GitHubRepo, error) {
	if org != m.Org {
		return nil, fmt.Errorf("org not found")
	}
	return m.Repos, nil
}

var _ GitHubClient = (*MockClient)(nil)
