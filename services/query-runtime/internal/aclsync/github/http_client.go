package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// HTTPClient implements GitHubClient by talking to api.github.com.
type HTTPClient struct {
	client *http.Client
	token  string
}

// NewHTTPClient creates a live GitHub API client.
func NewHTTPClient(token string) *HTTPClient {
	return &HTTPClient{
		client: &http.Client{Timeout: 15 * time.Second},
		token:  token,
	}
}

func (c *HTTPClient) doGET(ctx context.Context, path string, out any) error {
	endpoint := "https://api.github.com" + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("github API error: %s returned %s", path, resp.Status)
	}

	return json.NewDecoder(resp.Body).Decode(out)
}

// getPaginated follows the Link header for pagination.
// For the demo, we just get up to 100 per page and assume one page is enough for the demo org.
// A full enterprise connector would follow the 'next' link.
func (c *HTTPClient) doGETPaginated(ctx context.Context, path string, out any) error {
	p := path
	if strings.Contains(p, "?") {
		p += "&per_page=100"
	} else {
		p += "?per_page=100"
	}
	return c.doGET(ctx, p, out)
}

func (c *HTTPClient) ListOrgMembers(ctx context.Context, org string) ([]GitHubUser, error) {
	var users []GitHubUser
	path := fmt.Sprintf("/orgs/%s/members", url.PathEscape(org))
	if err := c.doGETPaginated(ctx, path, &users); err != nil {
		return nil, err
	}
	return users, nil
}

func (c *HTTPClient) ListTeams(ctx context.Context, org string) ([]GitHubTeam, error) {
	var teams []GitHubTeam
	path := fmt.Sprintf("/orgs/%s/teams", url.PathEscape(org))
	if err := c.doGETPaginated(ctx, path, &teams); err != nil {
		return nil, err
	}
	return teams, nil
}

func (c *HTTPClient) ListTeamMembers(ctx context.Context, org string, teamSlug string) ([]GitHubUser, error) {
	var users []GitHubUser
	path := fmt.Sprintf("/orgs/%s/teams/%s/members", url.PathEscape(org), url.PathEscape(teamSlug))
	if err := c.doGETPaginated(ctx, path, &users); err != nil {
		return nil, err
	}
	return users, nil
}

func (c *HTTPClient) ListTeamRepos(ctx context.Context, org string, teamSlug string) ([]GitHubTeamRepo, error) {
	var repos []GitHubTeamRepo
	path := fmt.Sprintf("/orgs/%s/teams/%s/repos", url.PathEscape(org), url.PathEscape(teamSlug))
	if err := c.doGETPaginated(ctx, path, &repos); err != nil {
		return nil, err
	}
	return repos, nil
}

func (c *HTTPClient) ListRepos(ctx context.Context, org string) ([]GitHubRepo, error) {
	var repos []GitHubRepo
	path := fmt.Sprintf("/orgs/%s/repos", url.PathEscape(org))
	if err := c.doGETPaginated(ctx, path, &repos); err != nil {
		return nil, err
	}
	return repos, nil
}

var _ GitHubClient = (*HTTPClient)(nil)
