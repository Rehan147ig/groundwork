# CI/CD Pipeline

Groundwork is a security product, so CI is part of the security guarantee - not just
engineering hygiene. Every pull request and every push to `master` is validated by a set of
GitHub Actions workflows under `.github/workflows/`. This is **Phase 1: PR validation**. It
deliberately does **not** start live infrastructure (no Postgres/OpenFGA/Qdrant/Elasticsearch),
use any cloud credentials, or publish/release anything - those come in later phases.

## Workflows

| Workflow | File | Trigger | What it validates |
|---|---|---|---|
| `go-ci` | `go-ci.yml` | PR + push to master | The Go query-runtime builds, is gofmt-clean, passes `go vet`, and passes the full test suite **with the race detector**. |
| `python-ci` | `python-ci.yml` | PR + push to master | The ingestion service installs and its unit tests pass against in-memory fakes (no live Qdrant/Elasticsearch). |
| `console-ci` | `console-ci.yml` | PR + push to master | The Next.js console installs (`npm ci` at the workspace root) and `next build` succeeds. |
| `compose-validate` | `compose-validate.yml` | PR + push to master | Both `infra/docker-compose*.yml` files parse and resolve (`docker compose config --quiet`). The stack is **not** started. |
| `secret-scan` | `secret-scan.yml` | every PR | gitleaks finds no committed secrets/keys/tokens in the diff. |
| `security-ci` | `security-ci.yml` | PR + push to master | **Only** the security-critical tests that prove Groundwork's core guarantees (see below). Fails the PR if any fail - or if the regex matches zero tests. |
| `migration-check` | `migration-check.yml` | PR touching `migrations/**` | `migrations/*_*.up.sql` form a contiguous, gap-free, duplicate-free sequence and each has a matching `.down.sql`. |

## Required vs. recommended checks

Configure these in **Settings -> Branches -> Branch protection rule for `master` -> Require status
checks to pass before merging**.

**Required before merge:**
- `go-ci`
- `python-ci`
- `console-ci`
- `compose-validate`
- `secret-scan`
- `security-ci`

**Recommended but not required initially:**
- `migration-check`

### Why the required checks have no path filter

A GitHub *required* status check whose workflow is skipped by a top-level `paths:` filter is
reported as **pending forever**, which **blocks merging** any PR that doesn't touch those paths
(e.g. a docs-only PR would be stuck waiting on `go-ci`). To avoid that foot-gun, the six
required workflows intentionally run on **every** PR (no path filter). The only path-filtered
workflow is `migration-check`, which is *not* required, so skipping it never blocks a merge.

**Phase 2 optimization (optional):** to skip the heavy required jobs on irrelevant PRs without
blocking merges, split each into a `changes` job (using a path-filter action such as
`dorny/paths-filter`) plus the real job gated on `needs.changes.outputs.<x> == 'true'`. A
*skipped* (not absent) required job is treated by GitHub as passing, so merges stay unblocked.
That added complexity wasn't worth it for Phase 1.

## Running the same checks locally

### Go (`go-ci`)
```bash
cd services/query-runtime
go build ./...
test -z "$(gofmt -l .)" || { echo "unformatted:"; gofmt -l .; exit 1; }
go vet ./...
go test ./...
go test -race ./...
```

### Security invariants (`security-ci`)
```bash
cd services/query-runtime
PATTERN='FailsClosed|FailClosed|Unresolved|CrossTenant|Authorized|AccessMatrix|FiltersACL|ClassifyACL|Audit|VerifyChain|JWTVerifier|Forged|IgnoresBodyUserID|VerifiedIdentity|ShadowMode|Revocation|DestructiveDelete|NoDeleteRule|NoUpdateRule|ImmutableDigest'
go test -v -run "$PATTERN" ./...
# Sanity-check what the gate covers (lists, does not run):
go test ./... -list "$PATTERN"
```

### Python ingestion (`python-ci`)
```bash
cd services/ingestion
pip install -r requirements.txt
python -m unittest discover -s tests -v
```
Note: a bare `python -m unittest discover` from `services/ingestion` finds **0 tests** (the
`tests/` directory is not a package) and still exits 0 - a false green. Always use `-s tests`.

### Console (`console-ci`)
```bash
# From the repo ROOT (npm workspaces - the only package-lock.json is at the root):
npm ci
NEXT_TELEMETRY_DISABLED=1 npm run build --workspace apps/console
```

### Compose validation (`compose-validate`)
```bash
docker compose -f infra/docker-compose.yml config --quiet
GROUNDWORK_BOOTSTRAP_API_KEY=local GROUNDWORK_JWT_HS_SECRET=local \
  docker compose -f infra/docker-compose.prod.yml config --quiet
```
The prod file has two required `${VAR:?}` variables; CI passes throwaway non-secret
placeholders just so interpolation resolves. Real secrets are injected at deploy time.

### Migration order (`migration-check`)
```bash
python3 scripts/check_migrations.py
```

## The security invariant gate, in detail

`security-ci` runs `go test -run "<regex>" ./...` where the regex (the `SECURITY_TESTS` env in
the workflow) is built from the **actual** test names in the repo and matches ~40 tests across
the `engine`, `runtime`, `mcp`, and `aclsync` packages, covering:

- **Fail-closed enforcement** - retrieval timeout, ACL-circuit-open, audit-write failure,
  missing/invalid identity assertions, backend failure.
- **Cross-tenant isolation** - cross-tenant candidates blocked; aliases don't resolve across
  tenants.
- **ACL filtering + folder inheritance** - group/nested-group and folder->document resolution.
- **Immutable, tamper-evident audit ledger** - digest, no-update/no-delete rules, chain
  verification (clean / broken-link / modified-row).
- **Identity** - JWT verification (rejects none-alg, bad signature, expired, missing expiry),
  forged `user_id` ignored in favor of the verified token, canonical principal resolution,
  and unresolved-identity fail-closed.
- **Shadow mode** - observe-only returns but records would-block, while tenant/region stay hard.
- **Revocation propagation** - revocation SLA and watch-mode revocation.

> Warning: `go test -run` matches test-name **substrings** and exits 0 even when it matches nothing.
> A regex that matches no tests would make the security gate pass vacuously. The workflow
> guards against this: it counts `=== RUN` lines and **fails if zero security tests ran**. If
> you rename or move security tests, update `SECURITY_TESTS` in `security-ci.yml` (and this
> doc) so the gate keeps covering them.

## Why Microsoft Graph (and other connectors) use mocks in CI

The Microsoft Graph connector talks to Entra/SharePoint over OAuth 2.0 client-credentials,
which needs real Azure tenant credentials. CI must never hold cloud secrets and must be
hermetic, so the Graph connector is unit-tested against a **fake `GraphClient`**
(`services/query-runtime/internal/aclsync/msgraph/msgraph_test.go`): mapping, nested groups,
folder/document inheritance, permission->viewer translation, revocation events, delta
classification, and the "Graph auth failure must not delete tuples" safety property are all
exercised without a network. The same principle applies to Postgres/OpenFGA/Qdrant/Elasticsearch:
PR CI uses in-memory fakes; the live OAuth/HTTP and database paths are integration-tested in a
later phase with credentials supplied out-of-band (never committed, never in PR CI).

## Adding a workflow when a new service is added

1. Create `.github/workflows/<service>-ci.yml`. Trigger on `pull_request` and `push: branches: [master]`.
2. Use the matching language toolchain action (`actions/setup-go`/`setup-python`/`setup-node`) and
   cache dependencies keyed on the lockfile (`go.sum` / `requirements.txt` / `package-lock.json`).
3. Keep it **hermetic** - fakes/mocks only, no live infra, no cloud secrets.
4. Give the single job a unique `name:` (this becomes the status-check name) and a unique
   `concurrency.group`.
5. If it should gate merges, leave it **unfiltered** (run on all PRs) and add it to the required
   checks list above. Only path-filter checks that are *not* required.
6. Update this document and, if the service has security-critical behavior, add its test-name
   patterns to `SECURITY_TESTS` in `security-ci.yml`.
