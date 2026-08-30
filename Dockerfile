# Multi-stage build from spec/10-architecture-stack.md:
# Node builds the frontend → Go embeds it → distroless final image.
FROM node:26 AS web
# The stage is rooted at /src and the frontend sits at /src/web — the same
# shape as the repository. Since the documentation has one source (#128),
# web/docs.mjs reads ../docs while building, and it checks the links that lead
# from there back into the repository (spec/, README.md, examples/, internal/).
# So the stage gets the whole context, not just web/: a narrower copy would
# turn every new link in a doc page into a failing image build.
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY . /src/
RUN npm run build

FROM golang:1.26 AS build
# Provenance of the binary (internal/buildinfo). The image has no .git
# (see .dockerignore), so the caller passes the values in — CI sets them from
# $CI_COMMIT_*; without build args it stays "dev".
ARG VERSION=dev
ARG COMMIT=
ARG DATE=
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN LDFLAGS="-X covey/internal/buildinfo.version=$VERSION \
             -X covey/internal/buildinfo.commit=$COMMIT \
             -X covey/internal/buildinfo.date=$DATE" && \
    CGO_ENABLED=0 go build -ldflags "$LDFLAGS" -o /covey ./cmd/covey && \
    CGO_ENABLED=0 go build -ldflags "$LDFLAGS" -o /coveyd ./cmd/coveyd

FROM gcr.io/distroless/static
COPY --from=build /covey /covey
COPY --from=build /coveyd /coveyd
# Static docker CLI for the docker sandbox provider: it starts sandboxes via
# `docker run` over the mounted host socket (sibling containers).
COPY --from=docker:27-cli /usr/local/bin/docker /usr/local/bin/docker
ENV COVEY_COVEYD_PATH=/coveyd
ENTRYPOINT ["/covey"]
CMD ["serve"]
