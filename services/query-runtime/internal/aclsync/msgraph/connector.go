package msgraph

import (
	"context"
	"log/slog"
	"time"

	"groundwork/query-runtime/internal/aclsync"
)

// Connector implements aclsync.Connector against Microsoft Graph (Entra + SharePoint).
// It feeds OpenFGA via the Syncer; it never touches the query engine, auth, or identity.
type Connector struct {
	client GraphClient
	cfg    Config
	logger *slog.Logger
	delta  DeltaTokenStore
}

// NewConnector builds a Graph connector from a GraphClient (real or fake) and config.
func NewConnector(client GraphClient, cfg Config, logger *slog.Logger, delta DeltaTokenStore) *Connector {
	if logger == nil {
		logger = slog.Default()
	}
	if delta == nil {
		delta = NewMemoryDeltaTokenStore()
	}
	return &Connector{client: client, cfg: cfg.withDefaults(), logger: logger, delta: delta}
}

// Snapshot reads the full Entra + SharePoint permission state and maps it to aclsync.
// Any Graph error is returned (never a partial/empty snapshot), so the Syncer's
// destructive-delete guard prevents wiping OpenFGA on a Graph outage.
func (c *Connector) Snapshot(ctx context.Context, tenantID string) (aclsync.PermissionSet, error) {
	users, err := c.client.ListUsers(ctx)
	if err != nil {
		return aclsync.PermissionSet{}, err
	}
	groups, err := c.client.ListGroups(ctx)
	if err != nil {
		return aclsync.PermissionSet{}, err
	}

	ps := aclsync.PermissionSet{TenantID: tenantID, Users: mapUsers(users)}
	byID := userKeyByID(users)

	for _, g := range groups {
		members, err := c.client.ListGroupMembers(ctx, g.ID)
		if err != nil {
			return aclsync.PermissionSet{}, err
		}
		ps.Groups = append(ps.Groups, mapGroup(g, members))
	}

	items, err := c.client.ListDriveItems(ctx)
	if err != nil {
		return aclsync.PermissionSet{}, err
	}
	for _, it := range items {
		perms, err := c.client.ListItemPermissions(ctx, it.ID)
		if err != nil {
			return aclsync.PermissionSet{}, err
		}
		if it.IsFolder {
			ps.Folders = append(ps.Folders, mapFolder(it, perms, byID))
		} else {
			ps.Documents = append(ps.Documents, mapDocument(it, perms, byID))
		}
	}
	return ps, nil
}

func (c *Connector) ListDocuments(ctx context.Context, _ string) ([]aclsync.Document, error) {
	byID, err := c.userIndex(ctx)
	if err != nil {
		return nil, err
	}
	items, err := c.client.ListDriveItems(ctx)
	if err != nil {
		return nil, err
	}
	var docs []aclsync.Document
	for _, it := range items {
		if it.IsFolder {
			continue
		}
		perms, err := c.client.ListItemPermissions(ctx, it.ID)
		if err != nil {
			return nil, err
		}
		docs = append(docs, mapDocument(it, perms, byID))
	}
	return docs, nil
}

func (c *Connector) GetDocumentPermissions(ctx context.Context, _ string, documentID string) (aclsync.DocumentPermissions, error) {
	byID, err := c.userIndex(ctx)
	if err != nil {
		return aclsync.DocumentPermissions{}, err
	}
	items, err := c.client.ListDriveItems(ctx)
	if err != nil {
		return aclsync.DocumentPermissions{}, err
	}
	for _, it := range items {
		if it.ID != documentID || it.IsFolder {
			continue
		}
		perms, err := c.client.ListItemPermissions(ctx, it.ID)
		if err != nil {
			return aclsync.DocumentPermissions{}, err
		}
		users, groups := granteesFromPerms(perms, byID)
		return aclsync.DocumentPermissions{DocumentID: it.ID, FolderID: it.ParentID, ViewerUsers: users, ViewerGroups: groups}, nil
	}
	return aclsync.DocumentPermissions{}, nil
}

// WatchPermissionChanges starts delta-based change detection. This milestone wires the
// delta plumbing (poll + durable token) and logs detected changes; correctness comes from
// the Service's periodic full reconcile, which this detection accelerates. Granular
// revoke-event streaming on the channel is the documented next step (see revokeChange).
func (c *Connector) WatchPermissionChanges(ctx context.Context, tenantID string) (<-chan aclsync.PermissionChange, error) {
	ch := make(chan aclsync.PermissionChange)
	go func() {
		defer close(ch)
		ticker := time.NewTicker(time.Duration(c.cfg.DeltaPollSeconds) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.pollDelta(ctx, tenantID)
			}
		}
	}()
	return ch, nil
}

func (c *Connector) pollDelta(ctx context.Context, tenantID string) {
	token, _ := c.delta.Load(ctx, tenantID)
	items, next, err := c.client.DeltaDriveItems(ctx, token)
	if err != nil {
		c.logger.Warn("msgraph_delta_failed", "tenant", tenantID, "err", err.Error())
		return
	}
	deleted, changed := classifyDelta(items)
	c.logger.Info("msgraph_delta_detected", "tenant", tenantID, "changed", len(changed), "deleted", len(deleted))
	if next != "" {
		if err := c.delta.Save(ctx, tenantID, next); err != nil {
			c.logger.Warn("msgraph_delta_token_save_failed", "tenant", tenantID, "err", err.Error())
		}
	}
}

func (c *Connector) userIndex(ctx context.Context) (map[string]string, error) {
	users, err := c.client.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	return userKeyByID(users), nil
}

var _ aclsync.Connector = (*Connector)(nil)
