// Command msgraph-connector enumerates a customer's Microsoft Entra directory
// (users + groups + memberships) and persists the result into the msgraph.*
// catalog tables. PR #19 of the Microsoft Graph pilot — "directory
// enumeration, visibility only".
//
// Explicit non-goals for this build (all deferred to PR #20+):
//   - No OpenFGA writes. No tuple generation.
//   - No SharePoint, site, drive, or document enumeration.
//   - No replay, no shadow mode, no leak report.
//   - No canonical principal resolution; gw_canonical_id is stored as the
//     placeholder "entra:<oid>" for later replacement.
//
// Exit codes:
//
//	0  success
//	1  required env var missing
//	2  FGA invariant violation (reserved; unreachable in PR #19 — no OpenFGA writes)
//	3  directory enumeration failed (auth, network, Graph error)
//	4  Postgres unavailable (connection or ping)
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	_ "github.com/jackc/pgx/v5/stdlib" // pgx driver registers itself as "pgx"

	"groundwork/query-runtime/internal/aclsync/msgraph"
)

var requiredEnv = []string{
	"MSGRAPH_TENANT_ID",
	"MSGRAPH_CLIENT_ID",
	"MSGRAPH_CLIENT_SECRET",
	"OPENFGA_STORE_ID",
}

// validate returns the names of required env vars that are unset or empty.
// Factored out so tests exercise it without spawning a subprocess.
func validate(getenv func(string) string) []string {
	var missing []string
	for _, k := range requiredEnv {
		if getenv(k) == "" {
			missing = append(missing, k)
		}
	}
	// DATABASE_URL is new in PR #19. Either DATABASE_URL (runtime convention)
	// or POSTGRES_URL (compose convention from PR #17) is acceptable — at
	// least one must be set.
	if getenv("DATABASE_URL") == "" && getenv("POSTGRES_URL") == "" {
		missing = append(missing, "DATABASE_URL_or_POSTGRES_URL")
	}
	return missing
}

// dbURL returns the Postgres connection string, preferring DATABASE_URL
// (runtime convention) over POSTGRES_URL (compose convention).
func dbURL(getenv func(string) string) string {
	if v := getenv("DATABASE_URL"); v != "" {
		return v
	}
	return getenv("POSTGRES_URL")
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	if missing := validate(os.Getenv); len(missing) > 0 {
		logger.Error("missing required env vars", "missing", missing)
		os.Exit(1)
	}

	cfg := msgraph.Config{
		TenantID:     os.Getenv("MSGRAPH_TENANT_ID"),
		ClientID:     os.Getenv("MSGRAPH_CLIENT_ID"),
		ClientSecret: os.Getenv("MSGRAPH_CLIENT_SECRET"),
	}

	db, err := sql.Open("pgx", dbURL(os.Getenv))
	if err != nil {
		logger.Error("postgres open failed", "err", err.Error())
		os.Exit(4)
	}
	defer func() { _ = db.Close() }()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		logger.Error("postgres ping failed (is db-migrate up-to-date?)", "err", err.Error())
		os.Exit(4)
	}

	graphClient := msgraph.NewHTTPGraphClient(cfg)
	connector := msgraph.NewConnector(graphClient, cfg, logger, nil)
	catalog := msgraph.NewPostgresCatalogWriter(db)

	stats, err := connector.EnumerateDirectory(ctx, cfg.TenantID, catalog)
	if err != nil {
		switch {
		case errors.Is(err, msgraph.ErrAuthFailed):
			logger.Error("microsoft graph authentication failed", "tenant", cfg.TenantID)
		default:
			logger.Error("directory enumeration failed", "tenant", cfg.TenantID, "err", err.Error())
		}
		os.Exit(3)
	}

	// Report totals from the catalog (post-upsert) plus the stats from this
	// run. On a re-run, observed counts may equal the run stats while catalog
	// totals stay flat — that's the idempotency property.
	pTotal, _ := catalog.PrincipalCount(ctx, cfg.TenantID)
	gTotal, _ := catalog.GroupCount(ctx, cfg.TenantID)
	mTotal, _ := catalog.MembershipCount(ctx, cfg.TenantID)

	fmt.Printf(
		"msgraph-connector OK\n"+
			"  tenant_id:           %s\n"+
			"  principals (total):  %d  (this run observed %d)\n"+
			"  groups (total):      %d  (this run observed %d)\n"+
			"  memberships (total): %d  (this run observed %d)\n"+
			"  mode:                directory enumeration (PR #19; no OpenFGA, no SharePoint)\n",
		cfg.TenantID,
		pTotal, stats.PrincipalsUpserted,
		gTotal, stats.GroupsUpserted,
		mTotal, stats.MembershipsUpserted,
	)
}
