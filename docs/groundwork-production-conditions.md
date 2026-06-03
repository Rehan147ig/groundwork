# Groundwork Production Conditions

Groundwork is a runtime data access control layer that sits between enterprise AI applications and private data sources. Its single purpose: enforce that every AI query obeys live permissions, data residency rules, and audit requirements before any data reaches the model. It is not a RAG tool. It is not a chatbot. It is infrastructure.

## Current Architecture

- Go query runtime: concurrent ACL evaluation, circuit breaker, fail-closed enforcement, sub-100ms p99 target
- Python ingestion engine: semantic chunking, fastembed embeddings, atomic dual-write to Qdrant and Elasticsearch
- OpenFGA: live permission graph, replaces all tag-based ACL checks
- Qdrant: vector search with int8 scalar quantization
- Elasticsearch: lexical search (query path currently bypassed, kept for future compliance search module)
- PostgreSQL: tenant metadata, audit traces, immutable query log
- Next.js console: CISO dashboard, live ACL test screen

## Security Model

Groundwork enforces permissions at query time, not ingestion time. If a user's access is revoked at 2pm, they cannot retrieve documents at 2:01pm. If OpenFGA is unreachable, the system returns zero chunks and logs FAIL_CLOSED. There is no fallback to an open state under any failure condition.

## Required Production Guarantees

### Access Control

- Every retrieved chunk must be checked against OpenFGA before it can be returned.
- OpenFGA denial must remove the chunk from the response.
- OpenFGA timeout or network failure must remove the chunk from the response.
- No transport layer may bypass the shared authorization engine.

### Tenant Isolation

- Cross-tenant data leakage: fully blocked via namespace isolation
- Tenant ID must be enforced from trusted runtime context, not only from user-provided request body.
- Vector collections, lexical indexes, OpenFGA stores, and audit partitions must preserve tenant boundaries.

### Data Residency

- Runtime requests must carry region context.
- Candidate chunks whose region does not match the request must be blocked.
- Multi-region physical deployment is a roadmap item, not a current production capability.

### Auditability

- Every query must produce a trace.
- The trace must include retrieved candidate counts, blocked-by-ACL counts, blocked-by-region counts, access decisions, and fail-closed reasons.
- Immutable query log persistence belongs in PostgreSQL and/or append-only storage for production.

## What Groundwork Prevents

- Unauthorised data retrieval: fully blocked via OpenFGA at query time
- Cross-tenant data leakage: fully blocked via namespace isolation
- Indirect prompt injection via documents: partially mitigated via basic input sanitisation, not a core feature
- LLM output manipulation: out of scope, handled by output guardrail layer (separate product)

## Explicitly Out Of Scope For V1

- Prompt injection scanner (not a core feature, basic sanitisation only)
- Cross-encoder reranking
- SharePoint / Google Drive / Slack connectors
- SOC 2 certification
- HIPAA BAA
- FedRAMP
- Redis ACL cache (planned, not yet built)
- OCI deployment (planned, not yet built)

## Integration Patterns

### Pattern A: REST API Gateway

AI app calls `/v1/query` instead of calling Qdrant directly.

### Pattern B: MCP Server

AI agents and tools like Cursor or Claude Desktop connect via Model Context Protocol. This is planned.

### Pattern C: Sidecar Proxy

Groundwork intercepts traffic between AI app and vector DB at network level.

## Roadmap Items

The following are not current production capabilities:

- Prompt injection scanner as a primary feature
- Reranking as a v1 feature
- Microsoft 365 OAuth connector as current
- Multi-region physical deployment as current
- SharePoint connector
- Google Drive connector
- Slack connector
- Redis ACL cache
- OCI deployment

## Production Readiness Checklist

Groundwork should not be described as enterprise-production ready until these are complete:

- API key authentication and tenant resolution
- Persistent OpenFGA datastore
- PostgreSQL-backed tenant metadata and immutable trace persistence
- End-to-end authorization test matrix
- Load testing for p95 and p99 query latency
- Operational metrics for Qdrant, OpenFGA, and query runtime
- Backup and restore procedure
- TLS and secret-management hardening
- Deployment runbook for the target environment
