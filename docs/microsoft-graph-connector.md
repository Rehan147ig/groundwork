# Microsoft Graph Connector (Entra + SharePoint)

The first real enterprise connector for Groundwork ACL sync. It reads **Microsoft Entra**
users/groups and **SharePoint** folder/file permissions via Microsoft Graph and maps them
onto the `aclsync` domain model, which the Syncer reconciles into OpenFGA. It implements
`aclsync.Connector` and **feeds OpenFGA only** — it never touches the query engine, auth,
or identity, and it does not bypass OpenFGA.

> Status: framework + Graph mapping + delta plumbing, **unit-tested with a mocked Graph
> client** (live Microsoft credentials are required for end-to-end and aren't available in
> the build sandbox). The OAuth/HTTP path is integration-tested later.

## Azure app registration (required Graph permissions)

Register an Entra application and grant **application** (not delegated) permissions, then
grant admin consent:

| Permission | Why |
|---|---|
| `User.Read.All` | enumerate Entra users |
| `Group.Read.All` | enumerate groups + (nested) membership |
| `Sites.Read.All` | read the SharePoint site/drive structure |
| `Files.Read.All` | read drive items (folders/files) and their permissions |

Use a **client secret** (or certificate). The app should be **read-only** — Groundwork
only reads source permissions and writes OpenFGA tuples; it never modifies SharePoint/Entra.

## Environment variables

| Variable | Purpose |
|---|---|
| `ACL_CONNECTOR_TYPE=msgraph` | select this connector |
| `MS_GRAPH_CONNECTOR_ENABLED=true` | explicit enable (refuses to start otherwise) |
| `MS_GRAPH_TENANT_ID` | Entra tenant id (auth) |
| `MS_GRAPH_CLIENT_ID` | app registration client id |
| `MS_GRAPH_CLIENT_SECRET` | app client secret (**inject via secret manager**) |
| `MS_GRAPH_SITE_ID` | SharePoint site id |
| `MS_GRAPH_DRIVE_ID` | SharePoint drive id |
| `MS_GRAPH_AUTHORITY_HOST` | default `https://login.microsoftonline.com` |
| `ACL_DELTA_TOKEN_DIR` | optional dir for durable delta tokens (else in-memory) |

It reuses the sync-service envs (`ACL_SYNC_MODE`, `ACL_SYNC_TENANT_ID`,
`ACL_SYNC_INTERVAL_SECONDS`, `ACL_DRIFT_CHECK_INTERVAL_SECONDS`, `OPENFGA_API_URL`, …) from
`docs/acl-sync-framework.md`.

## Authentication

OAuth 2.0 **client-credentials** flow against
`{authority}/{tenant}/oauth2/v2.0/token` with scope `https://graph.microsoft.com/.default`.
Tokens are cached until shortly before expiry. Token retrieval and all Graph calls **retry
transient failures (5xx/429/network) with exponential backoff + jitter**; a permanent auth
failure (401/403/bad credentials) is **not** retried and propagates so the sync **fails
safely**. **Secrets and access tokens are never logged.**

## How Entra/SharePoint permissions map to OpenFGA

| Source | OpenFGA |
|---|---|
| Entra user | `user:{mail \| userPrincipalName \| id}` |
| Entra group | `group:{object-id}` (keyed by id for stable cross-referencing) |
| user is group member | `user:… member group:…` |
| nested group | `group:{sub-id}#member member group:{parent-id}` |
| SharePoint folder | `folder:{item-id}` |
| SharePoint file | `document:{item-id}` |
| file under folder | `folder:{parent-id} parent document:{file-id}` (inheritance) |
| group can view item | `group:{id}#member viewer folder/document:{item-id}` |
| user can view item | `user:{key} viewer folder/document:{item-id}` |

**Identity key consistency:** users are keyed by `mail` → `userPrincipalName` → object id,
and SharePoint permissions (which reference users by object id) are resolved back to that
same key. **This key MUST match the effective `user_id` your query-time identity token
yields** (`sub`/`email`/`preferred_username`) — otherwise enforced and synced identities
won't line up. Validate this in staging.

**Inheritance:** a file inherits its parent folder's viewers via the `folder → document
parent` relation (the OpenFGA model resolves `viewer from parent`). Graph also returns
inherited permissions on each item, which are captured as direct viewers too.

## Delta / change feed

Uses Graph **drive `delta`** queries to detect new/modified/deleted items. The delta token
is persisted (in-memory by default, or file-backed via `ACL_DELTA_TOKEN_DIR`; swap a
DB-backed `DeltaTokenStore` for scale). This milestone wires **detection + durable token
management + logging**; correctness today comes from the Service's **periodic full
reconcile**, which delta detection accelerates. Granular revoke-event streaming
(`revokeChange`) is the documented next step.

## Safety rules (preserved)

- **No destructive delete on an unconfirmed source.** Any Graph error returns from
  `Snapshot` (never a partial/empty snapshot), so the Syncer's destructive-delete guard
  (`AllowEmptyDestructive=false`) does not wipe OpenFGA on a Graph outage. Proven by
  `TestGraphAuthFailureDoesNotDeleteTuples`.
- Revocations propagate via a non-empty snapshot missing the grant (reconcile) or an
  explicit revoke event.
- Sync failures are logged and retried.

## Dry-run / mock mode

For local development without Azure: keep `ACL_CONNECTOR_TYPE=mock` (the built-in mock
connector), or point the sync service at an in-memory sink (no `OPENFGA_API_URL`). The
Graph mapping itself is exercised by the unit tests via a fake Graph client.

## Limitations

- Live OAuth/HTTP path is integration-tested later (no creds in CI/sandbox).
- One SharePoint site/drive per connector instance (run multiple for multiple sites).
- Permission read is per-item (one Graph call per file/folder) — fine to start; batch/parallelize for large drives.
- Deep folder-tree inheritance relies on each file's `parent` folder plus Graph's
  per-item inherited permissions; multi-level folder→folder inheritance is represented via
  those item permissions, not a folder→folder relation in the model.
- Sharing-link permissions and external/guest identities are mapped conservatively (only
  read-granting roles become viewers).

## Production security warnings

- **Protect the client secret** — inject via a secret manager / Kubernetes secret, never
  commit it, rotate regularly. Prefer a certificate over a secret where possible.
- Grant **least privilege** (read-only Graph scopes above) and scope to the specific
  site/drive.
- Run the connector as its own workload with no inbound exposure; it only needs outbound
  HTTPS to Graph and access to write OpenFGA tuples.
- Never log secrets or tokens (the connector doesn't).
