# Integration Testing (P0 — live backends)

Groundwork's unit tests run against in-memory fakes, which is fast but never proves the system
works when wired to real infrastructure. This harness closes that gap: it stands up **live
OpenFGA, Postgres, and Qdrant** and runs the engine's three most important guarantees against
them. It is the first slice of "Phase 2 CI" from the pilot punch list.

## What it proves

| Test | Guarantee |
|---|---|
| `TestFailClosedOnUnauthorizedUser` | An authorized user retrieves the document; a verified-but-**unauthorized** user gets **zero** documents (the candidate is blocked by a live OpenFGA `check`). Real per-user enforcement, end to end. |
| `TestFailClosedWhenOpenFGADown` | When OpenFGA is **unreachable**, the engine **fails closed** (zero documents) instead of failing open. Retrieval still works (Qdrant is up); only the ACL backend is down. |
| `TestAuditChainWritesToPostgres` | Queries **actually persist** to the immutable `audit_log` in Postgres, and the rows form a **valid hash chain** — read back through the production `LoadAuditChain` + `VerifyChain`. |

## How to run

Prerequisites: **Docker** (with the `docker compose` v2 plugin) and **Go**.

```bash
scripts/integration-test.sh
```

That script: brings up `services/query-runtime/test/integration/docker-compose.yml`, exports
the `GROUNDWORK_TEST_*` env, runs `go test -tags integration -count=1 -v ./test/integration/...`,
and tears the stack down on exit (even on failure).

### Running the suite by hand

```bash
docker compose -p gw-integration -f services/query-runtime/test/integration/docker-compose.yml up -d

export GROUNDWORK_TEST_DATABASE_URL="postgres://groundwork:groundwork@localhost:5432/groundwork?sslmode=disable"
export GROUNDWORK_TEST_OPENFGA_URL="http://localhost:8080"
export GROUNDWORK_TEST_QDRANT_URL="http://localhost:6333"
export GROUNDWORK_TEST_MIGRATIONS_DIR="$(pwd)/migrations"

cd services/query-runtime
go test -tags integration -count=1 -v ./test/integration/...
```

## How it's wired (and why it's hermetic)

- **Build tag.** Every test file is `//go:build integration`, so the normal `go test ./...`
  CI never compiles or runs them — they need the live services. A non-tagged `doc.go` keeps
  the package present so `go build ./...` stays happy. Without the env set, the tests `t.Skip`
  (an honest skip, never a silent pass).
- **Real code paths.** Tests drive the real `engine.Execute`, the real `runtime.OpenFGAChecker`
  (which provisions the store + authorization model itself), the real `QdrantVectorSearcher`,
  and the real `engine.PostgresAuditWriter` / `LoadAuditChain` / `VerifyChain`.
- **Embeddings are stubbed, not the datastores.** A tiny in-process HTTP server returns a fixed
  vector for `/embed`, so we exercise the real Qdrant search without shipping the ML model.
  Vector *values* don't matter here — Qdrant returns the nearest of the seeded points
  regardless — these tests are about authorization and audit, not retrieval quality.
- **Migrations.** The harness applies `migrations/*.up.sql` in numeric order (003 → 007) to the
  test Postgres; `005` alters `audit_log`, so order matters. A successful `LoadAuditChain`
  implicitly proves `007` (the identity columns) applied.
- **Fresh + deterministic.** Datastores use tmpfs / no volumes, and each test uses a unique
  tenant + collection + OpenFGA store, so the audit-chain row counts are exact.

## Finding surfaced by this harness: the 30ms audit timeout

`NewPostgresAuditWriter` hardcodes a **30ms** per-write timeout. Because `PostgresAuditWriter.Write`
derives its own context from that value, it **silently caps** the engine's configured
`AUDIT_TIMEOUT_MS` (2s for Postgres in `cmd/query-runtime`). Against real Postgres — advisory
lock + select + insert, under load or a cold connection — 30ms is easy to exceed, which turns
every audit write into `audit_write_failed` and **fails the query closed**. The tests use the
new additive `engine.NewPostgresAuditWriterWithTimeout(db, 10s)` to be deterministic.

**Recommended follow-up (separate PR):** wire `cmd/query-runtime` to construct the Postgres
audit writer with `NewPostgresAuditWriterWithTimeout(db, cfg.AuditWrite)` (or bump the default),
so production honors `AUDIT_TIMEOUT_MS` instead of the 30ms cap.

## Wiring this into CI (next step)

This is intentionally **not** part of the fast PR CI. A future `integration-ci.yml` workflow
(GitHub Actions service containers, or `docker compose` + this script) should run it on a
schedule and pre-merge to `master`. Note: workflow files can't be pushed through the GitHub
integration (missing `workflow` scope), so that YAML is added the same way as the Phase 1
workflows.

## Adding a scenario

Add a `//go:build integration` test in `services/query-runtime/test/integration/`. Reuse the
harness helpers: `seedQdrantChunk`, `startStubEmbedder`, `qdrantSearcher`, `initOpenFGAStore` +
`writeOpenFGATuple`, `newEngine`, `postgresAuditor`, and `openDB`. Use `unique()` for tenant /
collection / store names so scenarios stay isolated.
