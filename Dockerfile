# Multi-Stage-Build aus spec/10-architecture-stack.md:
# Node baut das Frontend → Go bettet es ein → distroless-Endimage.
FROM node:26 AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ .
RUN npm run build

FROM golang:1.26 AS build
# Herkunft des Binaries (internal/buildinfo). Im Image gibt es kein .git
# (siehe .dockerignore), deshalb reicht der Aufrufer die Werte durch —
# die CI setzt sie aus $CI_COMMIT_*; ohne Build-Args bleibt es "dev".
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
# Statische docker-CLI für den docker-SandboxProvider: er startet Sandboxen
# via `docker run` über den gemounteten Host-Socket (Sibling-Container).
COPY --from=docker:27-cli /usr/local/bin/docker /usr/local/bin/docker
ENV COVEY_COVEYD_PATH=/coveyd
ENTRYPOINT ["/covey"]
CMD ["serve"]
