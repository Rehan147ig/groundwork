// Demo-corpus seeder for the PR #21 Audit Foundation.
//
// Reads the bank-demo persona graph + corpus frontmatter and writes the
// implied document attribution into the demo schema (created by migration
// 012). The Leak Report (PR #24) joins this against audit_log_decisions
// to produce "denied attempts per document". Until a real document
// attribution sync connector lands, this is how the bank demo gets
// realistic data for the operator experience.
//
// The seeder is idempotent: ON CONFLICT DO UPDATE on demo.documents,
// ON CONFLICT DO UPDATE on demo.document_permissions. Re-running the
// bank-demo bootstrap is safe.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// seedDemoCorpus writes documents and permissions for the bank-demo
// persona graph. Errors are returned to the caller; main.go decides
// whether they are fatal (default: yes, the demo data is load-bearing
// for the Leak Report).
//
// Folder viewers in personas.json are written as 'inherited' grants
// (subjects like "group:all_staff#member" are kept verbatim so the
// Leak Report can attribute them back to OpenFGA tuples).
// extra_viewers are written as 'direct_user' if they start with
// "user:" and 'direct_group' if they start with "group:".
func seedDemoCorpus(ctx context.Context, db *sql.DB, g *personaGraph, corpusDir string) error {
	if db == nil {
		return fmt.Errorf("nil database handle")
	}

	folderViewers := make(map[string][]string, len(g.Folders))
	for _, f := range g.Folders {
		folderViewers[f.ID] = f.Viewers
	}

	for _, doc := range g.Documents {
		title := docTitle(corpusDir, doc.File, doc.ID)
		sensitivityLabel := sensitivityFromFolder(doc.Folder)
		owner := ownerFromExtraViewers(doc.ExtraViewers)
		// PR #21 CI-4: anonymous_share == "exposed via a no-auth public
		// link", the headline Leak Report finding. None of the personas.json
		// documents model this — tier_public is "all employees can read",
		// which is a different (and much less alarming) risk. Always set
		// false here. Explicit anonymous-share fixtures for the Leak
		// Report come in PR #24 alongside the report itself.
		anonymous := false

		if _, err := db.ExecContext(ctx, `
			INSERT INTO demo.documents
				(tenant_id, document_id, title, sensitivity_label,
				 folder_id, owner_principal, anonymous_share)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (tenant_id, document_id) DO UPDATE SET
				title             = EXCLUDED.title,
				sensitivity_label = EXCLUDED.sensitivity_label,
				folder_id         = EXCLUDED.folder_id,
				owner_principal   = EXCLUDED.owner_principal,
				anonymous_share   = EXCLUDED.anonymous_share
		`,
			g.TenantID, doc.ID, title,
			nullIfEmpty(sensitivityLabel),
			nullIfEmpty(doc.Folder),
			nullIfEmpty(owner),
			anonymous,
		); err != nil {
			return fmt.Errorf("insert demo.documents %s: %w", doc.ID, err)
		}

		// Folder-inherited viewers: every viewer on the doc's folder
		// becomes an 'inherited' grant against this document.
		for _, viewer := range folderViewers[doc.Folder] {
			if _, err := db.ExecContext(ctx, `
				INSERT INTO demo.document_permissions
					(tenant_id, document_id, principal_or_group, kind, source_label)
				VALUES ($1, $2, $3, 'inherited', $4)
				ON CONFLICT (tenant_id, document_id, principal_or_group, kind) DO UPDATE SET
					source_label = EXCLUDED.source_label
			`,
				g.TenantID, doc.ID, viewer, "folder:"+doc.Folder,
			); err != nil {
				return fmt.Errorf("insert inherited grant %s/%s: %w", doc.ID, viewer, err)
			}
		}

		// extra_viewers: per-document direct grants. Classify by the
		// "user:" vs "group:" prefix (the OpenFGA-style subject format
		// used throughout the bank demo).
		for _, v := range doc.ExtraViewers {
			kind := "direct_user"
			switch {
			case strings.HasPrefix(v, "group:"):
				kind = "direct_group"
			case strings.HasPrefix(v, "user:"):
				kind = "direct_user"
			default:
				// Treat unprefixed subjects as direct_user (legacy
				// shape; personas.json uses prefixed subjects but be
				// defensive).
				kind = "direct_user"
			}
			if _, err := db.ExecContext(ctx, `
				INSERT INTO demo.document_permissions
					(tenant_id, document_id, principal_or_group, kind, source_label)
				VALUES ($1, $2, $3, $4, $5)
				ON CONFLICT (tenant_id, document_id, principal_or_group, kind) DO UPDATE SET
					source_label = EXCLUDED.source_label
			`,
				g.TenantID, doc.ID, v, kind, "document:"+doc.ID,
			); err != nil {
				return fmt.Errorf("insert direct grant %s/%s: %w", doc.ID, v, err)
			}
		}
	}
	return nil
}

// docTitle derives a human-readable title from the corpus file path
// when frontmatter isn't loaded. Falls back to the doc id when the
// path is empty. The seeder only reads the path; the actual file
// content is consumed by the Qdrant ingestion pass.
func docTitle(corpusDir, file, id string) string {
	if file == "" {
		return id
	}
	base := filepath.Base(file)
	// strip ".md" — the corpus is markdown.
	if ext := filepath.Ext(base); ext != "" {
		base = base[:len(base)-len(ext)]
	}
	// Tidy "POL-001-credit-policy" -> "POL-001-credit-policy" (keep
	// as-is; it's already readable). If it's purely an id pattern
	// keep the original id for clarity.
	if base == "" {
		return id
	}
	return base
}

// sensitivityFromFolder maps a folder tier to a label the Leak Report
// can display ("Public", "Executive", etc.). Returns "" for unknown
// folders so the column lands as NULL.
func sensitivityFromFolder(folderID string) string {
	switch folderID {
	case "tier_public":
		return "Public"
	case "tier_compliance":
		return "Compliance"
	case "tier_credit":
		return "Credit-Sensitive"
	case "tier_kyc":
		return "KYC-Confidential"
	case "tier_branch_mgmt_lon", "tier_branch_mgmt_nyc":
		return "Branch-Management"
	case "tier_audit":
		return "Audit-Only"
	case "tier_executive":
		return "Executive"
	case "tier_audit_committee_only":
		return "Audit-Committee-Only"
	default:
		return ""
	}
}

// ownerFromExtraViewers picks the first user-typed extra viewer as the
// nominal "owner" of the document. The bank demo doesn't model
// ownership distinctly, but the Leak Report dashboard shows an owner
// column so we surface a deterministic value (first direct user
// grant) for display purposes. Empty when there's no user grant.
func ownerFromExtraViewers(viewers []string) string {
	for _, v := range viewers {
		if strings.HasPrefix(v, "user:") {
			return strings.TrimPrefix(v, "user:")
		}
	}
	return ""
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
