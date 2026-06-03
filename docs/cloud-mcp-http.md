# Cloud MCP over HTTP

Groundwork exposes its MCP tools over HTTP at **`POST /mcp`**, served on the same
listener as the REST API (default `:8080`). This lets remote AI agents and hosted
clients (e.g. HyperAgent) reach Groundwork without launching a local stdio process.

> **Transport milestone.** This is the first Cloud MCP transport: **JSON-RPC 2.0 over a
> single HTTP POST** (request → response). It is wire-compatible with the request half of
> MCP "Streamable HTTP". The full Streamable HTTP session model (SSE streaming, `Mcp-Session-Id`,
> resumable streams) is a planned follow-up and is intentionally **not** implemented here.
> The stdio MCP transport is unchanged and remains fully supported.

## Architecture (no second engine)

`POST /mcp` reuses the **exact same** MCP tool registry and the single `Engine.Execute`
path as the stdio transport. There is one engine, one set of fail-closed/audit
guarantees, and one identity-resolution path shared across transports.

```
Agent ──HTTP POST /mcp (JSON-RPC)──▶ query-runtime
            │  API key  → tenant_id + region
            │  JWT      → effective user_id
            ▼
        mcp.dispatch ──▶ Engine.Execute ──▶ retrieval → live ACL filter → audit → permitted context
```

## Security model

| Concern | Source of truth |
|---|---|
| **Authentication** | Groundwork API key — **required** for every `/mcp` request |
| **tenant_id, region** | Resolved **only** from the API key. Never from the body or tool arguments. |
| **Effective user_id** | A verified OIDC/JWT identity assertion (`sub` → `email` → `preferred_username`). Header `X-Groundwork-User-Assertion`, or the `user_token` tool argument. The header wins. |
| **Demo/dev** | `ALLOW_DEMO_IDENTITY=true` allows a raw `user_id` argument. **Off by default; never enable in production.** |
| **Failure** | Fail closed — missing/invalid API key → `401`/`403`; missing/invalid identity or backend failure → zero documents (`isError`), and the query is still audited. |

The API key is sent as either `Authorization: Bearer <key>` or `X-Groundwork-API-Key: <key>`.

## Supported methods

`initialize`, `tools/list`, `tools/call` (tool: `groundwork_search`), and `ping`.
Notifications (e.g. `initialized`) are accepted and answered with `202 Accepted` (no body).

## curl examples

Assume `GW=http://localhost:8080` and a local API key `gw_local_acme_key`.

**Missing API key → 401**
```bash
curl -s -o /dev/null -w "%{http_code}\n" -X POST "$GW/mcp" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
# 401
```

**initialize**
```bash
curl -s -X POST "$GW/mcp" \
  -H "X-Groundwork-API-Key: gw_local_acme_key" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
# {"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05",...,"serverInfo":{"name":"groundwork","version":"1.0.0"}}}
```

**tools/list**
```bash
curl -s -X POST "$GW/mcp" \
  -H "X-Groundwork-API-Key: gw_local_acme_key" \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
# result.tools[0].name == "groundwork_search"
```

**tools/call — production (verified identity via JWT)**
```bash
curl -s -X POST "$GW/mcp" \
  -H "X-Groundwork-API-Key: gw_local_acme_key" \
  -H "X-Groundwork-User-Assertion: <signed-OIDC-JWT>" \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call",
       "params":{"name":"groundwork_search",
                 "arguments":{"question":"How do live ACL checks fail closed?"}}}'
```
The effective user comes from the JWT. Any `user_id` in `arguments` is ignored when a
JWT is present.

**tools/call — local/demo (`ALLOW_DEMO_IDENTITY=true`)**
```bash
curl -s -X POST "$GW/mcp" \
  -H "X-Groundwork-API-Key: gw_local_acme_key" \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call",
       "params":{"name":"groundwork_search",
                 "arguments":{"user_id":"finance_user","question":"How do live ACL checks fail closed?"}}}'
# finance_user → permitted; general_user → "ACCESS DENIED ... Blocked by ACL: 1"
```

**ping**
```bash
curl -s -X POST "$GW/mcp" -H "X-Groundwork-API-Key: gw_local_acme_key" \
  -d '{"jsonrpc":"2.0","id":9,"method":"ping","params":{}}'
# {"jsonrpc":"2.0","id":9,"result":{}}
```

## Connecting HyperAgent

HyperAgent connects to an MCP server by URL with custom headers. Point it at the Cloud
MCP endpoint and supply the Groundwork API key (and, in production, a per-user identity
token) as headers:

```json
{
  "mcpServers": {
    "groundwork": {
      "url": "https://your-groundwork-host/mcp",
      "transport": "http",
      "headers": {
        "X-Groundwork-API-Key": "gw_live_...",
        "X-Groundwork-User-Assertion": "<per-user OIDC/JWT>"
      }
    }
  }
}
```

- The API key scopes the connection to one tenant/region.
- Supply `X-Groundwork-User-Assertion` per end user so Groundwork enforces *that user's*
  permissions. In local demos with `ALLOW_DEMO_IDENTITY=true` you may instead pass
  `user_id` in the tool arguments.
- Because this milestone is plain JSON-RPC POST (no SSE), configure the client for a
  non-streaming HTTP MCP transport.

## Running locally (Docker Compose)

`infra/docker-compose.yml` builds `query-runtime` and maps port `8080`, so `/mcp` is
reachable at `http://localhost:8080/mcp`. The compose file sets `DATABASE_URL`, so every
`/mcp` call writes an immutable, hash-chained audit row (see `docs/architecture.md` and
`cmd/audit-verify`). For local convenience it sets `ALLOW_DEMO_IDENTITY=true`; remove that
and set `GROUNDWORK_JWT_HS_SECRET` (or an RSA public key) for production identity.

```bash
cd infra
docker compose up --build query-runtime postgres openfga qdrant elasticsearch
# then use the curl examples above against http://localhost:8080/mcp
```

## Guarantees preserved

- **Fail-closed**: identity or backend failure returns zero documents (and `isError`),
  never unauthorized content.
- **Immutable audit**: `/mcp` calls go through the same `Engine.Execute` path, so each
  request is synchronously written to the tamper-evident audit ledger before returning.
- **No trust in arguments**: `tenant_id`/`region` come only from the API key; `user_id`
  comes only from the verified identity token (except in explicit demo mode).
