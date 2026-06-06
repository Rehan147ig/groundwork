# Deploy & verify Groundwork in GitHub Codespaces

This runs the **real** Groundwork stack (runtime + Qdrant + OpenFGA + ingestion embedder)
against your dedicated **Supabase** project for OpenFGA's datastore, then seeds the
synthetic bank demo and verifies enforcement.

## What Codespaces is good for here

- ✅ **Proving it works** end-to-end: per-persona allow/deny, fail-closed, audit chain.
- ✅ A **shareable `*.app.github.dev` URL** while the Codespace is running — good for a
  live demo or a screen recording.
- ❌ **Not an always-on host.** A Codespace stops after ~30 min idle, the URL dies when it
  stops, and the free quota is ~60 core-hours/month (2-core) / ~30 (4-core). Use it for
  sessions; stop it when done. For an always-on link, graduate to a small VM later.
- ⚠️ **Latency here is not real performance.** Your Supabase project is in ap-southeast-1
  (Singapore); the Codespace is in its own region, so every OpenFGA check is a cross-region
  round trip. Use this to prove correctness, not to quote a p95.

## Prerequisites

- This branch (with `.devcontainer/`, `infra/`, `examples/bank-demo/`) pushed to GitHub.
- Your Supabase **session-pooler** connection string (Supabase dashboard → Connect →
  Session pooler, port 5432), with `?sslmode=require` appended, and the DB password you set.

## Steps

### 1. Open the Codespace
GitHub repo → **Code ▸ Codespaces ▸ Create codespace on this branch**. Pick a **4-core /
16 GB** machine (Elasticsearch + the embedder model need the headroom). The devcontainer
auto-sets `vm.max_map_count` so Elasticsearch can start.

### 2. Set the OpenFGA → Supabase connection (secret)
In the Codespace terminal (replace with your real session-pooler URI):
```bash
export OPENFGA_DATASTORE_URI="postgresql://postgres.gryziguuygznwlbdkvye:YOUR_PASSWORD@aws-0-ap-southeast-1.pooler.supabase.com:5432/postgres?sslmode=require"
```
(For a persistent value across rebuilds, add it as a **Codespaces secret** in repo settings
instead of exporting each time.)

### 3. Bring up the stack
```bash
docker compose -f infra/docker-compose.yml -f infra/docker-compose.codespace.yml up --build -d
```
First build takes a few minutes (Go build, Next build, ES + embedder model download). Watch
progress:
```bash
docker compose -f infra/docker-compose.yml -f infra/docker-compose.codespace.yml logs -f query-runtime openfga-migrate
```
`openfga-migrate` should run once and exit 0 (it created OpenFGA's tables in Supabase).

### 4. Provision the OpenFGA store, then seed the bank demo
The runtime provisions the OpenFGA store + model on its first query, so make one warm-up
call, then seed:
```bash
# warm-up (provisions the store in Supabase)
curl -s localhost:8080/v1/query -H "X-Groundwork-API-Key: gw_local_acme_key" \
  -H "Content-Type: application/json" -d '{"question":"warm up"}' >/dev/null

# seed the synthetic bank corpus + persona graph
# (the seeder embeds each chunk via the ingestion service on :8090 — make sure the
#  `ingestion` container is up/healthy first: docker compose ... ps)
go run ./examples/bank-demo/seed -qdrant=http://localhost:6333 -openfga=http://localhost:8081
```
The seeder's other inputs use defaults: `-corpus=./examples/bank-demo/corpus`,
`-personas=./examples/bank-demo/personas/personas.json`, `-embedding=http://localhost:8090`
(tenant/region come from `personas.json`; the store name is fixed to `groundwork_local`).

### 5. Make the runtime public + verify
- In the **Ports** tab, set port **8080** visibility to **Public** to get your shareable URL.
- Run the keystone checks (the teller must be blocked, the CEO allowed):
```bash
bash examples/bank-demo/verify.sh   # (added with the test pack) — or use the curls in DEMO-SCRIPT.md
```

## Troubleshooting (the things I expect on first run)

| Symptom | Cause / fix |
|---|---|
| Elasticsearch container unhealthy / exits | `vm.max_map_count` not set → run `sudo sysctl -w vm.max_map_count=262144` and `up` again |
| Every query returns 0 docs (even authorized) | OpenFGA can't reach Supabase (paused project, wrong pooler, missing `sslmode=require`) → check `openfga` logs; restore the Supabase project if paused |
| `openfga-migrate` fails | Wrong connection string — use the **Session** pooler (5432), not Transaction (6543); URL-encode the password |
| Authorized queries return 0 / wrong docs | Retrieval relevance — the seeder's embedding path; tell me and I'll fix the seeder |
| Slow responses | Expected — cross-region DB. Not GW's real latency. |

## When you're done
Stop the Codespace (it auto-stops on idle, but stop it manually to save quota). The Supabase
data persists; Qdrant is ephemeral, so re-seed (step 4) next session.
