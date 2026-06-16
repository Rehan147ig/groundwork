package github

import (
	"context"
	"log/slog"

	"groundwork/query-runtime/internal/aclsync"
)

type Connector struct {
	client GitHubClient
	org    string
	logger *slog.Logger
}

func NewConnector(client GitHubClient, org string, logger *slog.Logger) *Connector {
	if logger == nil {
		logger = slog.Default()
	}
	return &Connector{client: client, org: org, logger: logger}
}

func (c *Connector) Snapshot(ctx context.Context, tenantID string) (aclsync.PermissionSet, error) {
	users, err := c.client.ListOrgMembers(ctx, c.org)
	if err != nil {
		return aclsync.PermissionSet{}, err
	}

	teams, err := c.client.ListTeams(ctx, c.org)
	if err != nil {
		return aclsync.PermissionSet{}, err
	}

	ps := aclsync.PermissionSet{
		TenantID: tenantID,
		Users:    mapUsers(users),
	}

	// Map repo name to the teams that have access
	repoGrants := make(map[string][]string)

	for _, t := range teams {
		members, err := c.client.ListTeamMembers(ctx, c.org, t.Slug)
		if err != nil {
			return aclsync.PermissionSet{}, err
		}
		ps.Groups = append(ps.Groups, mapTeamToGroup(t, members))

		repos, err := c.client.ListTeamRepos(ctx, c.org, t.Slug)
		if err != nil {
			return aclsync.PermissionSet{}, err
		}
		for _, r := range repos {
			if grantsRead(r.Permissions) {
				repoGrants[r.Name] = append(repoGrants[r.Name], teamKey(t.Slug))
			}
		}
	}

	allRepos, err := c.client.ListRepos(ctx, c.org)
	if err != nil {
		return aclsync.PermissionSet{}, err
	}

	for _, r := range allRepos {
		grants := repoGrants[r.Name]
		ps.Documents = append(ps.Documents, mapRepoToDocument(r, grants))
	}

	return ps, nil
}

func (c *Connector) ListDocuments(ctx context.Context, tenantID string) ([]aclsync.Document, error) {
	ps, err := c.Snapshot(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	return ps.Documents, nil
}

func (c *Connector) GetDocumentPermissions(ctx context.Context, tenantID string, documentID string) (aclsync.DocumentPermissions, error) {
	ps, err := c.Snapshot(ctx, tenantID)
	if err != nil {
		return aclsync.DocumentPermissions{}, err
	}
	for _, doc := range ps.Documents {
		if doc.ID == documentID {
			return aclsync.DocumentPermissions{
				DocumentID:   doc.ID,
				FolderID:     doc.FolderID,
				ViewerUsers:  doc.ViewerUsers,
				ViewerGroups: doc.ViewerGroups,
			}, nil
		}
	}
	return aclsync.DocumentPermissions{}, nil
}

func (c *Connector) WatchPermissionChanges(ctx context.Context, tenantID string) (<-chan aclsync.PermissionChange, error) {
	// Not implemented for GitHub free tier (no webhooks for team membership)
	// Relies on periodic full reconcile
	return make(chan aclsync.PermissionChange), nil
}

var _ aclsync.Connector = (*Connector)(nil)
