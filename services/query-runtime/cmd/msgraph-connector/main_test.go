package main

import (
	"testing"
)

// TestMissingEnvVars asserts that an empty environment reports every required
// variable as missing. In the real binary this triggers os.Exit(1); we test
// the underlying validate() rather than the exit-behavior because asserting
// on os.Exit requires a subprocess test which is heavier than this PR needs.
func TestMissingEnvVars(t *testing.T) {
	missing := validate(func(string) string { return "" })
	if len(missing) != len(requiredEnv) {
		t.Fatalf("expected all %d env vars missing, got %d: %v",
			len(requiredEnv), len(missing), missing)
	}
}

// TestValidConfig asserts that a fully-populated environment leaves nothing
// missing — main() would then print "OK — config valid, no work yet" and
// os.Exit(0).
func TestValidConfig(t *testing.T) {
	env := map[string]string{
		"MSGRAPH_TENANT_ID":     "tenant-id-value",
		"MSGRAPH_CLIENT_ID":     "client-id-value",
		"MSGRAPH_CLIENT_SECRET": "client-secret-value",
		"OPENFGA_STORE_ID":      "store-id-value",
	}
	missing := validate(func(k string) string { return env[k] })
	if len(missing) != 0 {
		t.Fatalf("expected no missing env vars with valid config, got: %v", missing)
	}
}

// TestPartiallyMissing asserts the validator returns only the missing names,
// not all four — important so the operator's log message is actionable.
func TestPartiallyMissing(t *testing.T) {
	env := map[string]string{
		"MSGRAPH_TENANT_ID": "tenant-id-value",
		"MSGRAPH_CLIENT_ID": "client-id-value",
		// MSGRAPH_CLIENT_SECRET and OPENFGA_STORE_ID intentionally unset
	}
	missing := validate(func(k string) string { return env[k] })
	if len(missing) != 2 {
		t.Fatalf("expected 2 missing env vars, got %d: %v", len(missing), missing)
	}
}
