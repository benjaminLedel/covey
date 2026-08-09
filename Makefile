# Covey — development workflow. `make dev-db && make bootstrap && make run`.

GO ?= go
DB_URL ?= postgres://covey:covey@localhost:5433/covey?sslmode=disable

# Provenance of the binary (internal/buildinfo): `covey version`, the startup log
# and the foot of the UI show it. Taken from Git, overridable by variable.
VERSION ?= $(shell git describe --tags --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X covey/internal/buildinfo.version=$(VERSION) \
           -X covey/internal/buildinfo.commit=$(COMMIT) \
           -X covey/internal/buildinfo.date=$(DATE)

.PHONY: build web test test-integration run bootstrap dev-db sandbox-image egress-image clean skill-sync

# npm ci instead of npm install — deliberately: it installs exactly the lockfile
# and never rewrites it. npm install on macOS throws the Linux/wasm branches
# (@emnapi, tailwind-oxide-wasm) out of the lockfile that the container build
# needs; after that npm ci fails in CI and in the Dockerfile. The same line as
# there — what builds here builds there.
web:
	cd web && npm ci && npm run build

# Skills: skills/ is the source (embedded in the binary, offered for download);
# the copy under .claude/skills/ keeps Claude Code current in the repo itself.
skill-sync:
	@for d in skills/*/; do name=$$(basename $$d); mkdir -p .claude/skills/$$name && cp -f $$d/* .claude/skills/$$name/; done

build: web skill-sync
	$(GO) build -ldflags "$(LDFLAGS)" -o covey ./cmd/covey
	$(GO) build -ldflags "$(LDFLAGS)" -o coveyd ./cmd/coveyd

# Postgres with pgvector for development and tests (port 5433).
dev-db:
	docker run -d --name covey-postgres \
		-e POSTGRES_USER=covey -e POSTGRES_PASSWORD=covey -e POSTGRES_DB=covey \
		-p 5433:5432 pgvector/pgvector:pg16 || docker start covey-postgres

bootstrap: build
	@test -f .covey.key || ./covey genkey > .covey.key
	COVEY_MASTER_KEY=$$(cat .covey.key) ./covey bootstrap

run: build
	COVEY_MASTER_KEY=$$(cat .covey.key) ./covey serve

# The sandbox image agents work in (coveyd + Claude Code + chromium). Needed
# before the first wake, not before the first start: `covey serve` comes up
# without it and says at startup that it is missing.
sandbox-image:
	docker build -f Dockerfile.sandbox \
		--build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg DATE=$(DATE) \
		-t covey-sandbox:latest .

# Egress proxy image for COVEY_EGRESS_ISOLATION=network (hard network isolation).
egress-image:
	docker build -f Dockerfile.egress -t covey-egress:latest .

# Unit tests (without DB) + integration tests (need dev-db).
test:
	$(GO) vet ./...
	$(GO) test ./...

test-integration:
	$(GO) test ./internal/integration/ -v

clean:
	rm -f covey coveyd
	rm -rf data web/dist
