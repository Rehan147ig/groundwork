# Source-of-Truth ACL Sync Framework

Groundwork enforces document permissions at query time using OpenFGA. But OpenFGA only
knows what it's told. Until now, those permissions were **seeded by hand** — which means
they drift from reality the moment someone changes a SharePoint folder, leaves a team, or
is removed from an Entra group. Stale ACLs are exactly the oversharing risk Groundwork
exists to prevent.

This framework closes that gap: it **syncs real enterprise source permissions into
OpenFGA** so the permission graph Groundwork enforces is a faithful, continuously
reconciled copy of the source of truth.

## Why sync into OpenFGA (instead of querying the source at query time)?

- **Speed**: query-time enforcement is a single OpenFGA check (sub-100ms), not a fan-out
  of live Graph/Okta API calls per chunk.
- **Uniformity**: every source (Entra/SharePoint, Okta, Google) is normalized into one
  relationship model, so the query path never changes per connector.
- **Auditability**: the permission graph at any moment is inspectable, and drift is
  detectable (below).

The query engine is **unchanged** — it still asks OpenFGA "can `user:X` view
`document:Y`?". This framework only *feeds* OpenFGA; it never sits in the query path.

## How it prevents stale ACLs

`SyncToOpenFGA` is a reconciliation, not an append:

1. Take a snapshot of source permissions (`Connector.Snapshot`).
2. Translate it to OpenFGA tuples (`PermissionSetToTuples`).
3. Diff against the tuples currently in OpenFGA.
4. **Write** the tuples the source grants but OpenFGA lacks, and **delete** the tuples
   OpenFGA has but the source no longer grants.

Step 4's delete is how **revocations propagate**: remove a user from the `finance` group
at the source, sync, and the corresponding `member` tuple is deleted — so every
permission that flowed through that group (including inherited document access)
disappears at query time.

## The permission model

The framework writes tuples conforming to the authorization model in
`internal/runtime/openfga.go`:

| Relationship | Example tuple |
|---|---|
| user ∈ group | `user:finance_user  member  group:finance` |
| nested group | `group:finance#member  member  group:employees` |
| group can view folder | `group:finance#member  viewer  folder:finance-folder` |
| document inherits folder | `folder:finance-folder  parent  document:security-policy` |
| direct document viewer | `user:alice  viewer  document:security-policy` |

OpenFGA resolves `viewer document:D` as *direct viewers* **or** *viewers of the parent
folder* (`viewer from parent`), expanding nested group membership transitively. So adding
`folder → document` + group memberships is enough; the query check is unchanged.

## Connector interface (how Entra/Okta/SharePoint plug in later)

Real connectors implement `aclsync.Connector`:

```go
ListDocuments(ctx, tenantID) ([]Document, error)
GetDocumentPermissions(ctx, tenantID, documentID) (DocumentPermissions, error)
WatchPermissionChanges(ctx, tenantID) (<-chan PermissionChange, error)
Snapshot(ctx, tenantID) (PermissionSet, error)
```

(The fourth required operation, `SyncToOpenFGA`, lives on the `Syncer`, which consumes a
`Connector`.) A Microsoft Graph connector will map: Entra users/groups → `user`/`group`,
SharePoint sites/libraries/folders → `folder`, files → `document`, and Graph permission
deltas → `WatchPermissionChanges` for incremental sync. Okta groups and Google Drive
ACLs map the same way. **None of the query-time code changes when a real connector is
added** — only a new `Connector` implementation.

## How revocations are enforced at query time

Proven by `TestRevocationSLA`:

1. `finance_user` can retrieve `security-policy` (finance → finance-folder → document).
2. The source revokes `finance_user` from `group:finance`; `SyncToOpenFGA` deletes the
   `member` tuple.
3. The **same `engine.Execute` path** now returns **zero citations** for `finance_user` —
   query-time OpenFGA enforcement reflects the revocation. Fail-closed behavior is
   preserved throughout.

## Drift detection

`DetectDrift` reports four categories without mutating anything:

- `SourceMissingInFGA` — source grants it, OpenFGA lacks the tuple.
- `FGAExtraNotInSource` — OpenFGA has a tuple the source no longer grants (e.g. an
  unsynced revocation — a live security gap).
- `DocumentsMissingInFGA` — a source document with no OpenFGA tuples.
- `OrphanedFGADocuments` — a document referenced in OpenFGA but absent from the source.

Run it read-only in CI/cron via `cmd/acl-sync -drift-only` (exit code 2 on drift).

## Metrics / logs

Structured (`log/slog`) events from the syncer: `sync_started`, `sync_completed` (with
`tuples_written`, `tuples_deleted`, `sync_duration_ms`), `drift_detected` (per-category
counts), and `acl_sync_skipped_destructive_delete` (safety guard).

The continuous service additionally emits: `acl_sync_service_started`,
`initial_sync_started`, `initial_sync_completed`, `permission_change_received`,
`tuple_write_success`, `tuple_delete_success`, `drift_check_started`,
`drift_check_completed`, `acl_sync_retry`, `acl_sync_service_stopped`.

Prometheus metrics (registered via the metrics package; scrapeable when
`ACL_SYNC_METRICS_ADDR` is set): `groundwork_acl_sync_runs_total`,
`groundwork_acl_sync_errors_total`, `groundwork_acl_sync_drift_items`,
`groundwork_acl_sync_duration_seconds`.

## Running it: once vs. continuous (watch)

`cmd/acl-sync` runs in two modes via `ACL_SYNC_MODE`:

```bash
# ONCE (default, safe): one full sync, then exit.
ACL_SYNC_MODE=once OPENFGA_API_URL=http://openfga:8080 \
  ACL_SYNC_TENANT_ID=tenant_demo \
  go run ./services/query-runtime/cmd/acl-sync

# WATCH: initial sync, then continuously apply permission changes + periodic
# reconcile + drift checks until SIGINT/SIGTERM.
ACL_SYNC_MODE=watch OPENFGA_API_URL=http://openfga:8080 \
  ACL_SYNC_INTERVAL_SECONDS=60 ACL_DRIFT_CHECK_INTERVAL_SECONDS=300 \
  ACL_SYNC_METRICS_ADDR=:9090 \
  go run ./services/query-runtime/cmd/acl-sync
```

With no `OPENFGA_API_URL`/`OPENFGA_URL`, an in-memory sink is used (dev/demo only).

### Environment

| Variable | Default | Purpose |
|---|---|---|
| `ACL_SYNC_MODE` | `once` | `once` (sync + exit) or `watch` (continuous) |
| `ACL_SYNC_TENANT_ID` | `tenant_demo` | tenant to sync |
| `ACL_SYNC_INTERVAL_SECONDS` | `60` | periodic full reconcile (watch) |
| `ACL_DRIFT_CHECK_INTERVAL_SECONDS` | `300` | periodic drift check (watch) |
| `ACL_CONNECTOR_TYPE` | `mock` | source connector (only `mock` today) |
| `OPENFGA_API_URL` (or `OPENFGA_URL`) | — | OpenFGA endpoint; unset → in-memory sink |
| `OPENFGA_STORE_ID` | — | store id (else resolved by name) |
| `OPENFGA_STORE_NAME` | `groundwork_local` | store name |
| `OPENFGA_AUTHORIZATION_MODEL_ID` | — | pin a model id on writes |
| `ACL_SYNC_METRICS_ADDR` | — | expose Prometheus `/metrics` (e.g. `:9090`) |

### How revocation propagates

Two paths, both fail-safe:

1. **Change events (fast):** a revocation arrives via `WatchPermissionChanges` and is
   applied as a targeted tuple **delete** (an explicit revocation → safe to delete).
2. **Periodic reconcile (convergent):** the full snapshot diff deletes any tuple the
   source no longer grants.

Either way, the next query-time OpenFGA check denies the revoked user.

### Drift detection on interval

In watch mode, drift is checked every `ACL_DRIFT_CHECK_INTERVAL_SECONDS`, reported via
logs and the `groundwork_acl_sync_drift_items` gauge — without mutating anything.

### Retry / backoff and the destructive-delete guard

Sync and change-apply operations retry with **exponential backoff + jitter** and never
crash on a transient OpenFGA outage — they keep retrying until success or shutdown.
**Deletes are refused on an empty/unconfirmed snapshot** (`AllowEmptyDestructive=false`),
so a connector outage returning nothing can never wipe existing permissions.

### Production warning

The mock connector is credential-free. **Real connectors (Microsoft Graph, Okta, Google)
require source credentials — protect them.** Inject them via your secret manager /
Kubernetes secrets, never commit them, scope them least-privilege (read-only directory +
permission scopes), and rotate them. Run the sync service as its own workload with only
the access it needs to read source permissions and write OpenFGA tuples.

## Limitations of the mock connector

The mock (`MockConnector`) is a fixed, in-memory Entra/SharePoint-style dataset for
development and testing. It is **not** a real connector:

- No Microsoft Graph / Okta / Google API calls, pagination, throttling, or auth.
- A small fixed dataset (3 users, 3 groups, 3 folders, 3 documents) with one level of
  folder→document inheritance and one level of group nesting.
- `WatchPermissionChanges` emits events only for changes made via the mock's own
  mutators (e.g. `RevokeGroupMember`), not a real change feed.
- One OpenFGA store is used for all tenants (per-tenant stores are a future enhancement);
  tenant isolation at query time is still enforced by the engine before the OpenFGA check.

The real Microsoft Graph connector is intentionally **out of scope** for this milestone —
this PR delivers the framework + mock so connectors can be added without touching the
query path.
