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

Structured (`log/slog`) events: `sync_started`, `sync_completed` (with `tuples_written`,
`tuples_deleted`, `sync_duration_ms`), and `drift_detected` (with per-category counts).

## Running it

```bash
# Sync the mock connector into a live OpenFGA, then report drift:
OPENFGA_URL=http://openfga:8080 go run ./services/query-runtime/cmd/acl-sync -tenant tenant_demo

# Read-only drift check (exit 2 if drift found):
OPENFGA_URL=http://openfga:8080 go run ./services/query-runtime/cmd/acl-sync -drift-only
```

With no `OPENFGA_URL`, an in-memory sink is used (dev/demo only).

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
