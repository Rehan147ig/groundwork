// Command leak-report prints a Groundwork exposure report for the Acme
// GitHub demo. By default it runs OFFLINE against the in-memory MockClient,
// so it is demoable with zero provisioning ("here's what's overexposed").
// Point it at a live org by setting GITHUB_TOKEN (and -org), which swaps the
// mock for the real PAT-backed client — identical analysis, real data.
//
//	go run ./cmd/leak-report              # offline, Acme mock org
//	GITHUB_TOKEN=ghp_... go run ./cmd/leak-report -org acme-financial
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"

	"groundwork/query-runtime/internal/aclsync/github"
	"groundwork/query-runtime/internal/leakreport"
)

// acmeOwners encodes which team rightfully owns each repo, so the report can
// tell a legitimate grant from a cross-department exposure.
var acmeOwners = map[string]string{
	"gh:finance-budget":       "finance-team",
	"gh:payroll-system":       "engineering-team",
	"gh:engineering-platform": "engineering-team",
	"gh:security-audit":       "security-team",
	"gh:executive-strategy":   "executive-team",
}

func main() {
	org := flag.String("org", "acme-financial", "GitHub org")
	flag.Parse()

	var client github.GitHubClient
	mode := "offline (mock org)"
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		client = github.NewHTTPClient(token)
		mode = "live (GITHUB_TOKEN)"
	} else {
		client = github.NewMockClient()
	}

	conn := github.NewConnector(client, *org, nil)
	ps, err := conn.Snapshot(context.Background(), *org)
	if err != nil {
		fmt.Fprintf(os.Stderr, "snapshot failed: %v\n", err)
		os.Exit(1)
	}

	rep := leakreport.Analyze(ps, acmeOwners)
	printReport(rep, mode)
}

func printReport(rep leakreport.Report, mode string) {
	counts := rep.CountBySeverity()
	fmt.Printf("Groundwork Leak Report — tenant=%s  [%s]\n", rep.TenantID, mode)
	fmt.Printf("  scanned %d documents across %d groups\n", rep.DocumentCount, rep.GroupCount)
	fmt.Printf("  findings: %d high, %d medium, %d low\n\n",
		counts[leakreport.SeverityHigh], counts[leakreport.SeverityMedium], counts[leakreport.SeverityLow])

	if len(rep.Findings) == 0 {
		fmt.Println("  no exposure findings.")
		return
	}

	// High first, then medium, then low; stable within a severity.
	order := map[leakreport.Severity]int{leakreport.SeverityHigh: 0, leakreport.SeverityMedium: 1, leakreport.SeverityLow: 2}
	fs := append([]leakreport.Finding(nil), rep.Findings...)
	sort.SliceStable(fs, func(i, j int) bool { return order[fs[i].Severity] < order[fs[j].Severity] })

	for _, f := range fs {
		fmt.Printf("  [%-6s] %-24s %s\n", f.Severity, f.Kind, f.Detail)
	}
}
