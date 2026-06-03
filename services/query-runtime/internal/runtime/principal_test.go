package runtime

import (
	"context"
	"errors"
	"testing"
	"time"
)

func va(ns, val string) IdentityAssertion {
	return IdentityAssertion{Namespace: ns, Value: val, Verified: true}
}

func TestResolveOIDMapsToEntraID(t *testing.T) {
	ctx := context.Background()
	r := NewMemoryPrincipalResolver()
	// Graph sync wrote the entra:id alias for this principal.
	r.Seed("t1", "p-1", []IdentityAssertion{va("entra:id", "oid-123")})

	// A JWT presenting oid -> entra:id assertion resolves to the same principal.
	id := Identity{OID: "oid-123", Verified: true}
	p, err := r.Resolve(ctx, "t1", AssertionsFromIdentity(id))
	if err != nil || p.ID != "p-1" {
		t.Fatalf("oid should resolve to p-1, got %+v err=%v", p, err)
	}
}

func TestResolveSubGenericOIDC(t *testing.T) {
	ctx := context.Background()
	r := NewMemoryPrincipalResolver()
	r.Seed("t1", "p-2", []IdentityAssertion{va("jwt:sub", "okta|abc")})
	id := Identity{Subject: "okta|abc", Verified: true}
	p, err := r.Resolve(ctx, "t1", AssertionsFromIdentity(id))
	if err != nil || p.ID != "p-2" {
		t.Fatalf("generic sub should resolve to p-2, got %+v err=%v", p, err)
	}
}

func TestEmailVerificationGating(t *testing.T) {
	ctx := context.Background()
	r := NewMemoryPrincipalResolver()
	r.Seed("t1", "p-3", []IdentityAssertion{va("jwt:email", "alice@corp.com")})

	// email_verified=false -> the email assertion is not verified -> unresolved.
	unverified := Identity{Email: "alice@corp.com", EmailVerified: false, Verified: true}
	if _, err := r.Resolve(ctx, "t1", AssertionsFromIdentity(unverified)); !errors.Is(err, ErrIdentityUnresolved) {
		t.Fatal("unverified email must NOT resolve")
	}
	// email_verified=true -> resolves.
	verified := Identity{Email: "alice@corp.com", EmailVerified: true, Verified: true}
	if p, err := r.Resolve(ctx, "t1", AssertionsFromIdentity(verified)); err != nil || p.ID != "p-3" {
		t.Fatalf("verified email should resolve to p-3, got %+v err=%v", p, err)
	}
}

func TestUnresolvedFailsClosed(t *testing.T) {
	r := NewMemoryPrincipalResolver()
	if _, err := r.Resolve(context.Background(), "t1", []IdentityAssertion{va("entra:id", "nobody")}); !errors.Is(err, ErrIdentityUnresolved) {
		t.Fatal("unknown identity must be unresolved (fail closed)")
	}
}

func TestCrossTenantAliasDoesNotResolve(t *testing.T) {
	ctx := context.Background()
	r := NewMemoryPrincipalResolver()
	r.Seed("tenant-a", "p-a", []IdentityAssertion{va("entra:id", "shared-oid")})
	if _, err := r.Resolve(ctx, "tenant-b", []IdentityAssertion{va("entra:id", "shared-oid")}); !errors.Is(err, ErrIdentityUnresolved) {
		t.Fatal("alias must not resolve across tenants")
	}
}

func TestExpiresAtInvalidatesAlias(t *testing.T) {
	ctx := context.Background()
	r := NewMemoryPrincipalResolver()
	past := time.Now().Add(-time.Hour)
	r.Seed("t1", "p-4", []IdentityAssertion{{Namespace: "entra:id", Value: "oid-exp", Verified: true, ExpiresAt: &past}})
	if _, err := r.Resolve(ctx, "t1", []IdentityAssertion{va("entra:id", "oid-exp")}); !errors.Is(err, ErrIdentityUnresolved) {
		t.Fatal("expired alias must not resolve")
	}
}

type countingResolver struct {
	inner PrincipalResolver
	calls int
}

func (c *countingResolver) Resolve(ctx context.Context, t string, a []IdentityAssertion) (Principal, error) {
	c.calls++
	return c.inner.Resolve(ctx, t, a)
}
func (c *countingResolver) UpsertAliases(ctx context.Context, t string, p Principal, a []IdentityAssertion) error {
	return c.inner.UpsertAliases(ctx, t, p, a)
}

func TestCacheHitAvoidsSecondLookup(t *testing.T) {
	ctx := context.Background()
	mem := NewMemoryPrincipalResolver()
	mem.Seed("t1", "p-5", []IdentityAssertion{va("entra:id", "oid-cache")})
	counter := &countingResolver{inner: mem}
	cached := NewCachingResolver(counter, time.Minute)

	assertions := []IdentityAssertion{va("entra:id", "oid-cache")}
	if p, err := cached.Resolve(ctx, "t1", assertions); err != nil || p.ID != "p-5" {
		t.Fatalf("first resolve: %+v %v", p, err)
	}
	if p, err := cached.Resolve(ctx, "t1", assertions); err != nil || p.ID != "p-5" {
		t.Fatalf("second resolve: %+v %v", p, err)
	}
	if counter.calls != 1 {
		t.Fatalf("cache hit should avoid a second inner lookup, inner calls=%d", counter.calls)
	}
}

func TestCanonicalizeIdentity(t *testing.T) {
	ctx := context.Background()
	r := NewMemoryPrincipalResolver()
	r.Seed("t1", "p-6", []IdentityAssertion{va("entra:id", "oid-6")})

	// Canonical off -> raw user id, skipped.
	raw := Identity{UserID: "alice", Verified: true}
	if uid, status, err := CanonicalizeIdentity(ctx, r, false, "t1", raw); err != nil || uid != "alice" || status != "skipped" {
		t.Fatalf("canonical off: uid=%q status=%q err=%v", uid, status, err)
	}
	// Canonical on, resolves -> principal:<id>.
	id := Identity{OID: "oid-6", Verified: true}
	if uid, status, err := CanonicalizeIdentity(ctx, r, true, "t1", id); err != nil || uid != "principal:p-6" || status != "resolved" {
		t.Fatalf("canonical resolved: uid=%q status=%q err=%v", uid, status, err)
	}
	// Canonical on, unknown -> fail closed.
	unknown := Identity{OID: "nope", Verified: true}
	if _, status, err := CanonicalizeIdentity(ctx, r, true, "t1", unknown); !errors.Is(err, ErrIdentityUnresolved) || status != "unresolved" {
		t.Fatalf("canonical unresolved: status=%q err=%v", status, err)
	}
}
