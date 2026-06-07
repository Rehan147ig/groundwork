package aclsync

import (
	"context"
	"testing"
)

func TestDiscardSinkRecordsWrites(t *testing.T) {
	s := NewDiscardSink(nil)
	tuples := []Tuple{
		{User: "user:alice", Relation: "member", Object: "group:finance"},
		{User: "user:bob", Relation: "member", Object: "group:finance"},
	}
	if err := s.WriteTuples(context.Background(), "tenant_x", tuples); err != nil {
		t.Fatalf("WriteTuples: %v", err)
	}
	if got := s.WrittenCount(); got != 2 {
		t.Fatalf("WrittenCount: want 2, got %d", got)
	}
}

func TestDiscardSinkListAlwaysEmpty(t *testing.T) {
	s := NewDiscardSink(nil)
	_ = s.WriteTuples(context.Background(), "t", []Tuple{{User: "user:a", Relation: "member", Object: "group:g"}})
	// Even after writes, ListTuples must return empty so the Syncer treats every
	// desired tuple as new — that's the property that makes DiscardSink correct
	// under SyncToOpenFGA (writes are observed but nothing is reconciled away).
	got, err := s.ListTuples(context.Background(), "t")
	if err != nil {
		t.Fatalf("ListTuples: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ListTuples: want empty, got %d tuples", len(got))
	}
}

func TestDiscardSinkRecordsDeletes(t *testing.T) {
	s := NewDiscardSink(nil)
	tuples := []Tuple{{User: "user:alice", Relation: "viewer", Object: "document:doc1"}}
	if err := s.DeleteTuples(context.Background(), "t", tuples); err != nil {
		t.Fatalf("DeleteTuples: %v", err)
	}
	if got := s.DeletedCount(); got != 1 {
		t.Fatalf("DeletedCount: want 1, got %d", got)
	}
}
