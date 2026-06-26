#!/usr/bin/env bash
# Tier 2 ONLY — seed the Acme corpus for LIVE RAG retrieval.
#
# This pushes real embeddings (via the SAME /embed service the runtime queries
# use) into Qdrant with document_id = "gh:<repo>", and writes the Acme org's
# OpenFGA tuples — including the deliberate engineering-team -> finance-budget
# overexposure the Leak Report flags. After this, the console "Try It" panel
# shows the exact leak being stripped live.
#
# PREREQUISITES:
#   * Tier 1 is up and you have done the runtime WARM-UP once (one Try-It query),
#     so the OpenFGA store + model exist. The seed RESOLVES the store; it does not
#     create it.
#   * The embedder (services/ingestion) and Elasticsearch are deployed, and the
#     runtime has QDRANT_URL + ELASTICSEARCH_URL + EMBEDDING_URL set.
#   * Go toolchain available locally (the seeder is a standalone Go module).
#
# REACHING INTERNAL SERVICES FROM YOUR LAPTOP:
#   OpenFGA and the embedder are internal-only on Fly. Open tunnels first:
#       fly proxy 8080:8080 -a groundwork-openfga      # -> http://localhost:8080
#       fly proxy 8000:8000 -a groundwork-embedder     # -> http://localhost:8000
#   Qdrant Cloud is public HTTPS (use your cluster URL + api key directly).
#
# USAGE (example with tunnels open):
#   export QDRANT_URL='https://<cluster-id>.<region>.aws.cloud.qdrant.io:6333'
#   export QDRANT_API_KEY='<your-qdrant-api-key>'   # exported so the seeder can auth
#   export OPENFGA_URL='http://localhost:8080'
#   export EMBEDDING_URL='http://localhost:8000'
#   bash deploy/seed-acme.sh
set -euo pipefail

: "${QDRANT_URL:?set QDRANT_URL to your Qdrant Cloud cluster URL}"
: "${OPENFGA_URL:?set OPENFGA_URL (e.g. http://localhost:8080 via fly proxy)}"
: "${EMBEDDING_URL:?set EMBEDDING_URL (e.g. http://localhost:8000 via fly proxy)}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}/examples/github-demo/seed"

echo "Seeding Acme corpus -> Qdrant (${QDRANT_URL}) and tuples -> OpenFGA (${OPENFGA_URL}) ..."
go run . \
  -qdrant="${QDRANT_URL}" \
  -openfga="${OPENFGA_URL}" \
  -embedding="${EMBEDDING_URL}" \
  -repos=../repos \
  -tenant=acme-financial \
  -region=US

echo "Done. Try: ask the 'engineering' user for the Q4 finance budget — it should be stripped."
