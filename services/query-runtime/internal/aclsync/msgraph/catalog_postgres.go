package msgraph

import (
	"context"
	"database/sql"
)

// PostgresCatalogWriter persists directory data into the msgraph.* schema
// created by migrations 008 and 010. Every method is a single INSERT … ON
// CONFLICT … DO UPDATE, which is the upsert pattern Postgres provides as
// one atomic statement. That gives the writer two important properties for
// PR #19's "directory enumeration" flow:
//
//  1. Idempotency. Re-running the connector against an unchanged directory
//     produces zero new rows; existing rows have their last_seen_at and
//     updated_at refreshed.
//
//  2. No transaction is required around the whole enumeration. Each upsert
//     is atomic; if the connector dies halfway through, the catalog
//     converges on the next run. Snapshot-consistent semantics are
//     intentionally out of scope for PR #19.
//
// gw_canonical_id is filled with the pendingCanonicalID placeholder
// ("entra:<oid>") because PR #19 does not invoke the runtime's
// PrincipalResolver. PR #20+ replaces these placeholders with real
// canonical UUIDs via a background pass over the catalog.
type PostgresCatalogWriter struct {
	db *sql.DB
}

// NewPostgresCatalogWriter wraps an *sql.DB. The DB must point at the
// Groundwork Postgres database where migrations 003–010 have been applied
// (the db-migrate compose service ensures this on every `docker compose up`).
func NewPostgresCatalogWriter(db *sql.DB) *PostgresCatalogWriter {
	return &PostgresCatalogWriter{db: db}
}

func (w *PostgresCatalogWriter) UpsertPrincipal(ctx context.Context, tenantID string, p Principal) error {
	// PR #19 follow-up: COALESCE(EXCLUDED.x, msgraph.principals.x) preserves
	// the previously-stored value when Graph returns NULL/empty for a field
	// on a re-sync. Without this, a service account whose mail attribute is
	// briefly omitted on one /users page would have its previously-stored
	// email clobbered to NULL. account_enabled and gw_canonical_id are not
	// wrapped: both are non-nullable in the column and authoritative on
	// every read from Graph.
	const q = `
INSERT INTO msgraph.principals (
    tenant_id, entra_oid, gw_canonical_id, upn, email, display_name,
    account_enabled, last_seen_at, attributes, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, NOW(), '{}'::jsonb, NOW())
ON CONFLICT (tenant_id, entra_oid) DO UPDATE SET
    upn             = COALESCE(EXCLUDED.upn,          msgraph.principals.upn),
    email           = COALESCE(EXCLUDED.email,        msgraph.principals.email),
    display_name    = COALESCE(EXCLUDED.display_name, msgraph.principals.display_name),
    account_enabled = EXCLUDED.account_enabled,
    last_seen_at    = NOW(),
    updated_at      = NOW()
`
	_, err := w.db.ExecContext(ctx, q,
		tenantID, p.EntraOID, pendingCanonicalID(p.EntraOID),
		nullableText(p.UPN), nullableText(p.Email), nullableText(p.DisplayName),
		p.AccountEnabled,
	)
	return err
}

func (w *PostgresCatalogWriter) UpsertGroup(ctx context.Context, tenantID string, g Group) error {
	const q = `
INSERT INTO msgraph.groups (
    tenant_id, entra_group_id, display_name, group_type,
    last_seen_at, updated_at
)
VALUES ($1, $2, $3, $4, NOW(), NOW())
ON CONFLICT (tenant_id, entra_group_id) DO UPDATE SET
    display_name = COALESCE(EXCLUDED.display_name, msgraph.groups.display_name),
    group_type   = COALESCE(EXCLUDED.group_type,   msgraph.groups.group_type),
    last_seen_at = NOW(),
    updated_at   = NOW()
`
	_, err := w.db.ExecContext(ctx, q,
		tenantID, g.EntraGroupID,
		nullableText(g.DisplayName), nullableText(g.GroupType),
	)
	return err
}

func (w *PostgresCatalogWriter) UpsertMembership(ctx context.Context, tenantID string, m Membership) error {
	const q = `
INSERT INTO msgraph.group_memberships (
    tenant_id, group_id, member_id, member_type,
    last_seen_at, updated_at
)
VALUES ($1, $2, $3, $4, NOW(), NOW())
ON CONFLICT (tenant_id, group_id, member_id, member_type) DO UPDATE SET
    last_seen_at = NOW(),
    updated_at   = NOW()
`
	_, err := w.db.ExecContext(ctx, q, tenantID, m.GroupID, m.MemberID, m.MemberType)
	return err
}

func (w *PostgresCatalogWriter) PrincipalCount(ctx context.Context, tenantID string) (int, error) {
	return w.scalarCount(ctx, `SELECT count(*) FROM msgraph.principals WHERE tenant_id = $1`, tenantID)
}

func (w *PostgresCatalogWriter) GroupCount(ctx context.Context, tenantID string) (int, error) {
	return w.scalarCount(ctx, `SELECT count(*) FROM msgraph.groups WHERE tenant_id = $1`, tenantID)
}

func (w *PostgresCatalogWriter) MembershipCount(ctx context.Context, tenantID string) (int, error) {
	return w.scalarCount(ctx, `SELECT count(*) FROM msgraph.group_memberships WHERE tenant_id = $1`, tenantID)
}

func (w *PostgresCatalogWriter) scalarCount(ctx context.Context, query string, args ...any) (int, error) {
	var n int
	if err := w.db.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// nullableText returns a sql.NullString that maps an empty Go string to SQL
// NULL. Microsoft Graph sometimes returns "" for fields like mail (e.g. for
// service accounts); storing NULL is cleaner than storing the empty string.
func nullableText(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

var _ CatalogWriter = (*PostgresCatalogWriter)(nil)
