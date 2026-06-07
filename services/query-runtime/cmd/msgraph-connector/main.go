// Command msgraph-connector is the operator-facing entrypoint for the
// Microsoft Graph pilot. This scaffold-only build validates that the four
// required configuration environment variables are present, then exits.
// It performs zero Microsoft Graph calls, zero OpenFGA writes, and zero
// Postgres writes. Subsequent PRs in the feat/msgraph-pilot-* series fill
// in the sync logic on top of services/query-runtime/internal/aclsync/msgraph.
package main

import (
	"fmt"
	"log"
	"os"
)

var requiredEnv = []string{
	"MSGRAPH_TENANT_ID",
	"MSGRAPH_CLIENT_ID",
	"MSGRAPH_CLIENT_SECRET",
	"OPENFGA_STORE_ID",
}

// validate returns the names of required env vars that are unset or empty.
// Factored out of main() so the unit tests can exercise it without spawning
// a subprocess to observe os.Exit behavior.
func validate(getenv func(string) string) []string {
	var missing []string
	for _, k := range requiredEnv {
		if getenv(k) == "" {
			missing = append(missing, k)
		}
	}
	return missing
}

func main() {
	if missing := validate(os.Getenv); len(missing) > 0 {
		log.Printf("msgraph-connector: missing required env vars: %v", missing)
		os.Exit(1)
	}
	fmt.Println("OK — config valid, no work yet")
}
