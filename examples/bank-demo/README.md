# Groundwork — Bank Demo

A bank-shaped sandbox for demonstrating Groundwork to investors and bank security buyers. Synthetic banking documents, a realistic 7-persona permission graph in OpenFGA, a Spring Boot bank tool exposing Temenos Transact-style endpoints, and a Java SDK that production callers can depend on.

**Wire-compatible** with the Temenos Transact REST API surface. No real Temenos data is used in the demo; the synthetic corpus is modeled on the schema so the integration path to a real Temenos deployment is a configuration change, not a rewrite.

---

## What's in this directory

```
examples/bank-demo/
├── README.md                      <- you are here (the runbook)
├── DEMO-SCRIPT.md                 <- the talking script for live demos
├── docker-compose.demo.yml        <- infra (Postgres, Qdrant, OpenFGA, embedder)
├── corpus/                        <- 18 synthetic banking documents
│   ├── credit-memos/              <- 5 credit memos
│   ├── kyc-packets/               <- 4 KYC packets
│   ├── loan-reviews/              <- 2 loan review memos
│   ├── policy-memos/              <- 4 policy memos
│   ├── audit-findings/            <- 3 audit findings
│   └── customer-correspondence/   <- 3 customer correspondences
├── personas/
│   └── personas.json              <- 7 personas + group/folder/document graph
├── seed/
│   ├── go.mod
│   └── main.go                    <- corpus + persona seeder (Go)
├── sdk/                           <- Groundwork Java SDK
│   ├── pom.xml
│   └── src/main/java/com/groundwork/sdk/...
└── java-client/                   <- Spring Boot bank tool
    ├── pom.xml
    └── src/main/java/com/groundwork/bankdemo/...
```

## Prerequisites

- Docker + Docker Compose (for infra)
- Go 1.23+ (to build and run the query-runtime + the seeder)
- JDK 17+ and Maven (to build and run the Spring Boot bank-client)
- `curl` and `jq` (for the demo commands)

## One-time setup

From the repo root:

```bash
# 1. Bring up infra (Postgres, Qdrant, OpenFGA, embedder)
cd examples/bank-demo
docker compose -f docker-compose.demo.yml up -d
docker compose -f docker-compose.demo.yml ps   # confirm all four are healthy
```

```bash
# 2. Build and run the Groundwork query-runtime in a separate terminal
cd services/query-runtime
export GROUNDWORK_API_KEY_BOOTSTRAP=gw_local_demo_bank_key
export GROUNDWORK_TENANT_BOOTSTRAP=demo_bank
export GROUNDWORK_REGION_BOOTSTRAP=uk
export GROUNDWORK_JWT_HS_SECRET="demo-only-replace-in-production"
export DATABASE_URL="postgres://groundwork:groundwork@localhost:5432/groundwork?sslmode=disable"
export QDRANT_URL=http://localhost:6333
export OPENFGA_URL=http://localhost:8081
export EMBEDDING_URL=http://localhost:9000
go run ./cmd/query-runtime
# Server listens on :8080.
```

```bash
# 3. Trigger OpenFGA store provisioning by sending one query, then run the seeder.
# (The runtime provisions the OpenFGA store + model lazily on the first /v1/query call.
# The seeder needs that store to exist before it can write tuples.)
curl -sS -X POST http://localhost:8080/v1/query \
  -H "Content-Type: application/json" \
  -H "X-Groundwork-API-Key: gw_local_demo_bank_key" \
  -H "X-Groundwork-User-Assertion: $(./scripts/mint-warmup-jwt.sh)" \
  -d '{"question":"warmup"}' >/dev/null

# Now seed:
cd examples/bank-demo/seed
go run . \
  -qdrant=http://localhost:6333 \
  -openfga=http://localhost:8081 \
  -corpus=../corpus \
  -personas=../personas/personas.json \
  -postgres="postgres://groundwork:groundwork@localhost:5432/groundwork?sslmode=disable"
# Expect: ~18 documents, ~60-80 chunks, ~120 OpenFGA tuples written,
# and ~18 demo.documents rows + per-doc grants for the Leak Report.
# (-postgres can be omitted on a Qdrant+OpenFGA-only smoke run; the
#  Leak Report won't have document attribution to join against.)
```

```bash
# 4. Start the Spring Boot bank-client in a third terminal
cd examples/bank-demo/java-client
mvn -q install -f ../sdk/pom.xml      # install the SDK into the local Maven repo
mvn -q spring-boot:run \
  -Dspring-boot.run.arguments="--bank-demo.personas-file=../personas/personas.json"
# Server listens on :9090.
```

You now have a working bank-demo stack:

| Service | Port | Purpose |
| --- | --- | --- |
| Postgres | 5432 | Audit log (immutable hash chain) |
| Qdrant | 6333 | Vector store |
| OpenFGA | 8081 | Authorization graph |
| Embedder | 9000 | Deterministic embedding service |
| Groundwork runtime | 8080 | The brain — REST + MCP + enforcement |
| Bank-demo client | 9090 | Internal bank tool (Temenos-style API) |

## Demo commands

Switch persona via the `X-Persona` header; the bank-client mints the per-persona JWT.

```bash
# Tony (RM London, assigned to Stark) — should see the executive-restricted Stark credit memo
curl -sS http://localhost:9090/holdings/loans/search \
  -G --data-urlencode "q=Stark Industries credit memo" \
  -H "X-Persona: rm_tony" | jq '.body.loanRecords[] | .document_id'
# Expect: ["CM-2026-002"]

# Natasha (RM NYC, different portfolio) — should NOT see Stark
curl -sS http://localhost:9090/holdings/loans/search \
  -G --data-urlencode "q=Stark Industries credit memo" \
  -H "X-Persona: rm_natasha" | jq '.body | {loanRecords, blockedByAcl}'
# Expect: {"loanRecords": [], "blockedByAcl": >=1}

# Junior teller — should NOT see executive compensation policy
curl -sS http://localhost:9090/demo/query \
  -H "X-Persona: teller_jane" \
  -H "Content-Type: application/json" \
  -d '{"question":"executive compensation framework"}' | jq '.citations[] | .document_id'
# Expect: []

# CEO — should see executive compensation policy
curl -sS http://localhost:9090/demo/query \
  -H "X-Persona: exec_starkceo" \
  -H "Content-Type: application/json" \
  -d '{"question":"executive compensation framework"}' | jq '.citations[] | .document_id'
# Expect: ["POL-EXEC-COMP-2026"]

# Auditor — should see audit findings, including the meta one about the AI access controls
curl -sS http://localhost:9090/demo/query \
  -H "X-Persona: auditor_logan" \
  -H "Content-Type: application/json" \
  -d '{"question":"AI access controls audit finding"}' | jq '.citations[] | .document_id'
# Expect: ["AUDIT-2026-INTERNAL-ACCESS"]
```

## Running the demo on a screen

See `DEMO-SCRIPT.md` for the exact talking script. The five-minute version: switch personas, watch what changes, finish on the immutable audit trace.

## Verifying the audit chain

```bash
cd services/query-runtime
DATABASE_URL="postgres://groundwork:groundwork@localhost:5432/groundwork?sslmode=disable" \
  go run ./cmd/audit-verify
# Expect: "audit chain verified, N entries, no gaps, no tampering".
```

## Tearing down

```bash
cd examples/bank-demo
docker compose -f docker-compose.demo.yml down -v
```

---

## On the Temenos compatibility claim

The Spring Boot bank-client exposes endpoints in the shape of Temenos Transact's public REST surface — `GET /party/customers/{id}/documents`, `GET /holdings/loans/search`. The synthetic corpus uses field names consistent with Temenos schemas (`customerMnemonic`, numeric customer IDs, `accountOfficerId`).

**No live Temenos sandbox data is used in this demo.** Temenos's developer-portal terms of use prohibit using their sandbox in marketing or demonstration material, so the demo runs on a synthetic corpus modeled on the schema. The integration path to a real Temenos deployment in a customer environment is straightforward: replace the seeder's corpus loader with a Temenos API pull connector that retrieves documents through the customer's licensed Temenos environment.

## On the data

Every document carries the watermark **"SYNTHETIC — DEMO USE ONLY — NOT REAL BANK DATA"** in its frontmatter and in the rendered body. No customer name, account number, or transaction in this corpus is real.
