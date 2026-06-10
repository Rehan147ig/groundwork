package msgraph

import (
	"context"
	"sync"
)

// InMemoryCatalogWriter is a thread-safe overwrite-by-key implementation of
// CatalogWriter. Used by unit tests and any operator command that wants to
// dry-run the connector without touching Postgres.
//
// Idempotency is structural: each entity is keyed by its composite primary
// key, and a second Upsert with the same key replaces the prior value
// without growing the underlying map.
type InMemoryCatalogWriter struct {
	mu sync.Mutex
	// tenant_id → entra_oid → Principal
	principals map[string]map[string]Principal
	// tenant_id → entra_group_id → Group
	groups map[string]map[string]Group
	// tenant_id → membershipKey(group_id, member_id, member_type) → Membership
	memberships map[string]map[string]Membership
}

func NewInMemoryCatalogWriter() *InMemoryCatalogWriter {
	return &InMemoryCatalogWriter{
		principals:  map[string]map[string]Principal{},
		groups:      map[string]map[string]Group{},
		memberships: map[string]map[string]Membership{},
	}
}

func (w *InMemoryCatalogWriter) UpsertPrincipal(_ context.Context, tenantID string, p Principal) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.principals[tenantID] == nil {
		w.principals[tenantID] = map[string]Principal{}
	}
	w.principals[tenantID][p.EntraOID] = p
	return nil
}

func (w *InMemoryCatalogWriter) UpsertGroup(_ context.Context, tenantID string, g Group) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.groups[tenantID] == nil {
		w.groups[tenantID] = map[string]Group{}
	}
	w.groups[tenantID][g.EntraGroupID] = g
	return nil
}

func (w *InMemoryCatalogWriter) UpsertMembership(_ context.Context, tenantID string, m Membership) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.memberships[tenantID] == nil {
		w.memberships[tenantID] = map[string]Membership{}
	}
	w.memberships[tenantID][membershipKey(m)] = m
	return nil
}

func (w *InMemoryCatalogWriter) PrincipalCount(_ context.Context, tenantID string) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.principals[tenantID]), nil
}

func (w *InMemoryCatalogWriter) GroupCount(_ context.Context, tenantID string) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.groups[tenantID]), nil
}

func (w *InMemoryCatalogWriter) MembershipCount(_ context.Context, tenantID string) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.memberships[tenantID]), nil
}

// Principal returns the stored Principal for a given (tenant, entra_oid),
// or the zero value plus ok=false. Used by tests to assert upsert content.
func (w *InMemoryCatalogWriter) Principal(tenantID, entraOID string) (Principal, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	p, ok := w.principals[tenantID][entraOID]
	return p, ok
}

// Group returns the stored Group for a given (tenant, entra_group_id).
func (w *InMemoryCatalogWriter) Group(tenantID, entraGroupID string) (Group, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	g, ok := w.groups[tenantID][entraGroupID]
	return g, ok
}

// Memberships returns all memberships recorded for a tenant (unordered).
func (w *InMemoryCatalogWriter) Memberships(tenantID string) []Membership {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]Membership, 0, len(w.memberships[tenantID]))
	for _, m := range w.memberships[tenantID] {
		out = append(out, m)
	}
	return out
}

func membershipKey(m Membership) string {
	return m.GroupID + "|" + m.MemberID + "|" + m.MemberType
}

var _ CatalogWriter = (*InMemoryCatalogWriter)(nil)
