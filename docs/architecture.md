# Groundwork Architecture

Groundwork is a runtime data access control layer that sits between enterprise AI applications and private data sources. Its single purpose: enforce that every AI query obeys live permissions, data residency rules, and audit requirements before any data reaches the model. It is not a RAG tool. It is not a chatbot. It is infrastructure.

## Runtime Flow

```txt
Enterprise AI app / agent
        |
        v
Groundwork query runtime
        |
        +-- retrieve candidate chunks from Qdrant
        +-- evaluate live document permissions through OpenFGA
        +-- enforce tenant namespace and region constraints
        +-- strip all unauthorized chunks
        +-- emit query trace and immutable digest
        |
        v
Permitted citations only
```

No model should receive raw retrieved chunks directly from Qdrant. Groundwork is the enforcement layer between retrieval and model context.

## Core Components

- Go query runtime: concurrent ACL evaluation, circuit breaker, fail-closed enforcement, sub-100ms p99 target
- Python ingestion engine: semantic chunking, fastembed embeddings, atomic dual-write to Qdrant and Elasticsearch
- OpenFGA: live permission graph, replaces all tag-based ACL checks
- Qdrant: vector search with int8 scalar quantization
- Elasticsearch: lexical search (query path currently bypassed, kept for future compliance search module)
- PostgreSQL: tenant metadata, audit traces, immutable query log
- Next.js console: CISO dashboard, live ACL test screen

## Security Model

Groundwork enforces permissions at query time, not ingestion time. If a user's access is revoked at 2pm, they cannot retrieve documents at 2:01pm. If OpenFGA is unreachable, the system returns zero chunks and logs FAIL_CLOSED. There is no fallback to an open state under any failure condition.

The ingestion service may write permission relationships into OpenFGA, but ingestion-time tagging is not trusted as final authorization. The Go runtime must still check OpenFGA live for every retrieved chunk before the chunk can be emitted.

## What Groundwork Prevents

- Unauthorised data retrieval: fully blocked via OpenFGA at query time
- Cross-tenant data leakage: fully blocked via namespace isolation
- Indirect prompt injection via documents: partially mitigated via basic input sanitisation, not a core feature
- LLM output manipulation: out of scope, handled by output guardrail layer (separate product)

## Integration Patterns

### Pattern A: REST API Gateway

AI app calls `/v1/query` instead of calling Qdrant directly. This is the current integration path.

### Pattern B: MCP Server

AI agents and tools like Cursor or Claude Desktop connect via Model Context Protocol. The MCP transport wraps the same Go engine used by the REST API so security behavior remains identical across transports.

### Pattern C: Sidecar Proxy

Groundwork intercepts traffic between an AI app and vector DB at the network level. This pattern is useful for enterprises that need policy enforcement without rewriting existing AI applications.

## V1 Scope Boundaries

### Current V1 Capabilities

- REST `/v1/query` runtime path
- MCP runtime path
- Qdrant vector candidate retrieval
- OpenFGA query-time authorization
- Fail-closed behavior when retrieval or authorization is unavailable
- Circuit breaker around Qdrant retrieval
- Canonical principal resolver for tenant-scoped identity alignment
- Microsoft Graph ACL sync framework and connector mapping
- Python local file ingestion
- fastembed embeddings
- Atomic dual-write to Qdrant and Elasticsearch
- CISO-oriented Next.js console

### Explicitly Out Of Scope For V1

- Prompt injection scanner (not a core feature, basic sanitisation only)
- Cross-encoder reranking
- Google Drive / Slack connectors
- Production OAuth rollout for enterprise connectors
- SOC 2 certification
- HIPAA BAA
- FedRAMP
- Redis ACL cache (planned, not yet built)
- OCI deployment (planned, not yet built)

## Roadmap Clarifications

The following must be described as roadmap items, not current capabilities:

- Prompt injection scanner as a primary feature
- Reranking as a v1 feature
- Multi-region physical deployment as current

Groundwork currently supports region and tenant enforcement in its contracts and runtime checks. Physical multi-region cloud deployment is a roadmap deployment package.
