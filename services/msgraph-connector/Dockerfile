# Microsoft Graph connector — scaffold image.
#
# Builds the cmd/msgraph-connector binary out of services/query-runtime.
# The connector lives in the query-runtime Go module (same go.mod) and is
# built into its own image so it can be deployed and restarted independently
# of the runtime. Same module, two binaries, two images — standard Go monorepo
# pattern.
#
# Build context for this Dockerfile MUST be the repository root (see
# infra/docker-compose.codespace.yml). That's why the COPY paths are prefixed
# with services/query-runtime.

FROM golang:1.23-alpine AS builder
WORKDIR /src
RUN apk add --no-cache build-base
ENV GOCACHE=/tmp/go-build
COPY services/query-runtime/go.mod ./
COPY services/query-runtime/go.sum ./
COPY services/query-runtime/cmd     ./cmd
COPY services/query-runtime/internal ./internal
RUN go build -o /out/msgraph-connector ./cmd/msgraph-connector

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=builder /out/msgraph-connector /usr/local/bin/msgraph-connector
USER nobody
ENTRYPOINT ["/usr/local/bin/msgraph-connector"]
