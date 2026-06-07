package main

import (
	"testing"
)

// PR #19 narrows main.go's scope to env validation + the dbURL helper. The
// real enumeration flow is tested where it lives (internal/aclsync/msgraph)
// against the fake GraphClient — see enumerate_test.go and catalog_test.go.
// PR #18's runConnector-style integration test was removed along with the
// directoryOnlyGraphClient decorator it depended on.

func TestMissingEnvVars(t *testing.T) {
	missing := validate(func(string) string { return "" })
	// 4 MSGRAPH_*/OPENFGA_* vars from PR #17 + the DATABASE_URL_or_POSTGRES_URL marker added in PR #19 = 5.
	want := len(requiredEnv) + 1
	if len(missing) != want {
		t.Fatalf("expected %d missing env vars, got %d: %v", want, len(missing), missing)
	}
}

func TestValidConfig(t *testing.T) {
	env := map[string]string{
		"MSGRAPH_TENANT_ID":     "tenant-id-value",
		"MSGRAPH_CLIENT_ID":     "client-id-value",
		"MSGRAPH_CLIENT_SECRET": "client-secret-value",
		"OPENFGA_STORE_ID":      "store-id-value",
		"DATABASE_URL":          "postgres://groundwork:groundwork@postgres:5432/groundwork",
	}
	if missing := validate(func(k string) string { return env[k] }); len(missing) != 0 {
		t.Fatalf("expected no missing env vars with valid config, got: %v", missing)
	}
}

// TestValidConfigWithPostgresUrlFallback: DATABASE_URL not set but
// POSTGRES_URL is — validation must accept either. This preserves the
// PR #17 compose-file behavior (POSTGRES_URL was the only DB env declared
// for the msgraph-connector service).
func TestValidConfigWithPostgresUrlFallback(t *testing.T) {
	env := map[string]string{
		"MSGRAPH_TENANT_ID":     "tenant-id-value",
		"MSGRAPH_CLIENT_ID":     "client-id-value",
		"MSGRAPH_CLIENT_SECRET": "client-secret-value",
		"OPENFGA_STORE_ID":      "store-id-value",
		"POSTGRES_URL":          "postgres://groundwork:groundwork@postgres:5432/groundwork",
	}
	getter := func(k string) string { return env[k] }
	if missing := validate(getter); len(missing) != 0 {
		t.Fatalf("expected no missing env vars with POSTGRES_URL fallback, got: %v", missing)
	}
	if got := dbURL(getter); got != env["POSTGRES_URL"] {
		t.Fatalf("dbURL fallback: want %q, got %q", env["POSTGRES_URL"], got)
	}
}

// TestDbURLPrefersDatabaseURL: when both DATABASE_URL and POSTGRES_URL are
// set, the runtime convention (DATABASE_URL) wins.
func TestDbURLPrefersDatabaseURL(t *testing.T) {
	env := map[string]string{
		"DATABASE_URL": "postgres://from-database-url",
		"POSTGRES_URL": "postgres://from-postgres-url",
	}
	getter := func(k string) string { return env[k] }
	if got := dbURL(getter); got != "postgres://from-database-url" {
		t.Fatalf("dbURL preference: want DATABASE_URL, got %q", got)
	}
}

// TestPartiallyMissing: env vars partially set — validation reports each
// unset one. Exit-code-1 path stays actionable for the operator.
func TestPartiallyMissing(t *testing.T) {
	env := map[string]string{
		"MSGRAPH_TENANT_ID": "tenant-id-value",
		"MSGRAPH_CLIENT_ID": "client-id-value",
		"DATABASE_URL":      "postgres://groundwork:groundwork@postgres:5432/groundwork",
		// MSGRAPH_CLIENT_SECRET and OPENFGA_STORE_ID intentionally unset.
	}
	missing := validate(func(k string) string { return env[k] })
	if len(missing) != 2 {
		t.Fatalf("expected 2 missing env vars, got %d: %v", len(missing), missing)
	}
}
