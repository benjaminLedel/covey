# Multi-stage build from spec/10-architecture-stack.md:
# Node builds the frontend → Go embeds it → distroless final image.
FROM node:26 AS web
# Only web/ — the frontend build reads nothing outside it. It read docs/ for a
# while (#128), which is why this stage briefly copied the whole context; with
# the documentation pages out of the binary (#130) it is back to what it needs,
# and a change anywhere else no longer invalidates this layer.
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ .
RUN npm run build

FROM golang:1.27 AS build
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
COPY --from=web /web/dist ./web/dist
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
