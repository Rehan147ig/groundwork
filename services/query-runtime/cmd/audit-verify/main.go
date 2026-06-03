// Command audit-verify scans the immutable Groundwork audit ledger and detects
// tampering: rows whose fields were modified after write (digest mismatch) and
// broken hash-chain links (deletion or reordering). It exits non-zero if any
// tenant's chain is broken, so it can run in CI or a compliance cron.
//
// Usage:
//
//	DATABASE_URL=postgres://... go run ./cmd/audit-verify
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	"groundwork/query-runtime/internal/engine"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL is required")
		os.Exit(2)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open database: %v\n", err)
		os.Exit(2)
	}
	defer db.Close()

	ctx := context.Background()
	tenants, err := engine.ListAuditTenants(ctx, db)
	if err != nil {
		fmt.Fprintf(os.Stderr, "list tenants: %v\n", err)
		os.Exit(2)
	}

	violations := 0
	for _, tenant := range tenants {
		entries, err := engine.LoadAuditChain(ctx, db, tenant)
		if err != nil {
			fmt.Fprintf(os.Stderr, "load chain for tenant %s: %v\n", tenant, err)
			os.Exit(2)
		}
		problems := engine.VerifyChain(entries)
		if len(problems) == 0 {
			fmt.Printf("OK     tenant=%s entries=%d chain intact\n", tenant, len(entries))
			continue
		}
		violations += len(problems)
		for _, p := range problems {
			fmt.Printf("TAMPER tenant=%s idx=%d trace=%s kind=%s: %s\n", tenant, p.Index, p.TraceID, p.Kind, p.Detail)
		}
	}

	if violations > 0 {
		fmt.Printf("\nFAILED: %d integrity violation(s) detected across %d tenant(s)\n", violations, len(tenants))
		os.Exit(1)
	}
	fmt.Printf("\nPASSED: all %d tenant chain(s) verified intact\n", len(tenants))
}
