# Groundwork — Live Demo Deployment

Stand up a public, working Groundwork demo for the YC / Antler application. Three
tiers, each a superset of the one before. Start at Tier 0 today for an instant
link; add Tier 1 for the live product core; add Tier 2 for live RAG retrieval.

| Tier | What's live | Needs | Time |
|------|-------------|-------|------|
| **0** | Full Acme UX from curated data (identical to live output) | Vercel | ~5 min |
| **1** | Connect, Leak Report, Audit timeline + verify, ACL enforcement — **100% computed live** | + Supabase, Fly | ~30–45 min |
| **2** | Everything above **+ live RAG retrieval** (Try-It strips the real `gh:finance-budget` leak) | + Qdrant, Elasticsearch, embedder | +30–60 min |

> The product's whole thesis — runtime authorization + tamper-evident audit —
> is fully live at **Tier 1**. Retrieval (Qdrant) is the Tier 2 layer. Don't let
> Tier 2 block your link going out.

---

## Hosting — free vs. paid (pick the runtime's home)

The console (Vercel), Postgres (Supabase), and vectors (Qdrant Cloud) are all free
and **card-free**. The only piece that needs a compute host is the backend.

| Host | Free? | Card? | Fit |
|------|-------|-------|-----|
| **Render** | yes (750 hrs/mo) | **no card** | ✅ Recommended free path — Docker-native |
| Koyeb | yes | no card | ⚠️ only 1 free service/org |
| Northflank | yes (always-on) | **card required** | ❌ |
| Fly.io | trial | **card required** | paid path (configs in `deploy/fly/`, `services/query-runtime/fly.toml`) |

### Recommended FREE path — Render, no credit card

One **self-bootstrapping container** runs the whole backend: it applies the DB
migrations to Supabase, starts OpenFGA on localhost (Supabase datastore, never
public), then starts the runtime. One free service, no manual migration step.

1. Push the repo to GitHub.
2. Render → **New → Blueprint** → select the repo. Render reads
   `deploy/render/render.yaml` and provisions the service from
   `services/query-runtime/Dockerfile.allinone`.
3. When prompted, fill the four secrets (stored by Render, never in git):
   - `DATABASE_URL` — Supabase **session pooler** URI (`:5432`), `?sslmode=require`
   - `GROUNDWORK_JWT_HS_SECRET` — `openssl rand -hex 32` (use the SAME value on Vercel)
   - `BOOTSTRAP_API_KEY` — `gw_live_<random>` (= `GROUNDWORK_API_KEY` on Vercel)
   - `IMMUTABLE_AUDIT_SALT` — `openssl rand -hex 32` (set once, never change)
4. Deploy. First boot runs migrations + OpenFGA bootstrap (~30–60s), then health
   goes green at `/healthz`. Your runtime URL is `https://groundwork-runtime.onrender.com`.
5. Point Vercel at it (Tier 1, step 5 below) and do the **warm-up → Connect** order (step 6).

You skip the OpenFGA Fly app, `migrate.sh`, and all the `fly …` commands — the
container handles them. Then continue at **Tier 1, step 5**.

**Cold start:** free services sleep after ~15 min idle and wake in ~30–60s. The Vercel
link always works (it shows demo data while the backend wakes). Before a live
walkthrough, open `…onrender.com/healthz` once to warm it — or keep it warm with a
free no-card cron (e.g. cron-job.org) hitting `/healthz` every ~10 min; one always-on
service fits the 750 hrs/month budget.

**Qdrant on free hosting:** Tier 2 (live retrieval) needs Elasticsearch + an embedder,
which are RAM-heavy and don't fit a 512 MB free instance — that step waits until you
have a small paid box. Your Qdrant cluster isn't wasted; it's the Tier 2 vector store.
The free Render path delivers Tier 1 (authz + audit + leak report fully live).

---

## Secrets — set once, never commit, never paste in chat

| Secret | Where it lives | Shape / notes |
|--------|----------------|---------------|
| `DATABASE_URL` | Fly (runtime) | Supabase **session pooler** (`:5432`) or direct conn, `?sslmode=require` |
| `OPENFGA_DATASTORE_URI` | Fly (openfga) | Same Supabase DB, same session-pooler rule |
| `GROUNDWORK_JWT_HS_SECRET` | Fly (runtime) **and** Vercel | 32+ random bytes, **identical in both** |
| `BOOTSTRAP_API_KEY` | Fly (runtime) = `GROUNDWORK_API_KEY` on Vercel | e.g. `gw_live_<random>` |
| `IMMUTABLE_AUDIT_SALT` | Fly (runtime) | random; **set once and never change** (audit digest input) |
| `QDRANT_API_KEY` | Fly (runtime), Tier 2 | from Qdrant Cloud |
| `GITHUB_TOKEN` | Fly (runtime), optional | omit → offline Acme MockClient (recommended for the demo) |

Generate a good random value: `openssl rand -hex 32`.

---

## Tier 0 — instant public link (no backend)

1. Push the repo to GitHub (private is fine).
2. In Vercel: **Add New → Project → Import** the repo.
3. Set **Root Directory = `apps/console`**. Leave all env vars empty.
4. **Deploy.**

You get `https://<project>.vercel.app` with the complete Acme story — Connect
graph, Leak Report, Audit timeline, Try-It allow/deny — served from the curated
fallbacks in `apps/console/app/api/*`. This is your application link. Upgrade the
same project to live data by adding the Tier 1 env vars later (no re-import).

---

## Tier 1 — live product core (Supabase + OpenFGA + runtime)

> **No credit card?** Use the **Recommended FREE path (Render)** above — it replaces
> steps 2–4 here (migrations, OpenFGA, runtime) with one Blueprint deploy, then rejoin
> at step 5. The Fly steps below are the paid alternative.

Prereqs (Fly path): a [Fly.io](https://fly.io) account + `flyctl`, and your Supabase project.

### 1. Get your Supabase connection string
Supabase → Project Settings → Database → **Connection string → Session pooler**.
Append `?sslmode=require`. Use this same value for both `DATABASE_URL` (runtime)
and `OPENFGA_DATASTORE_URI` (openfga).

### 2. Apply the app migrations (003–013)
```bash
export DATABASE_URL='postgresql://postgres.<ref>:<pw>@aws-0-<region>.pooler.supabase.com:5432/postgres?sslmode=require'
bash deploy/migrate.sh
```
Creates `audit_log`, `audit_log_decisions`, `principal_aliases`, and the demo
schema. (Migration 013 builds indexes `CONCURRENTLY` — that's why a session/direct
connection is required, not the transaction pooler.)

### 3. Deploy OpenFGA (internal-only, Supabase datastore)
```bash
fly launch --no-deploy --copy-config --config deploy/fly/openfga/fly.toml \
  --name groundwork-openfga --region iad         # pick a region near Supabase

export OPENFGA_DATASTORE_URI="$DATABASE_URL"
fly secrets set -a groundwork-openfga OPENFGA_DATASTORE_URI="$OPENFGA_DATASTORE_URI"

# one-shot: create OpenFGA's own tables, then exit
fly machine run openfga/openfga:latest migrate --rm -a groundwork-openfga \
  -e OPENFGA_DATASTORE_ENGINE=postgres -e OPENFGA_DATASTORE_URI="$OPENFGA_DATASTORE_URI"

fly deploy --config deploy/fly/openfga/fly.toml
```

### 4. Deploy the runtime
```bash
cd services/query-runtime
fly launch --no-deploy --copy-config --name groundwork-runtime --region iad   # SAME region as openfga

fly secrets set -a groundwork-runtime \
  DATABASE_URL="$DATABASE_URL" \
  GROUNDWORK_JWT_HS_SECRET="$(openssl rand -hex 32)" \
  BOOTSTRAP_API_KEY="gw_live_$(openssl rand -hex 12)" \
  IMMUTABLE_AUDIT_SALT="$(openssl rand -hex 32)"

fly deploy
cd ../..
```
Save the JWT secret and API key — Vercel needs the exact same values. Confirm
health: `curl https://groundwork-runtime.fly.dev/healthz` → `ok`.

### 5. Point the console at the runtime
In your Vercel project, set (Production):
```
QUERY_RUNTIME_URL=https://groundwork-runtime.fly.dev
GROUNDWORK_API_KEY=<the BOOTSTRAP_API_KEY from step 4>
GROUNDWORK_JWT_HS_SECRET=<the JWT secret from step 4>
NEXT_PUBLIC_APP_NAME=Groundwork
```
Redeploy the Vercel project.

### 6. Warm up, then Connect  ⚠️ order matters
On a fresh store the runtime provisions OpenFGA (store + authorization model +
default memberships) lazily, on the **first ACL check**. The Connect flow only
*resolves* the store, so it must run second:

1. Open the console → **Try It** → run any question once. *(This triggers the
   runtime to create the OpenFGA store + model.)*
2. Now click **Connect**. It writes the Acme org's tuples (including the planted
   engineering → finance-budget leak).
3. **Leak Report** and the **Audit** timeline are now fully live and computed.

> Want first-click Connect with no warm-up? I can add an eager OpenFGA warm-up at
> runtime boot (small, safe, additive) — see "Recommended hardening" below.

### Tier 1 verification checklist
- `GET /healthz` → `ok`; `GET /readyz` → `ok` (Postgres reachable).
- Console **Connect** returns the 5 teams / 5 documents / tuple count from live OpenFGA.
- **Leak Report** shows `cross_department_access` (high) for engineering → finance-budget.
- **Audit** timeline lists real rows; **Verify** returns chain-intact.
- In Supabase, `select count(*) from audit_log;` grows as you run Try-It.

---

## Tier 2 — live RAG retrieval (Qdrant + Elasticsearch + embedder)

The engine fuses **Qdrant** (vector) and **Elasticsearch** (BM25) and embeds
queries via the **embedder** (`services/ingestion`). All three are required for
live retrieval — `main.go` only activates the HTTP backend when both
`QDRANT_URL` and `ELASTICSEARCH_URL` are set.

1. **Embedder** → deploy `services/ingestion` to Fly (internal-only, exposes `:8000`).
2. **Elasticsearch** → Elastic Cloud free trial, or a Fly ES app (internal `:9200`).
3. Add to the runtime and redeploy:
   ```bash
   fly secrets set -a groundwork-runtime QDRANT_API_KEY='<qdrant-cloud-key>'
   # then uncomment the Tier 2 [env] block in services/query-runtime/fly.toml:
   #   QDRANT_URL, QDRANT_COLLECTION, ELASTICSEARCH_URL, ELASTICSEARCH_INDEX, EMBEDDING_URL
   fly deploy -c services/query-runtime/fly.toml
   ```
4. **Seed** the Acme corpus (real embeddings → Qdrant, tuples → OpenFGA):
   ```bash
   fly proxy 8080:8080 -a groundwork-openfga &     # tunnel to internal OpenFGA
   fly proxy 8000:8000 -a groundwork-embedder &    # tunnel to internal embedder
   export QDRANT_URL='https://<cluster>.<region>.aws.cloud.qdrant.io:6333'
   export QDRANT_API_KEY='<qdrant-cloud-key>'
   export OPENFGA_URL='http://localhost:8080' EMBEDDING_URL='http://localhost:8000'
   bash deploy/seed-acme.sh
   ```
5. In the console, ask the **engineering** user for the Q4 finance budget — the
   `gh:finance-budget` chunk is retrieved by Qdrant but **stripped live by GW**,
   and the denial appears in the audit trail. That's the money demo.

---

## Troubleshooting

- **Connect → "store not found; provision via query-runtime first"** — you skipped
  the warm-up. Run one Try-It query, then Connect (Tier 1, step 6).
- **Console shows demo data after wiring Tier 1** — `GROUNDWORK_API_KEY` /
  `GROUNDWORK_JWT_HS_SECRET` on Vercel don't match the runtime, or
  `QUERY_RUNTIME_URL` is wrong. The console silently falls back to demo on auth
  failure by design.
- **Migration 013 fails / "CREATE INDEX CONCURRENTLY cannot run in a transaction"**
  — you're on the Supabase transaction pooler (`:6543`). Use the session pooler
  (`:5432`) or direct connection.
- **Everyone sees zero documents after an OpenFGA restart** — OpenFGA is on the
  `memory` datastore. Confirm `OPENFGA_DATASTORE_ENGINE=postgres` and that the
  `migrate` one-shot ran.
- **Runtime can't reach OpenFGA** — both Fly apps must be in the same org/region;
  the runtime uses `http://groundwork-openfga.internal:8080` (Fly private net).

## Recommended hardening (offered)
- **Eager OpenFGA warm-up at boot** — removes the warm-up-before-Connect ordering
  so a first-time visitor can Connect immediately. Small, additive, no change to
  the hash chain / fail-closed behavior.
- **Make OpenFGA fully private** — already internal-only here; keep it that way in
  prod (never give the OpenFGA app a public service).
