# Microsoft Graph Connector

Operator-facing connector that — in its final form — synchronizes a customer's
Microsoft 365 tenant (identity, group membership, one chosen SharePoint site
with drive items and permissions) into Groundwork's OpenFGA permission graph
and the `msgraph` Postgres catalog. The connector runs out-of-band; the
query-runtime never calls Microsoft Graph at request time.

**This image is the scaffold.** It validates configuration and exits. The
sync library it will wrap already lives in
`services/query-runtime/internal/aclsync/msgraph` and contains user/group
mapping, canonical principal resolution, snapshot access-matrix construction,
and delta-token bookkeeping from prior work.

---

## 1. What this connector does

In its final form (subsequent PRs):

- Authenticates to Microsoft Graph via OAuth 2.0 client-credentials.
- Enumerates users, groups, transitive group memberships, and one chosen
  SharePoint site with its drives, items, and per-item permissions.
- Writes principals + groups + sites + drives + documents + permission grants
  into the `msgraph` Postgres schema (catalog tables added in migration 008).
- Writes OpenFGA tuples conforming to the four-type model defined by
  `services/query-runtime/internal/runtime/openfga.go`:
  `user`, `group`, `folder`, `document`. The connector **never** writes the
  OpenFGA authorization model — that ownership belongs to the query-runtime.
- Runs once per operator invocation. No daemon mode, no webhooks, no delta
  sync in the pilot scope.

## 2. Azure app registration

The customer's Microsoft 365 administrator performs these one-time steps:

1. Sign in to the Azure Portal of the customer's tenant.
2. **Azure Active Directory ▸ App registrations ▸ New registration.**
3. Name: `Groundwork Connector (Pilot)`. Audience: *Accounts in this
   organizational directory only*.
4. After registration, copy **Application (client) ID** and
   **Directory (tenant) ID** — these become `MSGRAPH_CLIENT_ID` and
   `MSGRAPH_TENANT_ID`.
5. **Certificates & secrets** → new client secret. Copy the value (shown once)
   — this becomes `MSGRAPH_CLIENT_SECRET`.
6. **API permissions** → add the application (not delegated) permissions in
   section 3, then **Grant admin consent**.

## 3. Required OAuth scopes

All four scopes are **application-permission, read-only**. No write scope is
ever requested, so the connector cannot modify the customer's Microsoft tenant.

| Scope | Purpose |
| --- | --- |
| `User.Read.All` | Enumerate users in the tenant. |
| `GroupMember.Read.All` | Resolve group memberships for ReBAC inheritance. |
| `Sites.Read.All` | Enumerate the chosen SharePoint site and its drives. |
| `Files.Read.All` | Read drive items and per-item permission grants. |

A customer with a stricter security posture can substitute `Sites.Selected`
for `Sites.Read.All` (grants per-site access rather than tenant-wide). The
connector code path is identical; only the OAuth admin-consent step changes.

## 4. How to run

The connector is gated by the Compose `pilot` profile, so `make demo` and
`make up` do not bring it up.

Set the four required environment variables in `.env`:

```
MSGRAPH_TENANT_ID=...
MSGRAPH_CLIENT_ID=...
MSGRAPH_CLIENT_SECRET=...
OPENFGA_STORE_ID=...
```

Then run the scaffold-level validation:

```
make connector-enumerate
```

The scaffold prints `OK — config valid, no work yet` and exits `0` when the
four variables are set; otherwise it lists the missing variables and exits
`1`.
