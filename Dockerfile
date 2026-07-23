# Multi-Stage-Build aus spec/10-architektur-stack.md:
# Node baut das Frontend → Go bettet es ein → distroless-Endimage.
FROM node:22 AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ .
RUN npm run build

FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /web/dist ./web/dist
RUN CGO_ENABLED=0 go build -o /covey ./cmd/covey && \
    CGO_ENABLED=0 go build -o /coveyd ./cmd/coveyd

FROM gcr.io/distroless/static
COPY --from=build /covey /covey
COPY --from=build /coveyd /coveyd
ENV COVEY_COVEYD_PATH=/coveyd
ENTRYPOINT ["/covey"]
CMD ["serve"]
