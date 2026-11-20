# PACT MVP 0.1. Run everything from the repository root.
COMPOSE  := docker compose
OFFLINE  := docker compose -f compose.yaml -f deploy/docker-compose.offline.yml
API      := http://127.0.0.1:8080

.PHONY: help up up-kms build up-offline down clean logs ps test test-go test-agent web-dev web-build \
        bypass-test replay scoreboard scoreboard-live scoreboard-supervisor verify export tamper truncate untamper reset

help:
	@echo "make up              start the whole demo, building images if missing (dashboard on $(API))"
	@echo "make build           rebuild the images after changing code (needs about 4 GB of free disk)"
	@echo "make up-kms          same, with the org and issuer keys held in Google Cloud KMS"
	@echo "make up-offline      same, with a scripted stand-in model instead of the Gemini API"
	@echo "make down            stop it (make clean also deletes the volumes)"
	@echo "make test            Go tests, agent tests, dashboard type-check and build"
	@echo "make bypass-test     prove the agent has no route around the broker (run before every demo)"
	@echo "make replay          run scenarios S1-S8, T1, T2 through the scripted agent and check each decision"
	@echo "make scoreboard      with/without PACT, scripted agent (ARGS=--quick for one phrasing each)"
	@echo "make scoreboard-live with/without PACT, live Gemini agent (needs GEMINI_API_KEY)"
	@echo "make verify          ask pact-core to walk the evidence chain"
	@echo "make tamper [SEQ=n]  edit a stored evidence record in PostgreSQL, then run make verify"
	@echo "make truncate        delete the newest evidence record, then run make verify"
	@echo "make untamper        put the edited record(s) back, for rehearsals"
	@echo "make export          save the chain and verify it offline with the pact-core binary"
	@echo "make reset           restore the repository and clear the targets"

# `up` builds images only when they are missing; run `make build` after changing code.
up:
	$(COMPOSE) up -d --wait --remove-orphans
	@echo "Dashboard: $(API)"

up-offline:
	$(OFFLINE) up -d --wait
	@echo "Dashboard: $(API)  (scripted stand-in model, not Gemini)"

# Org and issuer keys held in Google Cloud KMS. See README, "Cloud KMS".
up-kms:
	docker compose -f compose.yaml -f deploy/docker-compose.kms.yml up -d --wait --remove-orphans
	@curl -fsS $(API)/v1/keys | python3 -c 'import json,sys; k=json.load(sys.stdin); print("key backends:", k["backends"], "| evidence key certified:", k["evidence_certified"])'

build:
	$(OFFLINE) build

down:
	$(OFFLINE) down --remove-orphans

clean:
	$(OFFLINE) down --remove-orphans --volumes

logs:
	$(COMPOSE) logs -f --tail=100

ps:
	$(COMPOSE) ps

test: test-go test-agent web-build

test-go:
	go vet ./...
	go test ./...

test-agent:
	$(COMPOSE) run --rm --no-deps -T -v "$(CURDIR)/agent/tests:/agent/tests:ro" --entrypoint python agent -m unittest discover -s tests -v

web-dev:
	pnpm --filter web dev

web-build:
	pnpm install
	pnpm --filter web typecheck
	pnpm --filter web build

bypass-test:
	@sh scripts/bypass-test.sh

replay:
	@python3 scripts/pact.py replay $(ARGS)

scoreboard:
	@python3 scripts/pact.py scoreboard --agent replay $(ARGS)

scoreboard-live:
	@python3 scripts/pact.py scoreboard --agent live $(ARGS)

# Three columns: unprotected, AI supervisor, PACT. Needs GEMINI_API_KEY.
scoreboard-supervisor:
	@python3 scripts/pact.py scoreboard --agent replay --supervisor $(ARGS)

verify:
	@python3 scripts/pact.py verify

export:
	@curl -fsS $(API)/v1/evidence/export -o pact-evidence.json
	@go run ./cmd/pact-core verify pact-evidence.json

tamper:
	@sh scripts/tamper.sh $(SEQ)

truncate:
	@sh scripts/tamper.sh truncate

untamper:
	@sh scripts/tamper.sh restore

reset:
	@curl -fsS -X POST -H 'X-PACT-Client: cli' $(API)/v1/demo/reset -d '{}' && echo
