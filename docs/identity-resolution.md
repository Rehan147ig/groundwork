# Identity Resolution — Canonical Principal Resolver (Phase 1)

Groundwork enforces permissions for the **verified end-user** on whose behalf a query runs.
But the identifier a token carries is not stable across identity providers or apps: Microsoft
Entra issues an app- and IdP-scoped `sub`, a different app sees a different `sub` for the same
person, email and UPN change, and a generic OIDC provider uses yet another subject. If
OpenFGA tuples are keyed by whatever string the current token happens to present, the same
human can be authorized under one identifier and silently denied under another.

**Canonical identity** fixes this by mapping every verified external identity to one
tenant-scoped **principal** (`principal:<uuid>`). OpenFGA tuples are written against the
principal; at query time the verified token is resolved to the same principal, so the engine
checks `user:principal:<uuid>`. The query engine (`Engine.Execute`) is **unchanged** — the
runtime simply sets `req.UserID = "principal:<uuid>"` after resolution, and the existing
`user:`-prefixing OpenFGA check resolves naturally.

> Status: resolver + REST/MCP integration + Graph-sync canonical tuples, fully unit-tested
> with the in-memory resolver. The Postgres resolver is exercised against a live database in
> integration. **Off by default** (`GROUNDWORK_CANONICAL_IDENTITY` unset) so demo/local mode
> and existing deployments keep working unchanged.

## Model

```
principals(id uuid, tenant_id, created_at)
principal_aliases(tenant_id, principal_id, namespace, value, verified_at, expires_at,
                  UNIQUE(tenant_id, namespace, value))
```

A **principal** is a canonical, tenant-scoped identity. An **alias** is one verified external
claim that points at a principal. Aliases are unique **per tenant** — the same external value
in two tenants resolves to two different principals (cross-tenant isolation, tested).

### Alias namespaces

| Namespace | Source claim | Trust |
|---|---|---|
| `entra:id` | Entra `oid` (directory object id) | always verified — the authoritative join key |
| `jwt:sub` | OIDC `sub` (generic providers) | always verified |
| `jwt:email` | `email` | **only** when `email_verified=true`, or when written by directory sync |
| `jwt:preferred_username` | `preferred_username` / UPN | same gating as email |

`entra:id` is the reliable join: the Entra `oid` claim equals the Graph directory object id,
so a token and a directory record converge on it regardless of email/UPN churn. Email and UPN
are **unverified-by-default** OIDC claims, so they are treated as identity aliases only when
the token asserts `email_verified=true` (this prevents a token from claiming someone else's
email to borrow their principal). Directory sync writes them as verified because the directory
*is* the source of truth.

## Query-time flow (fail closed)

1. Transport verifies the JWT (REST `X-Groundwork-User-Assertion`, or MCP `user_token`) — an
   unsigned/expired/tampered token is rejected exactly as before.
2. `AssertionsFromIdentity` derives the verified assertions from the claims (with the email /
   UPN gating above).
3. `CanonicalizeIdentity(resolver, GROUNDWORK_CANONICAL_IDENTITY, tenant, identity)`:
   - flag **off**, resolver nil, or identity **unverified/demo** → returns the raw user id,
     status `skipped` (no behavior change);
   - flag **on**, identity verified, alias **found** → returns `principal:<uuid>`, status
     `resolved`;
   - flag **on**, identity verified, **no alias** → `ErrIdentityUnresolved`, status
     `unresolved` → the request **fails closed** (`403 identity_unresolved` on REST, an
     `IsError` fail-closed tool result on MCP). It never silently downgrades to the raw subject.
4. The engine runs the query against `user:principal:<uuid>`. tenant_id/region still come
   **only** from the Groundwork API key, never from the request body.

Both transports share this path: REST in `Server.query`, MCP in `executeSearch` (used by both
stdio and the Cloud `/mcp` endpoint).

## Resolvers

| Resolver | Use | Notes |
|---|---|---|
| `MemoryPrincipalResolver` | tests / local demo | does **not** auto-create on `Resolve` (unresolved fails closed, exactly like prod); aliases come from `UpsertAliases` / `Seed` |
| `PostgresPrincipalResolver` | production | `SELECT … WHERE tenant_id/namespace/value AND (expires_at IS NULL OR expires_at > now())`; `UpsertAliases` is a transaction (`principals` ON CONFLICT DO NOTHING + `principal_aliases` ON CONFLICT DO UPDATE) |
| `CachingResolver` | hot-path wrapper | short-TTL **positive** cache so the per-query lookup doesn't hit the DB every time; cleared on `UpsertAliases`; alias `expires_at` is enforced by the inner resolver on a miss and overall staleness is bounded by the TTL |

`expires_at` lets an alias be time-boxed (e.g. a temporary external identity); an expired
alias does not resolve (tested). Production wires `CachingResolver(Postgres)`; local/demo wires
`CachingResolver(Memory)`. Cache TTL: `GROUNDWORK_PRINCIPAL_CACHE_TTL_MS` (default 60s).

## Pre-provisioning via ACL sync

A principal must exist **before** a user's first query, or that query fails closed. The
Microsoft Graph connector pre-provisions them: with `GROUNDWORK_CANONICAL_IDENTITY=true` it
resolves (or mints) a principal for **every directory user**, upserts the verified aliases
(`entra:id` / `jwt:email` / `jwt:preferred_username`), and emits **canonical** tuples.

Canonicalization rewrites **every user reference** in a snapshot — group membership, folder
viewers, and direct document grants alike — so the tuples become:

```
user:principal:<uuid>  member  group:G          (group membership — canonical)
group:H#member         member  group:G          (nested group — unchanged)
user:principal:<uuid>  viewer  folder:F         (folder viewer — canonical)
user:principal:<uuid>  viewer  document:D       (direct document viewer — canonical)
folder:F               parent  document:D        (inheritance — unchanged)
```

Only **users** are canonicalized; groups/folders/inheritance are untouched. The Graph object
id is the join, so a user referenced by a SharePoint grant (by object id) and the same user in
the directory collapse onto one principal. The sync resolver **must share the query runtime's
Postgres** (`DATABASE_URL`) so the aliases it writes are the ones the runtime resolves against.

## Audit

Every audit row records how identity was resolved, and both fields feed the immutable digest
so the resolution cannot be rewritten after the fact:

| Column | Meaning |
|---|---|
| `identity_resolution` | `""` (canonical off / N/A) or `resolved` |
| `principal_id` | the canonical principal UUID when `user_id = user:principal:<uuid>` |

(Migration `007_add_audit_identity_columns`; the resolver schema is `006_add_principal_aliases`.)

## Configuration

| Variable | Purpose |
|---|---|
| `GROUNDWORK_CANONICAL_IDENTITY=true` | enable canonical resolution (query runtime **and** acl-sync) |
| `GROUNDWORK_PRINCIPAL_CACHE_TTL_MS` | per-query alias cache TTL (default 60000) |
| `DATABASE_URL` | Postgres for the production resolver (shared by runtime + sync) |

## Rollout

1. Apply migrations `006` and `007`.
2. Run `acl-sync` with `GROUNDWORK_CANONICAL_IDENTITY=true` against the **same** `DATABASE_URL`
   to pre-provision principals + aliases and write canonical tuples (start in the connector's
   normal reconcile; the destructive-delete guard still protects against an empty snapshot).
3. Once aliases exist, set `GROUNDWORK_CANONICAL_IDENTITY=true` on the query runtime. Verified
   identities now resolve to principals; an unknown verified identity fails closed.
4. Leave it **off** to keep the prior behavior (raw token subject as the OpenFGA user). Demo
   identities (`ALLOW_DEMO_IDENTITY=true`) are always `skipped` — canonical mode never affects
   them.

## Security properties

- **Fail closed.** A verified-but-unresolved identity is denied, never downgraded to the raw
  subject.
- **No unverified aliasing.** Email/UPN only count when `email_verified=true` or written by
  directory sync.
- **Tenant isolation.** Aliases are unique per tenant; a shared external value never resolves
  across tenants.
- **No principal auto-creation from tokens.** The query path never mints a principal from an
  unknown JWT — only directory sync (an authenticated, authoritative source) creates them.
- **Tamper-evident.** `identity_resolution` + `principal_id` are covered by the audit digest.
- **Engine untouched.** `Engine.Execute` is unchanged; only `req.UserID` is canonicalized
  upstream, so OpenFGA enforcement is identical.
