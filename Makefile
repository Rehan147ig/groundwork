# Groundwork developer UX. One command per intent.
#
# NOTE: This Makefile is created in feat/msgraph-pilot-scaffold. No Makefile
# existed on the base branch; this file declares the targets the project's
# acceptance gates and docs reference (`make demo`, `make up`, `make down`,
# `make seed`, `make verify`) and adds the new `make connector-enumerate`
# target for the Microsoft Graph pilot scaffold.

SHELL := /bin/bash

# Two compose stacks:
#   - DEV     = base dev profile (used by `make up` / `make demo` for the bank demo)
#   - PILOT   = base + codespace override (the codespace override defines the
#               msgraph-connector service under the `pilot` profile)
COMPOSE_DEV   := docker compose -f infra/docker-compose.yml
COMPOSE_PILOT := docker compose -f infra/docker-compose.yml -f infra/docker-compose.codespace.yml

.PHONY: help demo up down clean seed verify connector-enumerate

help:                                ## Show this help
	@echo "Groundwork targets:"
	@echo "  make demo                  Bring stack up, seed bank demo, run verify.sh"
	@echo "  make up                    Bring the dev stack up (background)"
	@echo "  make down                  Stop the dev stack"
	@echo "  make clean                 Stop + wipe volumes + remove local images"
	@echo "  make seed                  Seed the synthetic bank corpus + persona graph"
	@echo "  make verify                Run examples/bank-demo/verify.sh"
	@echo "  make connector-enumerate   Run the MS Graph connector scaffold (pilot profile)"

# -- Dev / bank-demo flow --------------------------------------------------------------

up:                                  ## Bring the dev stack up
	$(COMPOSE_DEV) up -d --build

down:                                ## Stop the dev stack (containers only)
	$(COMPOSE_DEV) down

clean:                               ## Stop + wipe volumes + remove local images
	$(COMPOSE_DEV) down --volumes --rmi local

seed:                                ## Seed the bank demo corpus + persona graph
	cd examples/bank-demo/seed && go run . \
	  -qdrant=http://localhost:6333 \
	  -openfga=http://localhost:8081 \
	  -corpus=../corpus \
	  -personas=../personas/personas.json

verify:                              ## Persona acceptance gate (the keystone moments)
	bash examples/bank-demo/verify.sh

demo: up seed verify                 ## End-to-end: stack + seed + verify
	@echo "✅ Demo ready"

# -- MS Graph pilot (scaffold) ---------------------------------------------------------

# Bring up the connector only — it runs under the `pilot` Compose profile, so it is
# explicitly NOT started by `make up` or `make demo`. The scaffold validates four env
# vars and exits 0; subsequent PRs add the real sync logic.
connector-enumerate:                 ## Run the MS Graph connector (pilot profile)
	$(COMPOSE_PILOT) --profile pilot run --rm msgraph-connector
