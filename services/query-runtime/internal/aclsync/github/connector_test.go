package github

import (
	"context"
	"log/slog"
	"testing"

	"groundwork/query-runtime/internal/aclsync"
)

func TestGitHubConnector_Mapping(t *testing.T) {
	client := NewMockClient()
	connector := NewConnector(client, "acme-financial", slog.Default())
	ctx := context.Background()

	ps, err := connector.Snapshot(ctx, "acme-financial")
	if err != nil {
		t.Fatalf("Snapshot failed: %v", err)
	}

	tuples := aclsync.PermissionSetToTuples(ps)
	if len(tuples) == 0 {
		t.Fatalf("Expected tuples, got 0")
	}

	fga := aclsync.NewMemoryFGA()
	if err := fga.WriteTuples(ctx, "acme-financial", tuples); err != nil {
		t.Fatalf("Failed to write tuples: %v", err)
	}

	tests := []struct {
		user     string
		repo     string
		expected bool
	}{
		{"user:alice", "document:gh:finance-budget", true},    // Alice in finance -> finance-budget
		{"user:bob", "document:gh:executive-strategy", false}, // Bob in eng -> no access to exec
		{"user:dave", "document:gh:security-audit", true},     // Dave in sec -> sec audit
		{"user:carol", "document:gh:payroll-system", false},   // Carol in HR -> no access to payroll (eng owns it)
		{"user:eve", "document:gh:executive-strategy", true},  // Eve in exec -> exec strategy
		{"user:bob", "document:gh:finance-budget", true},      // Leak scenario: bob in eng -> finance-budget
	}

	for _, tt := range tests {
		t.Run(tt.user+"_"+tt.repo, func(t *testing.T) {
			allowed := fga.Check("acme-financial", tt.user, "viewer", tt.repo)
			if allowed != tt.expected {
				t.Errorf("Expected %v, got %v for %s accessing %s", tt.expected, allowed, tt.user, tt.repo)
			}
		})
	}
}
