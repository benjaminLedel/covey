# Covey — Entwicklungs-Workflow. `make dev-db && make bootstrap && make run`.

GO ?= go
DB_URL ?= postgres://covey:covey@localhost:5433/covey?sslmode=disable

.PHONY: build web test test-integration run bootstrap dev-db sandbox-image egress-image clean skill-sync

# npm ci statt npm install — bewusst: es installiert exakt den Lockfile und
# schreibt ihn nie um. npm install auf macOS wirft die Linux-/wasm-Zweige
# (@emnapi, tailwind-oxide-wasm) aus dem Lockfile, die der Container-Build
# braucht; danach scheitert npm ci in CI und Dockerfile. Dieselbe Zeile wie
# dort — was hier baut, baut auch da.
web:
	cd web && npm ci && npm run build

# Skills: skills/ ist die Quelle (ins Binary eingebettet, per Download angeboten);
# die Kopie unter .claude/skills/ hält Claude Code im Repo selbst aktuell.
skill-sync:
	@for d in skills/*/; do name=$$(basename $$d); mkdir -p .claude/skills/$$name && cp -f $$d/* .claude/skills/$$name/; done

build: web skill-sync
	$(GO) build -o covey ./cmd/covey
	$(GO) build -o coveyd ./cmd/coveyd

# Postgres mit pgvector für Entwicklung und Tests (Port 5433).
dev-db:
	docker run -d --name covey-postgres \
		-e POSTGRES_USER=covey -e POSTGRES_PASSWORD=covey -e POSTGRES_DB=covey \
		-p 5433:5432 pgvector/pgvector:pg16 || docker start covey-postgres

bootstrap: build
	@test -f .covey.key || ./covey genkey > .covey.key
	COVEY_MASTER_KEY=$$(cat .covey.key) ./covey bootstrap

run: build
	COVEY_MASTER_KEY=$$(cat .covey.key) ./covey serve

# Sandbox-Image für COVEY_SANDBOX_PROVIDER=docker (coveyd + Claude Code).
sandbox-image:
	docker build -f Dockerfile.sandbox -t covey-sandbox:latest .

# Egress-Proxy-Image für COVEY_EGRESS_ISOLATION=network (harte Netz-Isolation).
egress-image:
	docker build -f Dockerfile.egress -t covey-egress:latest .

# Unit-Tests (ohne DB) + Integrationstests (brauchen dev-db).
test:
	$(GO) vet ./...
	$(GO) test ./...

test-integration:
	$(GO) test ./internal/integration/ -v

clean:
	rm -f covey coveyd
	rm -rf data web/dist
