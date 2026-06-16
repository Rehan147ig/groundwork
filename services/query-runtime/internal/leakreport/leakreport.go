// Package leakreport analyzes a Groundwork permission snapshot
// (aclsync.PermissionSet) for exposure problems an operator should act on,
// independent of which connector produced the snapshot. It is the
// "shadow-mode" artifact: connect read-only, then show what is overexposed
// before any agent query is run.
//
// The analysis is connector-agnostic — it reasons about Groundwork's own
// model (groups, documents, viewer grants), so the same code reports on
// GitHub, Slack, or Atlassian snapshots. The optional ownership map encodes
// "which group is the rightful owner of this document"; with it, the report
// distinguishes a legitimate grant (owner can view) from a cross-department
// exposure (a different department can view).
package leakreport

import (
	"fmt"
	"sort"

	"groundwork/query-runtime/internal/aclsync"
)

// Severity ranks a finding for triage.
type Severity string

const (
	SeverityHigh   Severity = "high"
	SeverityMedium Severity = "medium"
	SeverityLow    Severity = "low"
)

// Kind enumerates the exposure classes the report detects.
type Kind string

const (
	// KindCrossDepartment: a group that is NOT the document's owner holds a
	// viewer grant — the classic "engineering can read the finance budget"
	// overexposure.
	KindCrossDepartment Kind = "cross_department_access"
	// KindOverexposed: a document is viewable by more than one group
	// (broad blast radius), reported once per document.
	KindOverexposed Kind = "overexposed_document"
	// KindWorldReadable: a document grants viewer to the public principal
	// (user:* / a "*" viewer) — anyone can read it.
	KindWorldReadable Kind = "world_readable"
	// KindOrphaned: a document has no viewer grants at all (often a sign of
	// a missing sync or a stranded resource).
	KindOrphaned Kind = "orphaned_document"
	// KindExcessiveGroup: a group can view documents owned by more than one
	// OTHER department — an over-privileged group.
	KindExcessiveGroup Kind = "excessive_group_access"
)

// Finding is one exposure problem.
type Finding struct {
	Kind       Kind
	Severity   Severity
	DocumentID string // empty for group-scoped findings
	Group      string // the offending group, when applicable
	Owner      string // the rightful owner, when known
	Detail     string
}

// Report is the full analysis result.
type Report struct {
	TenantID      string
	DocumentCount int
	GroupCount    int
	Findings      []Finding
}

// CountBySeverity tallies findings for a headline summary.
func (r Report) CountBySeverity() map[Severity]int {
	out := map[Severity]int{}
	for _, f := range r.Findings {
		out[f.Severity]++
	}
	return out
}

// Analyze inspects ps and returns a Report. owners maps document ID ->
// rightful owning group; pass nil to skip ownership-based findings
// (cross-department / excessive-group), in which case only structural
// findings (overexposed / world-readable / orphaned) are produced.
func Analyze(ps aclsync.PermissionSet, owners map[string]string) Report {
	rep := Report{
		TenantID:      ps.TenantID,
		DocumentCount: len(ps.Documents),
		GroupCount:    len(ps.Groups),
	}

	// group -> set of OTHER owners' documents it can view (for excessive-group).
	groupForeignOwners := map[string]map[string]struct{}{}

	docs := append([]aclsync.Document(nil), ps.Documents...)
	sort.Slice(docs, func(i, j int) bool { return docs[i].ID < docs[j].ID })

	for _, d := range docs {
		viewers := dedupeSorted(d.ViewerGroups)

		// World-readable: a "*" / public viewer (user or group).
		if hasPublic(d.ViewerUsers) || hasPublic(d.ViewerGroups) {
			rep.Findings = append(rep.Findings, Finding{
				Kind:       KindWorldReadable,
				Severity:   SeverityHigh,
				DocumentID: d.ID,
				Detail:     fmt.Sprintf("%s grants public/world-readable access", d.ID),
			})
		}

		// Orphaned: nobody can see it.
		if len(viewers) == 0 && len(d.ViewerUsers) == 0 {
			rep.Findings = append(rep.Findings, Finding{
				Kind:       KindOrphaned,
				Severity:   SeverityLow,
				DocumentID: d.ID,
				Detail:     fmt.Sprintf("%s has no viewer grants", d.ID),
			})
			continue
		}

		owner := owners[d.ID]

		// Cross-department: a viewer group that isn't the owner.
		if owner != "" {
			for _, vg := range viewers {
				if vg == owner {
					continue
				}
				rep.Findings = append(rep.Findings, Finding{
					Kind:       KindCrossDepartment,
					Severity:   SeverityHigh,
					DocumentID: d.ID,
					Group:      vg,
					Owner:      owner,
					Detail:     fmt.Sprintf("%s can view %s, which is owned by %s", vg, d.ID, owner),
				})
				if groupForeignOwners[vg] == nil {
					groupForeignOwners[vg] = map[string]struct{}{}
				}
				groupForeignOwners[vg][owner] = struct{}{}
			}
		}

		// Overexposed: viewable by >1 group (blast radius), reported once.
		if len(viewers) > 1 {
			rep.Findings = append(rep.Findings, Finding{
				Kind:       KindOverexposed,
				Severity:   SeverityMedium,
				DocumentID: d.ID,
				Detail:     fmt.Sprintf("%s is viewable by %d groups: %v", d.ID, len(viewers), viewers),
			})
		}
	}

	// Excessive group: can read documents owned by >1 other department.
	groups := make([]string, 0, len(groupForeignOwners))
	for g := range groupForeignOwners {
		groups = append(groups, g)
	}
	sort.Strings(groups)
	for _, g := range groups {
		if len(groupForeignOwners[g]) > 1 {
			owned := make([]string, 0, len(groupForeignOwners[g]))
			for o := range groupForeignOwners[g] {
				owned = append(owned, o)
			}
			sort.Strings(owned)
			rep.Findings = append(rep.Findings, Finding{
				Kind:     KindExcessiveGroup,
				Severity: SeverityHigh,
				Group:    g,
				Detail:   fmt.Sprintf("%s can read documents owned by %d other departments: %v", g, len(owned), owned),
			})
		}
	}

	return rep
}

func hasPublic(ids []string) bool {
	for _, id := range ids {
		if id == "*" || id == "user:*" || id == "public" {
			return true
		}
	}
	return false
}

func dedupeSorted(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
