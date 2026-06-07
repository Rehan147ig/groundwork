package msgraph

import "context"

// Principal is one directory user as observed in the source system, before
// canonical resolution. Maps to msgraph.principals (one row per (tenant_id,
// entra_oid)).
type Principal struct {
	EntraOID       string // Entra Object ID — the source-system principal id
	UPN            string // userPrincipalName
	Email          string // mail (may be empty for guest accounts)
	DisplayName    string
	AccountEnabled bool
}

// Group is one directory group as observed in the source system.
type Group struct {
	EntraGroupID string
	DisplayName  string
	GroupType    string // "security" | "m365" | "" (best-effort; not always known from /groups)
}

// Membership is one (group, member) edge. member_type='user' for directory
// users, 'group' for nested-group membership.
type Membership struct {
	GroupID    string // entra_group_id of the parent group
	MemberID   string // entra_oid (user) or entra_group_id (nested group)
	MemberType string // "user" or "group"
}

// CatalogWriter persists the connector's view of the directory. Implementations
// must be idempotent: multiple runs against the same directory produce no
// duplicate rows. The Postgres implementation uses ON CONFLICT … DO UPDATE on
// composite primary keys; the in-memory implementation is overwrite-by-key.
//
// CatalogWriter is intentionally narrow. It does NOT write OpenFGA tuples and
// it does NOT trigger any runtime authorization side effects. PR #20+ adds the
// OpenFGA sink, which is a separate write target.
type CatalogWriter interface {
	UpsertPrincipal(ctx context.Context, tenantID string, p Principal) error
	UpsertGroup(ctx context.Context, tenantID string, g Group) error
	UpsertMembership(ctx context.Context, tenantID string, m Membership) error

	PrincipalCount(ctx context.Context, tenantID string) (int, error)
	GroupCount(ctx context.Context, tenantID string) (int, error)
	MembershipCount(ctx context.Context, tenantID string) (int, error)
}

// pendingCanonicalID returns the placeholder value stored in
// msgraph.principals.gw_canonical_id during PR #19 (visibility-only).
//
// The runtime's PrincipalResolver (internal/runtime/principal.go) produces
// real canonical IDs (UUID-style "principal:cmq…"). PR #19 does not invoke
// the resolver — it captures the directory only — so the catalog stores a
// stable, deterministic alias-key as the canonical id placeholder. The
// "entra:" prefix lets a later background job (PR #20+) distinguish
// unresolved rows from resolved ones in a single SQL predicate:
//
//	WHERE gw_canonical_id LIKE 'entra:%'
//
// This avoids loosening the NOT NULL constraint from migration 008.
func pendingCanonicalID(entraOID string) string {
	return "entra:" + entraOID
}
