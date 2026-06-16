package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"groundwork/query-runtime/internal/aclsync"
	"groundwork/query-runtime/internal/aclsync/github"
)

func main() {
	var (
		org        = flag.String("org", "acme-financial", "GitHub Organization to sync")
		openfgaURL = flag.String("openfga", "http://localhost:8081", "OpenFGA API URL")
	)
	flag.Parse()

	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		slog.Error("GITHUB_TOKEN environment variable is required")
		os.Exit(1)
	}

	logger := slog.Default()
	logger.Info("Starting GitHub connector", "org", *org)

	// Inject the live HTTP client
	client := github.NewHTTPClient(token)
	connector := github.NewConnector(client, *org, logger)

	// Fetch live snapshot from GitHub API
	ctx := context.Background()
	ps, err := connector.Snapshot(ctx, *org)
	if err != nil {
		logger.Error("Failed to fetch permissions from GitHub", "error", err)
		os.Exit(1)
	}

	// Map to Groundwork tuples
	tuples := aclsync.PermissionSetToTuples(ps)
	logger.Info("Successfully fetched permissions", "users", len(ps.Users), "groups", len(ps.Groups), "repos", len(ps.Documents), "tuples", len(tuples))

	// Write to OpenFGA
	fgaSink := aclsync.NewOpenFGASink(*openfgaURL, "groundwork_local")
	if err := fgaSink.WriteTuples(ctx, *org, tuples); err != nil {
		logger.Error("Failed to sync tuples to OpenFGA", "error", err)
		os.Exit(1)
	}

	logger.Info("Sync complete.")
}
