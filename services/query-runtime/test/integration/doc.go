// Package integration holds Groundwork's end-to-end integration tests, which run the real
// engine against LIVE OpenFGA, Postgres, and Qdrant (no in-memory fakes).
//
// All test files are guarded by the `integration` build tag, so the normal unit-test CI
// (`go test ./...`) does not compile or run them — they require the backing services. Bring
// the stack up and run them via scripts/integration-test.sh (or `go test -tags integration`
// with GROUNDWORK_TEST_* env pointing at running services). See docs/integration-testing.md.
//
// This file carries no build tag so the package always contains at least one Go file when
// the tag is absent (keeping `go build ./...` / `go vet ./...` happy).
package integration
