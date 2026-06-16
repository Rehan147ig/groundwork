package github

import (
	"context"
	"errors"
	"testing"

	"groundwork/query-runtime/internal/aclsync"
)

// fakeClient is the Acme Financial Services GitHub org, in memory. Mirrors
// msgraph's fakeEnumClient: canned data, no network. The deliberate trap —
// payroll-system belongs to engineering-team, not hr-team — is encoded here
// so the DENY scenario is real, not asserted in a vacuum.
type fakeClient struct {
	teams     []Team
	members   map[string][]string // teamSlug -> logins
	repos     []Repo
	repoTeams map[string][]RepoTeamAccess // repo -> team grants
	err       error
}

func (f *fakeClient) ListTeams(context.Context) ([]Team, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.teams, nil
}
func (f *fakeClient) ListTeamMembers(_ context.Context, slug string) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.members[slug], nil
}
func (f *fakeClient) ListRepos(context.Context) ([]Repo, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.repos, nil
}
func (f *fakeClient) ListRepoTeams(_ context.Context, repo string) ([]RepoTeamAccess, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.repoTeams[repo], nil
}

func acmeOrg() *fakeClient {
	return &fakeClient{
		teams: []Team{
			{Slug: "finance-team", Name: "Finance"},
			{Slug: "engineering-team", Name: "Engineering"},
			{Slug: "hr-team", Name: "HR"},
			{Slug: "security-team", Name: "Security"},
			{Slug: "executive-team", Name: "Executive"},
		},
		members: map[string][]string{
			"finance-team":     {"alice-gh"},
			"engineering-team": {"bob-gh"},
			"hr-team":          {"carol-gh"},
			"security-team":    {"dave-gh"},
			"executive-team":   {"eve-gh"},
		},
		repos: []Repo{
			{Name: "finance-budget", Private: true},
			{Name: "payroll-system", Private: true},
			{Name: "engineering-platform", Private: true},
			{Name: "security-audit", Private: true},
			{Name: "executive-strategy", Private: true},
		},
		repoTeams: map[string][]RepoTeamAccess{
			"finance-budget":       {{TeamSlug: "finance-team", Permission: "push"}},
			"payroll-system":       {{TeamSlug: "engineering-team", Permission: "push"}}, // trap: NOT hr-team
			"engineering-platform": {{TeamSlug: "engineering-team", Permission: "push"}},
			"security-audit":       {{TeamSlug: "security-team", Permission: "push"}},
			"executive-strategy":   {{TeamSlug: "executive-team", Permission: "admin"}},
		},
	}
}

// canView replays the OpenFGA tuple set the connector produces and answers
// "can <login> view <repo>?" the same way the runtime's checker would:
// a user can view a document if a team they're a member of has a viewer
// grant on it. This lets the test assert demo-scenario decisions directly
// against the emitted tuples, not just the PermissionSet shape.
func canView(tuples []aclsync.Tuple, login, repo string) bool {
	userMember := map[string]bool{}  // team -> member?
	groupViewer := map[string]bool{} // team -> viewer of repo?
	for _, t := range tuples {
		if t.Relation == "member" && t.User == "user:"+login {
			userMember[t.Object] = true // object = group:<team>
		}
		if t.Relation == "viewer" && t.Object == "document:"+repo {
			groupViewer[t.User] = true // user = group:<team>#member
		}
	}
	for groupObj := range userMember { // group:<team>
		if groupViewer[groupObj+"#member"] {
			return true
		}
	}
	return false
}

func TestBuildPermissionSet_AcmeShape(t *testing.T) {
	ps, err := BuildPermissionSet(context.Background(), acmeOrg(), "acme-financial")
	if err != nil {
		t.Fatalf("BuildPermissionSet: %v", err)
	}
	if ps.TenantID != "acme-financial" {
		t.Fatalf("tenant: %q", ps.TenantID)
	}
	if len(ps.Groups) != 5 {
		t.Fatalf("groups: want 5, got %d", len(ps.Groups))
	}
	if len(ps.Documents) != 5 {
		t.Fatalf("documents: want 5, got %d", len(ps.Documents))
	}
	if len(ps.Users) != 5 {
		t.Fatalf("users: want 5 unique, got %d (%v)", len(ps.Users), ps.Users)
	}
	// finance-budget must be viewable only by finance-team.
	for _, d := range ps.Documents {
		if d.ID == "finance-budget" {
			if len(d.ViewerGroups) != 1 || d.ViewerGroups[0] != "finance-team" {
				t.Fatalf("finance-budget viewers: %v", d.ViewerGroups)
			}
		}
	}
}

// TestDemoScenarios_GitHub asserts the GitHub-connector subset of the 10
// demo scenarios end-to-end: PermissionSet -> tuples -> view decision.
func TestDemoScenarios_GitHub(t *testing.T) {
	ps, err := BuildPermissionSet(context.Background(), acmeOrg(), "acme-financial")
	if err != nil {
		t.Fatalf("BuildPermissionSet: %v", err)
	}
	tuples := aclsync.PermissionSetToTuples(ps)

	cases := []struct {
		name  string
		login string
		repo  string
		want  bool
	}{
		{"S1 Alice->finance-budget ALLOW", "alice-gh", "finance-budget", true},
		{"S2 Bob->executive-strategy DENY", "bob-gh", "executive-strategy", false},
		{"S3 Dave->security-audit ALLOW", "dave-gh", "security-audit", true},
		{"S4 Carol->payroll-system DENY (eng repo, not HR)", "carol-gh", "payroll-system", false},
		{"Bob->engineering-platform ALLOW", "bob-gh", "engineering-platform", true},
		{"Eve->executive-strategy ALLOW", "eve-gh", "executive-strategy", true},
		{"Alice->executive-strategy DENY", "alice-gh", "executive-strategy", false},
	}
	for _, tc := range cases {
		if got := canView(tuples, tc.login, tc.repo); got != tc.want {
			t.Errorf("%s: canView(%s,%s)=%v want %v", tc.name, tc.login, tc.repo, got, tc.want)
		}
	}
}

func TestBuildPermissionSet_AuthErrorPropagates(t *testing.T) {
	c := &fakeClient{err: errors.New("401 bad PAT")}
	if _, err := BuildPermissionSet(context.Background(), c, "acme-financial"); err == nil {
		t.Fatal("expected error to propagate from a failing GitHub client")
	}
}

func TestBuildPermissionSet_NilClient(t *testing.T) {
	if _, err := BuildPermissionSet(context.Background(), nil, "acme-financial"); err == nil {
		t.Fatal("expected error for nil client")
	}
}

// TestGrantsReadFailClosed: an unknown/empty permission string must NOT
// produce a viewer grant.
func TestGrantsReadFailClosed(t *testing.T) {
	if grantsRead("") || grantsRead("none") || grantsRead("bogus") {
		t.Fatal("unknown permission must not grant read (fail closed)")
	}
	for _, p := range []string{"pull", "triage", "push", "maintain", "admin"} {
		if !grantsRead(p) {
			t.Fatalf("%q should grant read", p)
		}
	}
}
