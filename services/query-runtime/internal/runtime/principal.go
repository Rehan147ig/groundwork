package runtime

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// ErrIdentityUnresolved means no verified alias maps to a canonical principal. In
// production this fails closed — the query is denied.
var ErrIdentityUnresolved = errors.New("identity_unresolved")

// IdentityAssertion is one external identity claim (e.g. entra:id=<oid>, jwt:email=<mail>).
type IdentityAssertion struct {
	Namespace string
	Value     string
	Verified  bool
	ExpiresAt *time.Time
}

// Principal is a tenant-scoped canonical identity. Its OpenFGA user string is
// "user:principal:<ID>" (the engine builds "user:"+req.UserID, so req.UserID = "principal:<ID>").
type Principal struct {
	ID       string
	TenantID string
}

// PrincipalResolver maps verified external aliases to a canonical principal.
type PrincipalResolver interface {
	Resolve(ctx context.Context, tenantID string, assertions []IdentityAssertion) (Principal, error)
	UpsertAliases(ctx context.Context, tenantID string, principal Principal, aliases []IdentityAssertion) error
}

// PrincipalUserID is the engine UserID for a principal (becomes user:principal:<id>).
func PrincipalUserID(principalID string) string { return "principal:" + principalID }

// NewPrincipalID returns a random UUIDv4 string (used by sync to mint new principals).
func NewPrincipalID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// AssertionsFromIdentity derives the identity assertions to resolve from a verified JWT.
// entra:id (oid) and jwt:sub are always trustworthy; email / preferred_username are only
// treated as verified when the token asserts email_verified (avoids unverified-claim spoofing).
func AssertionsFromIdentity(id Identity) []IdentityAssertion {
	var a []IdentityAssertion
	if id.OID != "" {
		a = append(a, IdentityAssertion{Namespace: "entra:id", Value: id.OID, Verified: true})
	}
	if id.Subject != "" {
		a = append(a, IdentityAssertion{Namespace: "jwt:sub", Value: id.Subject, Verified: true})
	}
	if id.Email != "" {
		a = append(a, IdentityAssertion{Namespace: "jwt:email", Value: id.Email, Verified: id.EmailVerified})
	}
	if id.Username != "" {
		a = append(a, IdentityAssertion{Namespace: "jwt:preferred_username", Value: id.Username, Verified: id.EmailVerified})
	}
	return a
}

// CanonicalizeIdentity converts a verified Identity into the effective OpenFGA user id.
// Off (or for an unverified/demo identity) it returns the raw user id unchanged; on, it
// resolves a canonical principal and returns "principal:<uuid>". An unresolved verified
// identity returns ErrIdentityUnresolved (fail closed). The returned status is one of
// "skipped" | "resolved" | "unresolved" | "error".
func CanonicalizeIdentity(ctx context.Context, resolver PrincipalResolver, canonical bool, tenantID string, id Identity) (effectiveUserID, status string, err error) {
	if !canonical || resolver == nil || !id.Verified {
		return id.UserID, "skipped", nil
	}
	p, err := resolver.Resolve(ctx, tenantID, AssertionsFromIdentity(id))
	if err != nil {
		if errors.Is(err, ErrIdentityUnresolved) {
			return "", "unresolved", ErrIdentityUnresolved
		}
		return "", "error", err
	}
	return PrincipalUserID(p.ID), "resolved", nil
}

func verifiedOnly(assertions []IdentityAssertion) []IdentityAssertion {
	out := assertions[:0:0]
	for _, a := range assertions {
		if a.Verified && a.Value != "" {
			out = append(out, a)
		}
	}
	return out
}

// --- MemoryPrincipalResolver (tests / local demo) ---

type aliasRecord struct {
	principalID string
	expiresAt   *time.Time
}

// MemoryPrincipalResolver is an in-memory resolver for tests/demo. It does NOT auto-create
// principals on Resolve (unresolved fails closed, exactly like production); aliases are
// created via UpsertAliases / Seed.
type MemoryPrincipalResolver struct {
	mu      sync.RWMutex
	aliases map[string]aliasRecord
}

func NewMemoryPrincipalResolver() *MemoryPrincipalResolver {
	return &MemoryPrincipalResolver{aliases: map[string]aliasRecord{}}
}

func aliasKey(tenant, namespace, value string) string {
	return tenant + "\x1f" + namespace + "\x1f" + value
}

func (m *MemoryPrincipalResolver) Resolve(_ context.Context, tenantID string, assertions []IdentityAssertion) (Principal, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, a := range verifiedOnly(assertions) {
		rec, ok := m.aliases[aliasKey(tenantID, a.Namespace, a.Value)]
		if !ok {
			continue
		}
		if rec.expiresAt != nil && time.Now().After(*rec.expiresAt) {
			continue
		}
		return Principal{ID: rec.principalID, TenantID: tenantID}, nil
	}
	return Principal{}, ErrIdentityUnresolved
}

func (m *MemoryPrincipalResolver) UpsertAliases(_ context.Context, tenantID string, principal Principal, aliases []IdentityAssertion) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range aliases {
		if a.Value == "" {
			continue
		}
		m.aliases[aliasKey(tenantID, a.Namespace, a.Value)] = aliasRecord{principalID: principal.ID, expiresAt: a.ExpiresAt}
	}
	return nil
}

// Seed is a test helper that maps a set of aliases to a principal id.
func (m *MemoryPrincipalResolver) Seed(tenantID, principalID string, aliases []IdentityAssertion) {
	_ = m.UpsertAliases(context.Background(), tenantID, Principal{ID: principalID, TenantID: tenantID}, aliases)
}

// --- PostgresPrincipalResolver (production) ---

type PostgresPrincipalResolver struct {
	db      *sql.DB
	timeout time.Duration
}

func NewPostgresPrincipalResolver(db *sql.DB) *PostgresPrincipalResolver {
	return &PostgresPrincipalResolver{db: db, timeout: 2 * time.Second}
}

func (p *PostgresPrincipalResolver) Resolve(ctx context.Context, tenantID string, assertions []IdentityAssertion) (Principal, error) {
	if p == nil || p.db == nil {
		return Principal{}, fmt.Errorf("principal resolver unavailable")
	}
	rctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	for _, a := range verifiedOnly(assertions) {
		var id string
		err := p.db.QueryRowContext(rctx, `
			SELECT principal_id FROM principal_aliases
			WHERE tenant_id = $1 AND namespace = $2 AND value = $3
			  AND (expires_at IS NULL OR expires_at > now())
			LIMIT 1
		`, tenantID, a.Namespace, a.Value).Scan(&id)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return Principal{}, err
		}
		return Principal{ID: id, TenantID: tenantID}, nil
	}
	return Principal{}, ErrIdentityUnresolved
}

func (p *PostgresPrincipalResolver) UpsertAliases(ctx context.Context, tenantID string, principal Principal, aliases []IdentityAssertion) error {
	if p == nil || p.db == nil {
		return fmt.Errorf("principal resolver unavailable")
	}
	rctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	tx, err := p.db.BeginTx(rctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(rctx, `INSERT INTO principals (id, tenant_id) VALUES ($1, $2) ON CONFLICT (id) DO NOTHING`, principal.ID, tenantID); err != nil {
		return err
	}
	for _, a := range aliases {
		if a.Value == "" {
			continue
		}
		var expires any
		if a.ExpiresAt != nil {
			expires = *a.ExpiresAt
		}
		if _, err := tx.ExecContext(rctx, `
			INSERT INTO principal_aliases (tenant_id, principal_id, namespace, value, verified_at, expires_at)
			VALUES ($1, $2, $3, $4, now(), $5)
			ON CONFLICT (tenant_id, namespace, value)
			DO UPDATE SET principal_id = EXCLUDED.principal_id, verified_at = now(), expires_at = EXCLUDED.expires_at
		`, tenantID, principal.ID, a.Namespace, a.Value, expires); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// --- CachingResolver (short-TTL alias->principal cache on the hot path) ---

type cacheRecord struct {
	principal Principal
	until     time.Time
}

// CachingResolver wraps a resolver with a short-TTL positive cache, so the per-query
// alias lookup doesn't hit the database every time. Alias expires_at is enforced by the
// inner resolver on cache miss; cache staleness is bounded by ttl.
type CachingResolver struct {
	inner PrincipalResolver
	ttl   time.Duration
	mu    sync.Mutex
	cache map[string]cacheRecord
}

func NewCachingResolver(inner PrincipalResolver, ttl time.Duration) *CachingResolver {
	if ttl <= 0 {
		ttl = 60 * time.Second
	}
	return &CachingResolver{inner: inner, ttl: ttl, cache: map[string]cacheRecord{}}
}

func (c *CachingResolver) Resolve(ctx context.Context, tenantID string, assertions []IdentityAssertion) (Principal, error) {
	key := resolveCacheKey(tenantID, assertions)
	c.mu.Lock()
	if rec, ok := c.cache[key]; ok && time.Now().Before(rec.until) {
		c.mu.Unlock()
		return rec.principal, nil
	}
	c.mu.Unlock()

	p, err := c.inner.Resolve(ctx, tenantID, assertions)
	if err != nil {
		return p, err
	}
	c.mu.Lock()
	c.cache[key] = cacheRecord{principal: p, until: time.Now().Add(c.ttl)}
	c.mu.Unlock()
	return p, nil
}

func (c *CachingResolver) UpsertAliases(ctx context.Context, tenantID string, principal Principal, aliases []IdentityAssertion) error {
	err := c.inner.UpsertAliases(ctx, tenantID, principal, aliases)
	// Aliases changed; clear the cache so stale negatives/positives don't linger.
	c.mu.Lock()
	c.cache = map[string]cacheRecord{}
	c.mu.Unlock()
	return err
}

func resolveCacheKey(tenantID string, assertions []IdentityAssertion) string {
	parts := make([]string, 0, len(assertions))
	for _, a := range verifiedOnly(assertions) {
		parts = append(parts, a.Namespace+"="+a.Value)
	}
	sort.Strings(parts)
	return tenantID + "\x1f" + strings.Join(parts, "\x1e")
}
