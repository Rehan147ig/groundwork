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

// httpClient is the live GitHub REST API implementation of Client. It is
// read-only: every call is a GET, and the PAT requires only read:org +
// repo (read) scopes. Exercised against real GitHub in integration, not in
// unit tests (same convention as msgraph's httpGraphClient). The token is
// never logged.
type httpClient struct {
	org   string
	token string
	base  string // e.g. https://api.github.com (overridable for GH Enterprise)
	http  *http.Client
}

// NewHTTPClient builds a live GitHub connector client for an org. baseURL
// is optional; pass "" for github.com. The token is a PAT (classic or
// fine-grained) with read:org and repository read access.
func NewHTTPClient(org, token, baseURL string) Client {
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	return &httpClient{
		org:   org,
		token: token,
		base:  strings.TrimRight(baseURL, "/"),
		http:  &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *httpClient) ListTeams(ctx context.Context) ([]Team, error) {
	var out []Team
	err := c.paginate(ctx, fmt.Sprintf("/orgs/%s/teams", c.org), func(page []byte) error {
		var raw []struct {
			Slug string `json:"slug"`
			Name string `json:"name"`
		}
		if err := json.Unmarshal(page, &raw); err != nil {
			return err
		}
		for _, t := range raw {
			out = append(out, Team{Slug: t.Slug, Name: t.Name})
		}
		return nil
	})
	return out, err
}

func (c *httpClient) ListTeamMembers(ctx context.Context, teamSlug string) ([]string, error) {
	var out []string
	err := c.paginate(ctx, fmt.Sprintf("/orgs/%s/teams/%s/members", c.org, teamSlug), func(page []byte) error {
		var raw []struct {
			Login string `json:"login"`
		}
		if err := json.Unmarshal(page, &raw); err != nil {
			return err
		}
		for _, m := range raw {
			out = append(out, m.Login)
		}
		return nil
	})
	return out, err
}

func (c *httpClient) ListRepos(ctx context.Context) ([]Repo, error) {
	var out []Repo
	err := c.paginate(ctx, fmt.Sprintf("/orgs/%s/repos", c.org), func(page []byte) error {
		var raw []struct {
			Name    string `json:"name"`
			Private bool   `json:"private"`
		}
		if err := json.Unmarshal(page, &raw); err != nil {
			return err
		}
		for _, r := range raw {
			out = append(out, Repo{Name: r.Name, Private: r.Private})
		}
		return nil
	})
	return out, err
}

func (c *httpClient) ListRepoTeams(ctx context.Context, repo string) ([]RepoTeamAccess, error) {
	var out []RepoTeamAccess
	err := c.paginate(ctx, fmt.Sprintf("/repos/%s/%s/teams", c.org, repo), func(page []byte) error {
		var raw []struct {
			Slug       string `json:"slug"`
			Permission string `json:"permission"`
		}
		if err := json.Unmarshal(page, &raw); err != nil {
			return err
		}
		for _, t := range raw {
			out = append(out, RepoTeamAccess{TeamSlug: t.Slug, Permission: t.Permission})
		}
		return nil
	})
	return out, err
}

// paginate walks the GitHub REST Link-header pagination, calling fn with
// each page's raw JSON body. per_page is maxed at 100.
func (c *httpClient) paginate(ctx context.Context, path string, fn func([]byte) error) error {
	next := c.base + path + "?per_page=100"
	for next != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, next, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

		resp, err := c.http.Do(req)
		if err != nil {
			return err
		}
		body, readErr := readAllAndClose(resp)
		if readErr != nil {
			return readErr
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return fmt.Errorf("github auth/permission error (%d) on %s — check PAT read:org/repo scopes", resp.StatusCode, path)
		}
		if resp.StatusCode >= 300 {
			return fmt.Errorf("github GET %s -> %d", path, resp.StatusCode)
		}
		if err := fn(body); err != nil {
			return err
		}
		next = nextLink(resp.Header.Get("Link"))
	}
	return nil
}

// nextLink extracts the rel="next" URL from a GitHub Link header, or "".
func nextLink(linkHeader string) string {
	if linkHeader == "" {
		return ""
	}
	for _, part := range strings.Split(linkHeader, ",") {
		segs := strings.Split(strings.TrimSpace(part), ";")
		if len(segs) < 2 {
			continue
		}
		if strings.Contains(segs[1], `rel="next"`) {
			u := strings.TrimSpace(segs[0])
			u = strings.TrimPrefix(u, "<")
			u = strings.TrimSuffix(u, ">")
			if _, err := url.Parse(u); err == nil {
				return u
			}
		}
	}
	return ""
}
