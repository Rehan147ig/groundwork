// Package msgraph implements the first real enterprise connector for Groundwork ACL
// sync: it reads Microsoft Entra users/groups and SharePoint folder/file permissions via
// Microsoft Graph and maps them onto the aclsync domain model, which the Syncer then
// reconciles into OpenFGA. It implements aclsync.Connector and feeds OpenFGA only — it
// does not touch the query engine, auth, or identity.
package msgraph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Config holds Microsoft Graph connection settings (from MS_GRAPH_* env).
type Config struct {
	TenantID         string // Entra tenant (auth)
	ClientID         string
	ClientSecret     string
	SiteID           string
	DriveID          string
	AuthorityHost    string        // default https://login.microsoftonline.com
	GraphBaseURL     string        // default https://graph.microsoft.com/v1.0
	DeltaPollSeconds int           // watch poll cadence (default 60)
	Enabled          bool          // MS_GRAPH_CONNECTOR_ENABLED
	HTTPTimeout      time.Duration // default 15s
}

func (c Config) withDefaults() Config {
	if c.AuthorityHost == "" {
		c.AuthorityHost = "https://login.microsoftonline.com"
	}
	if c.GraphBaseURL == "" {
		c.GraphBaseURL = "https://graph.microsoft.com/v1.0"
	}
	if c.DeltaPollSeconds <= 0 {
		c.DeltaPollSeconds = 60
	}
	if c.HTTPTimeout <= 0 {
		c.HTTPTimeout = 15 * time.Second
	}
	c.AuthorityHost = strings.TrimRight(c.AuthorityHost, "/")
	c.GraphBaseURL = strings.TrimRight(c.GraphBaseURL, "/")
	return c
}

// ErrAuthFailed marks a permanent authentication failure (bad credentials / forbidden).
// It is NOT retried and it propagates up so the sync fails safely (no destructive delete).
var ErrAuthFailed = errors.New("microsoft graph authentication failed")

// --- Graph DTOs (only the fields we use) ---

type GraphUser struct {
	ID                string
	DisplayName       string
	Mail              string
	UserPrincipalName string
}

type GraphGroup struct {
	ID          string
	DisplayName string
}

type MemberType string

const (
	MemberUser  MemberType = "user"
	MemberGroup MemberType = "group"
)

type GraphMember struct {
	ID                string
	DisplayName       string
	Mail              string
	UserPrincipalName string
	Type              MemberType
}

type GraphDriveItem struct {
	ID       string
	Name     string
	ParentID string
	IsFolder bool
}

// GraphGrantee is who a SharePoint permission is granted to (a user or a group).
type GraphGrantee struct {
	UserID   string
	UserUPN  string
	UserMail string
	GroupID  string
}

type GraphPermission struct {
	ID      string
	Roles   []string
	Grantee GraphGrantee
}

type GraphDeltaItem struct {
	ID       string
	Name     string
	ParentID string
	IsFolder bool
	Deleted  bool
}

// GraphClient is the Microsoft Graph surface the connector needs. The real
// implementation talks HTTP to Graph; tests inject a fake (live creds aren't available).
type GraphClient interface {
	ListUsers(ctx context.Context) ([]GraphUser, error)
	ListGroups(ctx context.Context) ([]GraphGroup, error)
	ListGroupMembers(ctx context.Context, groupID string) ([]GraphMember, error)
	ListDriveItems(ctx context.Context) ([]GraphDriveItem, error)
	ListItemPermissions(ctx context.Context, itemID string) ([]GraphPermission, error)
	DeltaDriveItems(ctx context.Context, token string) (items []GraphDeltaItem, nextToken string, err error)
}

// --- HTTP implementation (OAuth2 client credentials + Graph REST) ---

type httpGraphClient struct {
	cfg    Config
	http   *http.Client
	tokens *tokenSource
}

// NewHTTPGraphClient builds a live Graph client. It is exercised against real Graph in
// integration (not in unit tests). Secrets and tokens are never logged.
func NewHTTPGraphClient(cfg Config) *httpGraphClient {
	cfg = cfg.withDefaults()
	client := &http.Client{Timeout: cfg.HTTPTimeout}
	return &httpGraphClient{
		cfg:    cfg,
		http:   client,
		tokens: &tokenSource{cfg: cfg, http: client},
	}
}

type tokenSource struct {
	cfg    Config
	http   *http.Client
	mu     sync.Mutex
	token  string
	expiry time.Time
}

// get returns a cached token or fetches a new one via the client-credentials flow.
func (t *tokenSource) get(ctx context.Context) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.token != "" && time.Now().Before(t.expiry.Add(-time.Minute)) {
		return t.token, nil
	}
	endpoint := fmt.Sprintf("%s/%s/oauth2/v2.0/token", t.cfg.AuthorityHost, t.cfg.TenantID)
	form := url.Values{}
	form.Set("client_id", t.cfg.ClientID)
	form.Set("client_secret", t.cfg.ClientSecret)
	form.Set("grant_type", "client_credentials")
	form.Set("scope", "https://graph.microsoft.com/.default")

	var parsed struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
	}
	err := retry(ctx, 5, func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := t.http.Do(req)
		if err != nil {
			return err // network — retryable
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 500 || resp.StatusCode == 429 {
			return fmt.Errorf("token endpoint status %d", resp.StatusCode) // transient — retry
		}
		_ = json.NewDecoder(resp.Body).Decode(&parsed)
		if resp.StatusCode >= 300 || parsed.AccessToken == "" {
			// Permanent (bad creds). Include only the non-sensitive error code; never the secret.
			return fmt.Errorf("%w: %s", ErrAuthFailed, parsed.Error)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	t.token = parsed.AccessToken
	t.expiry = time.Now().Add(time.Duration(parsed.ExpiresIn) * time.Second)
	return t.token, nil
}

func (g *httpGraphClient) doGet(ctx context.Context, fullURL string, out any) error {
	return retry(ctx, 5, func() error {
		tok, err := g.tokens.get(ctx)
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := g.http.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		switch {
		case resp.StatusCode == 401 || resp.StatusCode == 403:
			return fmt.Errorf("%w: graph status %d", ErrAuthFailed, resp.StatusCode)
		case resp.StatusCode >= 500 || resp.StatusCode == 429:
			return fmt.Errorf("graph status %d", resp.StatusCode) // transient
		case resp.StatusCode >= 300:
			return fmt.Errorf("graph GET %s: status %d", redactURL(fullURL), resp.StatusCode)
		}
		if out == nil {
			return nil
		}
		return json.NewDecoder(resp.Body).Decode(out)
	})
}

// getPaged follows @odata.nextLink, invoking each on every value element.
func (g *httpGraphClient) getPaged(ctx context.Context, startURL string, each func(json.RawMessage) error) error {
	next := startURL
	for next != "" {
		var page struct {
			Value    []json.RawMessage `json:"value"`
			NextLink string            `json:"@odata.nextLink"`
		}
		if err := g.doGet(ctx, next, &page); err != nil {
			return err
		}
		for _, raw := range page.Value {
			if err := each(raw); err != nil {
				return err
			}
		}
		next = page.NextLink
	}
	return nil
}

func (g *httpGraphClient) ListUsers(ctx context.Context) ([]GraphUser, error) {
	var out []GraphUser
	err := g.getPaged(ctx, g.cfg.GraphBaseURL+"/users?$select=id,displayName,mail,userPrincipalName&$top=100", func(raw json.RawMessage) error {
		var u GraphUser
		if err := json.Unmarshal(raw, &u); err != nil {
			return err
		}
		out = append(out, u)
		return nil
	})
	return out, err
}

func (g *httpGraphClient) ListGroups(ctx context.Context) ([]GraphGroup, error) {
	var out []GraphGroup
	err := g.getPaged(ctx, g.cfg.GraphBaseURL+"/groups?$select=id,displayName&$top=100", func(raw json.RawMessage) error {
		var grp GraphGroup
		if err := json.Unmarshal(raw, &grp); err != nil {
			return err
		}
		out = append(out, grp)
		return nil
	})
	return out, err
}

func (g *httpGraphClient) ListGroupMembers(ctx context.Context, groupID string) ([]GraphMember, error) {
	var out []GraphMember
	u := fmt.Sprintf("%s/groups/%s/members?$select=id,displayName,mail,userPrincipalName&$top=100", g.cfg.GraphBaseURL, url.PathEscape(groupID))
	err := g.getPaged(ctx, u, func(raw json.RawMessage) error {
		var m struct {
			OType             string `json:"@odata.type"`
			ID                string `json:"id"`
			DisplayName       string `json:"displayName"`
			Mail              string `json:"mail"`
			UserPrincipalName string `json:"userPrincipalName"`
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			return err
		}
		mt := MemberUser
		if strings.Contains(strings.ToLower(m.OType), ".group") {
			mt = MemberGroup
		}
		out = append(out, GraphMember{ID: m.ID, DisplayName: m.DisplayName, Mail: m.Mail, UserPrincipalName: m.UserPrincipalName, Type: mt})
		return nil
	})
	return out, err
}

type driveItemJSON struct {
	ID              string           `json:"id"`
	Name            string           `json:"name"`
	Folder          *json.RawMessage `json:"folder"`
	ParentReference struct {
		ID string `json:"id"`
	} `json:"parentReference"`
}

func (g *httpGraphClient) ListDriveItems(ctx context.Context) ([]GraphDriveItem, error) {
	var items []GraphDriveItem
	var walk func(childrenURL, parentID string) error
	walk = func(childrenURL, parentID string) error {
		return g.getPaged(ctx, childrenURL, func(raw json.RawMessage) error {
			var it driveItemJSON
			if err := json.Unmarshal(raw, &it); err != nil {
				return err
			}
			isFolder := it.Folder != nil
			items = append(items, GraphDriveItem{ID: it.ID, Name: it.Name, ParentID: parentID, IsFolder: isFolder})
			if isFolder {
				child := fmt.Sprintf("%s/drives/%s/items/%s/children?$top=200", g.cfg.GraphBaseURL, url.PathEscape(g.cfg.DriveID), url.PathEscape(it.ID))
				return walk(child, it.ID)
			}
			return nil
		})
	}
	root := fmt.Sprintf("%s/drives/%s/root/children?$top=200", g.cfg.GraphBaseURL, url.PathEscape(g.cfg.DriveID))
	if err := walk(root, ""); err != nil {
		return nil, err
	}
	return items, nil
}

func (g *httpGraphClient) ListItemPermissions(ctx context.Context, itemID string) ([]GraphPermission, error) {
	var out []GraphPermission
	u := fmt.Sprintf("%s/drives/%s/items/%s/permissions", g.cfg.GraphBaseURL, url.PathEscape(g.cfg.DriveID), url.PathEscape(itemID))
	err := g.getPaged(ctx, u, func(raw json.RawMessage) error {
		var p struct {
			ID    string   `json:"id"`
			Roles []string `json:"roles"`
			Grant struct {
				User  *struct{ ID, DisplayName, Email string } `json:"user"`
				Group *struct{ ID, DisplayName string }        `json:"group"`
			} `json:"grantedToV2"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		perm := GraphPermission{ID: p.ID, Roles: p.Roles}
		if p.Grant.Group != nil {
			perm.Grantee.GroupID = p.Grant.Group.ID
		} else if p.Grant.User != nil {
			perm.Grantee.UserID = p.Grant.User.ID
			perm.Grantee.UserMail = p.Grant.User.Email
		}
		out = append(out, perm)
		return nil
	})
	return out, err
}

func (g *httpGraphClient) DeltaDriveItems(ctx context.Context, token string) ([]GraphDeltaItem, string, error) {
	start := token
	if start == "" {
		start = fmt.Sprintf("%s/drives/%s/root/delta", g.cfg.GraphBaseURL, url.PathEscape(g.cfg.DriveID))
	}
	var items []GraphDeltaItem
	deltaLink := ""
	next := start
	for next != "" {
		var page struct {
			Value []struct {
				ID              string           `json:"id"`
				Name            string           `json:"name"`
				Folder          *json.RawMessage `json:"folder"`
				Deleted         *json.RawMessage `json:"deleted"`
				ParentReference struct {
					ID string `json:"id"`
				} `json:"parentReference"`
			} `json:"value"`
			NextLink  string `json:"@odata.nextLink"`
			DeltaLink string `json:"@odata.deltaLink"`
		}
		if err := g.doGet(ctx, next, &page); err != nil {
			return nil, "", err
		}
		for _, it := range page.Value {
			items = append(items, GraphDeltaItem{
				ID: it.ID, Name: it.Name, ParentID: it.ParentReference.ID,
				IsFolder: it.Folder != nil, Deleted: it.Deleted != nil,
			})
		}
		if page.NextLink != "" {
			next = page.NextLink
			continue
		}
		deltaLink = page.DeltaLink
		next = ""
	}
	return items, deltaLink, nil
}

// --- retry / backoff (transient only; ErrAuthFailed is permanent) ---

func retry(ctx context.Context, attempts int, op func() error) error {
	var err error
	for i := 0; i < attempts; i++ {
		if err = op(); err == nil {
			return nil
		}
		if errors.Is(err, ErrAuthFailed) {
			return err // permanent — fail safely, do not hammer
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff(i)):
		}
	}
	return err
}

func backoff(attempt int) time.Duration {
	base := 300 * time.Millisecond
	d := base
	for i := 0; i < attempt && d < 5*time.Second; i++ {
		d *= 2
	}
	if d > 5*time.Second {
		d = 5 * time.Second
	}
	half := d / 2
	return half + time.Duration(rand.Int63n(int64(half)+1))
}

// redactURL strips the query string so logs never carry tokens/links with secrets.
func redactURL(u string) string {
	if i := strings.IndexByte(u, '?'); i >= 0 {
		return u[:i]
	}
	return u
}

var _ GraphClient = (*httpGraphClient)(nil)
