# Groundwork

Groundwork is a runtime data access control layer that sits between enterprise AI applications and private data sources. Its single purpose: enforce that every AI query obeys live permissions, data residency rules, and audit requirements before any data reaches the model. It is not a RAG tool. It is not a chatbot. It is infrastructure.

## Core Components

- Go query runtime: concurrent ACL evaluation, circuit breaker, fail-closed enforcement, sub-100ms p99 target
- Python ingestion engine: semantic chunking, fastembed embeddings, atomic dual-write to Qdrant and Elasticsearch
- OpenFGA: live permission graph, replaces all tag-based ACL checks
- Qdrant: vector search with int8 scalar quantization
- Elasticsearch: lexical search (query path currently bypassed, kept for future compliance search module)
- PostgreSQL: tenant metadata, audit traces, immutable query log
- Next.js console: CISO dashboard, live ACL test screen

## What Groundwork Prevents

- Unauthorised data retrieval: fully blocked via OpenFGA at query time
- Cross-tenant data leakage: fully blocked via namespace isolation
- Indirect prompt injection via documents: partially mitigated via basic input sanitisation, not a core feature
- LLM output manipulation: out of scope, handled by output guardrail layer (separate product)

## Security Model

Groundwork enforces permissions at query time, not ingestion time. If a user's access is revoked at 2pm, they cannot retrieve documents at 2:01pm. If OpenFGA is unreachable, the system returns zero chunks and logs FAIL_CLOSED. There is no fallback to an open state under any failure condition.

## Integration Patterns

### Pattern A: REST API Gateway

AI app calls `/v1/query` instead of calling Qdrant directly. Groundwork retrieves candidate chunks, checks OpenFGA live permissions, filters unauthorized chunks, and returns only permitted citations.

### Pattern B: MCP Server

AI agents and tools like Cursor or Claude Desktop connect through Groundwork's MCP transport. MCP and REST share the same Go engine path, so identity verification, tenant resolution, OpenFGA checks, shadow mode, and audit behavior stay identical across transports.

### Pattern C: Sidecar Proxy

Groundwork intercepts traffic between an AI app and vector DB at the network level. This is a deployment pattern for teams that cannot modify application code directly.

## Explicitly Out Of Scope For V1

- Prompt injection scanner (not a core feature, basic sanitisation only)
- Cross-encoder reranking
- Google Drive / Slack connectors
- SOC 2 certification
- HIPAA BAA
- FedRAMP
- Redis ACL cache (planned, not yet built)
- OCI deployment (planned, not yet built)

## Current Repository Layout

```txt
apps/console                         Next.js admin console and live ACL test screen
apps/marketing-site                  Public waitlist / marketing website
services/query-runtime               Go runtime gateway, MCP server, OpenFGA checks, audit, identity
services/ingestion                   Python parser, chunker, embedding, and dual-index writer
packages/contracts                   Shared API contracts and schemas
examples/                            Demo clients and integration examples
infra/docker-compose.yml             Local development infrastructure
infra/docker-compose.prod.yml        Non-bypassable production-style deployment profile
infra/helm/groundwork                Early Kubernetes / Helm packaging
migrations/                          Postgres schema migrations
scripts/                             Validation, demo, migration, and integration helper scripts
docs/                                Architecture, security, CI, deployment, and connector documentation
docs/business/                       Pitch and investor-facing business material
docs/archive/                        Older long-form planning artifacts kept out of the repo root
```

## Roadmap Items

The following are roadmap items, not current production capabilities:

- Google Drive connector
- Slack connector
- Multi-region physical cloud deployment
- Cross-encoder reranking
- Full prompt injection scanner
- OCI production deployment
- Redis ACL cache

## Local Development

Install dependencies:

```bash
npm install
```

Run the console:

```bash
npm run dev --workspace apps/console
```

Run Python tests:

```bash
python -m unittest discover services/ingestion/tests
```

Run Go tests:

```bash
cd services/query-runtime
go test ./...
```
