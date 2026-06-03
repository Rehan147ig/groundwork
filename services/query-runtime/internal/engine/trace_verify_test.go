package engine

import (
	"testing"
	"time"
)

// buildChain produces a correctly hash-chained sequence of audit entries the same
// way PostgresAuditWriter.Write would (previous_hash = prior digest).
func buildChain(entries ...AuditEntry) []AuditEntry {
	prev := ""
	out := make([]AuditEntry, 0, len(entries))
	for _, e := range entries {
		if e.TimestampUTC.IsZero() {
			e.TimestampUTC = time.Now().UTC()
		}
		e.PreviousHash = prev
		e.ImmutableDigest = ComputeDigest(e)
		prev = e.ImmutableDigest
		out = append(out, e)
	}
	return out
}

func sampleEntry(trace, user string) AuditEntry {
	return AuditEntry{
		TraceID:             trace,
		TenantID:            "acme",
		UserID:              user,
		QueryHash:           hashText("policy"),
		TimestampUTC:        time.Now().UTC(),
		Region:              "US",
		CandidatesRetrieved: 3,
		CandidatesAllowed:   1,
		CandidatesBlocked:   2,
		TotalLatencyMs:      5,
		CircuitBreakerState: "closed",
		DecisionMode:        "engine_live_acl_fail_closed",
		ACLDecision:         "allowed",
		Reason:              "allowed",
	}
}

func TestVerifyChainCleanIsValid(t *testing.T) {
	chain := buildChain(sampleEntry("t1", "alice"), sampleEntry("t2", "bob"), sampleEntry("t3", "carol"))
	if problems := VerifyChain(chain); len(problems) != 0 {
		t.Fatalf("expected a clean chain to verify, got: %+v", problems)
	}
}

func TestVerifyChainDetectsModifiedRow(t *testing.T) {
	chain := buildChain(sampleEntry("t1", "alice"), sampleEntry("t2", "bob"), sampleEntry("t3", "carol"))
	// Tamper a row in place WITHOUT recomputing its digest, as an attacker editing the DB would.
	chain[1].UserID = "attacker"

	problems := VerifyChain(chain)
	if len(problems) == 0 {
		t.Fatal("expected the verifier to detect the modified row")
	}
	found := false
	for _, p := range problems {
		if p.Index == 1 && p.Kind == "digest_mismatch" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected digest_mismatch at index 1, got: %+v", problems)
	}
}

func TestVerifyChainDetectsBrokenLink(t *testing.T) {
	chain := buildChain(sampleEntry("t1", "alice"), sampleEntry("t2", "bob"), sampleEntry("t3", "carol"))
	// Drop the middle row (deletion / reordering): t3's previous_hash no longer matches.
	broken := []AuditEntry{chain[0], chain[2]}

	problems := VerifyChain(broken)
	found := false
	for _, p := range problems {
		if p.Kind == "broken_link" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a broken_link after deletion, got: %+v", problems)
	}
}
