package msgraph

import (
	"context"
	"errors"
	"strings"

	"groundwork/query-runtime/internal/aclsync"
	"groundwork/query-runtime/internal/runtime"
)

// directoryAssertions builds the verified identity aliases a Graph directory record yields.
//
// The Entra object id is the authoritative join key: it is written as entra:id and matches
// the query-time "oid" claim, so a synced principal and a live token converge regardless of
// email/UPN changes. Mail and userPrincipalName are recorded as verified aliases
// (jwt:email / jwt:preferred_username) BECAUSE the directory is the source of truth — at
// query time those same namespaces are only trusted when the token asserts
// email_verified=true, so the two sides resolve to the same principal without trusting an
// unverified self-asserted email.
func directoryAssertions(mail, upn, id string) []runtime.IdentityAssertion {
	var a []runtime.IdentityAssertion
	if v := strings.TrimSpace(id); v != "" {
		a = append(a, runtime.IdentityAssertion{Namespace: "entra:id", Value: v, Verified: true})
	}
	if v := strings.TrimSpace(mail); v != "" {
		a = append(a, runtime.IdentityAssertion{Namespace: "jwt:email", Value: v, Verified: true})
	}
	if v := strings.TrimSpace(upn); v != "" {
		a = append(a, runtime.IdentityAssertion{Namespace: "jwt:preferred_username", Value: v, Verified: true})
	}
	return a
}

// canonicalizer maps Graph identities to tenant-scoped canonical principals and rewrites a
// PermissionSet so every USER reference becomes "principal:<uuid>". Groups, folders, and
// folder->document inheritance are untouched; because only users are rewritten, the group
// membership tuples (user member group:G) also end up carrying canonical principals.
//
// observe() resolves an existing principal by the directory aliases, minting a fresh one
// when none exists — this is how Graph sync pre-provisions a principal (and its aliases)
// so the user's first query resolves instead of failing closed. It is keyed by the same
// userKey the pure mapping functions emit, so rewrite() can remap the assembled snapshot.
type canonicalizer struct {
	ctx       context.Context
	resolver  runtime.PrincipalResolver
	tenantID  string
	byUserKey map[string]string // userKey -> "principal:<uuid>"
}

func newCanonicalizer(ctx context.Context, resolver runtime.PrincipalResolver, tenantID string) *canonicalizer {
	return &canonicalizer{ctx: ctx, resolver: resolver, tenantID: tenantID, byUserKey: map[string]string{}}
}

// observe resolves (or mints) the canonical principal for one Graph identity and records the
// userKey -> principal mapping. Idempotent per person: repeated calls (e.g. a directory user
// who is also a group member and a document grantee) collapse onto the same principal because
// the resolver joins on the stable entra:id alias.
func (c *canonicalizer) observe(mail, upn, id string) error {
	uk := userKey(mail, upn, id)
	if uk == "" {
		return nil
	}
	if _, ok := c.byUserKey[uk]; ok {
		return nil
	}
	assertions := directoryAssertions(mail, upn, id)
	if len(assertions) == 0 {
		return nil
	}
	p, err := c.resolver.Resolve(c.ctx, c.tenantID, assertions)
	switch {
	case errors.Is(err, runtime.ErrIdentityUnresolved):
		p = runtime.Principal{ID: runtime.NewPrincipalID(), TenantID: c.tenantID}
	case err != nil:
		return err
	}
	// Upsert writes the full alias set for a freshly minted principal and refreshes aliases
	// (recording any new email/UPN) for an existing one.
	if err := c.resolver.UpsertAliases(c.ctx, c.tenantID, p, assertions); err != nil {
		return err
	}
	c.byUserKey[uk] = runtime.PrincipalUserID(p.ID)
	return nil
}

// mapUserKey returns the canonical principal user id for a userKey. An unobserved key is
// returned unchanged: it then produces a user:<key> tuple that the canonical query path
// (user:principal:<uuid>) never matches, i.e. it fails closed rather than over-granting.
func (c *canonicalizer) mapUserKey(uk string) string {
	if v, ok := c.byUserKey[uk]; ok {
		return v
	}
	return uk
}

// rewrite replaces every user reference in the snapshot with its canonical principal id.
func (c *canonicalizer) rewrite(ps *aclsync.PermissionSet) {
	for i := range ps.Users {
		ps.Users[i] = c.mapUserKey(ps.Users[i])
	}
	for gi := range ps.Groups {
		for ui := range ps.Groups[gi].MemberUsers {
			ps.Groups[gi].MemberUsers[ui] = c.mapUserKey(ps.Groups[gi].MemberUsers[ui])
		}
	}
	for fi := range ps.Folders {
		for ui := range ps.Folders[fi].ViewerUsers {
			ps.Folders[fi].ViewerUsers[ui] = c.mapUserKey(ps.Folders[fi].ViewerUsers[ui])
		}
	}
	for di := range ps.Documents {
		for ui := range ps.Documents[di].ViewerUsers {
			ps.Documents[di].ViewerUsers[ui] = c.mapUserKey(ps.Documents[di].ViewerUsers[ui])
		}
	}
}
