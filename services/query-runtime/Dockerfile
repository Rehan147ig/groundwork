FROM golang:1.23-alpine
WORKDIR /src
RUN apk add --no-cache build-base
ENV GOCACHE=/tmp/go-build
COPY go.mod ./
COPY go.sum ./
COPY cmd ./cmd
COPY internal ./internal
RUN go build -o /out/query-runtime ./cmd/query-runtime
RUN adduser -D -H appuser
RUN mkdir -p /tmp/go-build && chown -R appuser:appuser /tmp/go-build /go
USER appuser
EXPOSE 8080
CMD ["/out/query-runtime"]
