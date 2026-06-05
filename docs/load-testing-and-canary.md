# Load testing & production canary

Two operator tools under `services/query-runtime/cmd/`. Both talk to the stack over HTTP, so
they run from any machine against a local or deployed Groundwork.

## `loadtest` — seed a realistic dataset + measure performance

Turns "I worry it's slow" into numbers: p50/p95/p99 latency, throughput, and the fail-closed
rate under concurrency. Two modes.

**Seed** a bank-shaped dataset (run the runtime once first so it provisions the OpenFGA store):
```bash
go run ./cmd/loadtest -mode=seed \
  -qdrant=http://localhost:6333 -openfga=http://localhost:8081 \
  -openfga-store=groundwork_local -tenant=acme -region=uk \
  -users=500 -docs=2000
```
This upserts `-docs` documents into Qdrant and grants each of `-users` users access to one
document (deterministically: user *i* → doc *i mod docs*), so the load run hits a realistic
mix of authorized and fail-closed queries.

**Load** the runtime:
```bash
go run ./cmd/loadtest -mode=load \
  -runtime=http://localhost:8080 -apikey=$GROUNDWORK_API_KEY \
  -jwt-secret=$GROUNDWORK_JWT_HS_SECRET -tenant=acme \
  -users=500 -concurrency=50 -duration=30s
```
Each request mints a signed end-user JWT, calls `POST /v1/query`, and the tool reports:
requests, req/s, allowed vs fail-closed counts, **fail-closed rate**, 429s, and latency
**p50/p95/p99/max**. (Run the **audit-timeout fix** + rate-limit PRs first, or you'll be
load-testing those gaps rather than the system.)

## `canary` — production smoke test

A scheduled safety net: verifies the live deployment and **exits non-zero** if a guarantee
breaks, so cron / CI / a scheduled agent can alert.
```bash
go run ./cmd/canary -runtime=https://gw.example.com \
  -apikey=$GROUNDWORK_API_KEY -jwt-secret=$GROUNDWORK_JWT_HS_SECRET \
  -authorized-user=alice@corp.test
```
Checks: `/healthz`; **fail-closed** (an unauthorized user must receive **zero** documents — a
leak fails the canary loudly); and, if `-authorized-user` is given, that the authorized path
returns documents. Wire it to a 5–15 min schedule and alert on a non-zero exit.

## Related: backend auth for non-bypassable deployment

To make "every retrieval goes through Groundwork" literally true, set these on the runtime so
the datastores **require** the runtime's secret (and firewall them to the runtime's network):

| Variable | Effect |
|---|---|
| `QDRANT_API_KEY` | runtime sends Qdrant's `api-key` header |
| `ELASTICSEARCH_API_KEY` | runtime sends `Authorization: ApiKey` |
| `OPENFGA_API_TOKEN` | runtime sends OpenFGA's `Authorization: Bearer` pre-shared key |

All optional; unset = current behavior.
