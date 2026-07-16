# Covey — Entwicklungs-Workflow. `make dev-db && make bootstrap && make run`.

GO ?= go
DB_URL ?= postgres://covey:covey@localhost:5433/covey?sslmode=disable

.PHONY: build web test test-integration run bootstrap dev-db sandbox-image clean

web:
	cd web && npm install && npm run build

build: web
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
	COVEY_MASTER_KEY=$$(cat .covey.key) COVEY_COVEYD_PATH=$$PWD/coveyd ./covey serve

# Sandbox-Image für COVEY_SANDBOX_PROVIDER=docker (coveyd + Claude Code).
sandbox-image:
	docker build -f Dockerfile.sandbox -t covey-sandbox:latest .

# Unit-Tests (ohne DB) + Integrationstests (brauchen dev-db).
test:
	$(GO) vet ./...
	$(GO) test ./...

test-integration:
	$(GO) test ./internal/integration/ -v

clean:
	rm -f covey coveyd
	rm -rf data web/dist
