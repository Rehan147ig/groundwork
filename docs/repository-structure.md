# Repository Structure

Groundwork is organized as a monorepo. Keep production code, demo code, business material, and archived planning artifacts separate so the repo stays easy to review.

## Product Code

```txt
services/query-runtime/    Go runtime gateway, MCP, ACL enforcement, identity, audit
services/ingestion/        Python ingestion, parsing, chunking, embeddings, indexing
apps/console/              Admin console for security telemetry and live ACL testing
apps/marketing-site/       Public waitlist and landing website
packages/contracts/        Shared API schemas
```

## Operations

```txt
infra/                     Docker Compose, production profile, Nginx, Helm
migrations/                Postgres migrations
scripts/                   Validation, demo, integration, and migration helper scripts
.github/workflows/         CI checks
```

## Examples And Demos

```txt
examples/                  Integration examples and demo clients
examples/mcp/              Legacy/simple MCP helper examples
```

Future bank demos should live under `examples/bank-demo/`, not inside the runtime service.

## Documentation

```txt
docs/                      Architecture, security, deployment, CI, connector docs
docs/business/             Pitch and investor-facing material
docs/archive/              Older long-form planning artifacts kept for history
```

Root-level files should stay limited to repo entrypoints such as `README.md`, `package.json`, `.gitignore`, and `.env.example`.
