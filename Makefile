# covey — development workflow. `make dev-db && make bootstrap && make run`.

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

.PHONY: build build-nopack web test test-integration run bootstrap dev-db sandbox-image sandbox-image-dev sandbox-image-dev-flutter sandbox-image-dev-php sandbox-image-dev-web sandbox-image-dev-full sandbox-images sandbox-images-pull upgrade runner egress-image clean skill-sync

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

# The same build without the bundled plugin pack (spec/22). The binary then
# carries no target system of its own and takes them from the catalogue —
# roughly 3 MB smaller, and above all nothing to wait for a release over. For
# an instance that uses none of the compiled systems, or only catalogue ones.
build-nopack: web skill-sync
	$(GO) build -tags nopack -ldflags "$(LDFLAGS)" -o covey ./cmd/covey
	$(GO) build -tags nopack -ldflags "$(LDFLAGS)" -o coveyd ./cmd/coveyd

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
#
# The image hangs off the agent (spec/16), and there are three kinds of them:
# `base` carries what every agent needs, `dev` the union of the developer
# toolchains, and the role workplaces below one field each. A mail agent
# therefore carries no JVM, and a Flutter agent no database server.
sandbox-image:
	docker build -f Dockerfile.sandbox \
		--build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg DATE=$(DATE) \
		-t covey-sandbox:latest .

sandbox-image-dev: sandbox-image
	docker build -f Dockerfile.sandbox.dev \
		--build-arg BASE_IMAGE=covey-sandbox:latest \
		-t covey-sandbox-dev:latest .

# The role workplaces: base plus the toolchain of ONE field. For an agent whose
# field is settled — it carries the same tools as in `dev` and not the three it
# never calls. Each one builds on base, none on `dev`; a role image derived from
# the union would save nothing.
sandbox-image-dev-flutter: sandbox-image
	docker build -f Dockerfile.sandbox.dev-flutter \
		--build-arg BASE_IMAGE=covey-sandbox:latest \
		-t covey-sandbox-dev-flutter:latest .

sandbox-image-dev-php: sandbox-image
	docker build -f Dockerfile.sandbox.dev-php \
		--build-arg BASE_IMAGE=covey-sandbox:latest \
		-t covey-sandbox-dev-php:latest .

sandbox-image-dev-web: sandbox-image
	docker build -f Dockerfile.sandbox.dev-web \
		--build-arg BASE_IMAGE=covey-sandbox:latest \
		-t covey-sandbox-dev-web:latest .

# The all-rounder: dev plus the Flutter SDK in the image. On `dev` and not on
# base, because that is what it is — everything in dev, and nothing fetched into
# the home first. The largest of the workplaces, and the one to take if you do
# not want to choose one per agent.
sandbox-image-dev-full: sandbox-image-dev
	docker build -f Dockerfile.sandbox.dev-full \
		--build-arg BASE_IMAGE=covey-sandbox-dev:latest \
		-t covey-sandbox-dev-full:latest .

# Every workplace at once — several gigabytes and the better part of an hour.
# An installation rarely needs all of them: build the profiles your agents
# actually stand in, or pull them (sandbox-images-pull).
sandbox-images: sandbox-image-dev sandbox-image-dev-flutter sandbox-image-dev-php sandbox-image-dev-web sandbox-image-dev-full

# The same images, but pulled instead of built: GitHub builds and publishes them
# on every push and every release (.github/workflows/sandbox-images.yml).
# Minutes instead of a multi-gigabyte build, and the only way that works at all
# where there is no checkout — a container installation sets
# COVEY_SANDBOX_IMAGE / COVEY_SANDBOX_IMAGE_<PROFILE> to these references
# instead.
#
# SANDBOX_TAG picks the state: `latest` follows main, a release tag (v0.4.0)
# fetches the images built for that release. Take the one your binary is.
#
# SANDBOX_PROFILES picks which ones: all of them by default, a subset where the
# agents stand in only one field (`make sandbox-images-pull
# SANDBOX_PROFILES="base dev-flutter"`).
SANDBOX_PKG ?= ghcr.io/benjaminledel/covey-sandbox
SANDBOX_TAG ?= latest
SANDBOX_PROFILES ?= base dev dev-flutter dev-php dev-web dev-full
sandbox-images-pull:
	@for p in $(SANDBOX_PROFILES); do \
		docker pull $(SANDBOX_PKG):$$p-$(SANDBOX_TAG) || exit 1; \
		case "$$p" in \
			base) local_name=covey-sandbox:latest ;; \
			*)    local_name=covey-sandbox-$$p:latest ;; \
		esac; \
		docker tag $(SANDBOX_PKG):$$p-$(SANDBOX_TAG) $$local_name; \
		echo "$$p -> $$local_name"; \
	done
	@echo "The workplaces now sit under the names the instance looks for."

# What an upgrade needs: the new binaries and the sandbox images, because each
# one carries a coveyd that has to speak to this control plane. All workplaces
# is a long build — an installation that pulls instead runs `make build` and
# `make sandbox-images-pull`. Afterwards `covey doctor` says whether anything is
# still in the way on THIS installation.
upgrade: build sandbox-images
	@echo
	@echo "Built. Now:  covey doctor   (says what a restart would run into here)" 

# The standalone runner (spec/16). Its own artefact on purpose: on a runner host
# `serve`, `migrate` and `bootstrap` should not exist at all, and "no database
# access" reads badly when the database code is compiled in beside it.
runner:
	$(GO) build -ldflags "$(LDFLAGS)" -o covey-runner ./cmd/covey-runner

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
	rm -f covey coveyd covey-runner
	rm -rf data web/dist
