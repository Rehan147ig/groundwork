# Non-Bypassable Deployment

Groundwork's "zero data leakage" guarantee is a property of the **network topology**,
not just the code. The engine fails closed and enforces live ACLs on every chunk it
returns — but that only protects data **that flows through Groundwork**. If an AI agent
(or a curious engineer, or a compromised service) can reach Qdrant, OpenFGA, PostgreSQL,
or Elasticsearch **directly**, it bypasses every control Groundwork provides.

> **The guarantee holds only when Groundwork is the single reachable path to retrieval.**
> Direct backend access must be blocked at the network layer. This document describes the
> production deployment profile that enforces that, and how to validate it.

## Threat model in one line

The data plane (vector store, lexical index, permission graph, audit DB, ingestion) must
be **unreachable from anywhere an attacker or agent sits**. Only Groundwork's authenticated
endpoints may be exposed.

## Ports: safe to expose vs. must stay internal

| Service | Port | Production exposure |
|---|---|---|
| **Gateway (Nginx)** | 80 / 443 | **PUBLIC** — the only host-published port |
| query-runtime | 8080 | internal only (reached via the gateway) |
| console (optional) | 3000 | internal only (via gateway `/console`) |
| ingestion | 8000 | **internal only** |
| Qdrant | 6333 | **internal only — never public** |
| OpenFGA | 8080 (8081 grpc) | **internal only — never public** |
| PostgreSQL (audit + keys) | 5432 | **internal only — never public** |
| Elasticsearch | 9200 | **internal only — never public** |
| MinIO | 9000 / 9001 | **internal only — never public** |

Public, allow-listed Groundwork endpoints (everything else returns 404 at the gateway):

- `POST /v1/query` — REST retrieval
- `POST /mcp` — Cloud MCP (JSON-RPC 2.0)
- `GET /healthz`, `GET /readyz` — liveness/readiness
- `GET /metrics` — **internal by default**; expose only to a restricted scraper CIDR
- `/console` — optional CISO console (requires `basePath: '/console'`)

> The admin API-key endpoints (`/v1/admin/api-keys…`) are intentionally **not** in the
> public allow-list. Manage keys over the internal network / a bastion, never the open
> internet.

## The production profile

`infra/docker-compose.prod.yml` puts every service on an internal Docker network and
publishes **only** the gateway's host port. query-runtime reaches the backends by service
name (`qdrant:6333`, `openfga:8080`, `postgres:5432`, `elasticsearch:9200`). Because the
backends declare no `ports:` mapping, they cannot be reached from the host's public
interface at all. `infra/nginx/nginx.conf` is default-closed: only the allow-list above is
forwarded; every other path is rejected.

The dev profile (`infra/docker-compose.yml`) is unchanged and **may** expose backend ports
for debugging — never run it as your production ingress.

## Three enforcement modes

All three share the same principle: Groundwork holds the only credentials/route to the
data, and the network blocks everything else.

### 1. API gateway mode
Apps call `POST /v1/query` through the gateway instead of querying Qdrant directly. The
vector store, OpenFGA, and audit DB live on a private network with no public route. This is
the default in `docker-compose.prod.yml`.

### 2. MCP gateway mode
AI agents/clients connect to `POST /mcp` (Cloud MCP, JSON-RPC 2.0) through the gateway. Same
backends, same private network. The agent toolchain can only retrieve via the Groundwork
tool — it has no network path to the vector DB. See `docs/cloud-mcp-http.md`.

### 3. Sidecar / proxy mode
For existing AI apps you don't want to modify: run Groundwork as a sidecar/egress proxy and
use network policy so the **only** egress route to the vector DB/sources is through
Groundwork. In Kubernetes this is a `NetworkPolicy` that allows traffic to Qdrant/OpenFGA/
Postgres/Elasticsearch **only** from the query-runtime pod, plus holding the backend
credentials solely in query-runtime. (Compose models this with the internal network; k8s
enforces it with NetworkPolicies — see `infra/helm/groundwork`.)

## Running it

> Docker is required. (It was not available in the environment where this profile was
> authored, so the steps below are the exact commands to run locally.)

```bash
# 1. Provide secrets (never commit real values).
export GROUNDWORK_BOOTSTRAP_API_KEY="gw_live_replace_me"
export GROUNDWORK_JWT_HS_SECRET="replace-with-a-strong-secret"   # or use an RSA public key

# 2. Bring up the production profile.
docker compose -f infra/docker-compose.prod.yml up --build -d

# 3. Validate that ONLY Groundwork is exposed.
GW_URL=http://localhost ./scripts/validate-non-bypassable.sh
#   (PowerShell: ./scripts/validate-non-bypassable.ps1 -GwUrl http://localhost)
```

## How to validate the backends are not reachable

The validation script automates this; to check manually:

```bash
# Groundwork IS reachable (through the gateway):
curl -s -o /dev/null -w '%{http_code}\n' http://localhost/healthz          # 200
curl -s -o /dev/null -w '%{http_code}\n' -X POST http://localhost/mcp \
     -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'        # 401 (needs API key)

# Backends are NOT reachable on host ports (each must FAIL / connection refused):
curl -s -m 3 http://localhost:6333/collections   ; echo "  <- Qdrant must refuse"
curl -s -m 3 http://localhost:8081/stores         ; echo "  <- OpenFGA must refuse"
curl -s -m 3 http://localhost:9200/_cluster/health; echo "  <- Elasticsearch must refuse"
nc -z -w3 localhost 5432 && echo "PostgreSQL REACHABLE (BAD)" || echo "PostgreSQL refused (good)"
```

A correct production deployment: `/healthz` returns `200`, `/mcp` returns `401` without a
key, and every backend probe is refused. If any backend probe **succeeds**, the deployment
is bypassable — fix the port mapping / network policy before going live.

## What this does NOT change

- `Engine.Execute`, auth, and identity are untouched — this is a deployment/topology change
  only.
- Fail-closed behavior and the immutable audit ledger are preserved (every `/v1/query` and
  `/mcp` call still runs the full engine path).
- The stdio MCP transport and the Cloud MCP HTTP endpoint are preserved.
