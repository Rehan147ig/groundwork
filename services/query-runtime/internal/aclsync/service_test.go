package aclsync

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --- test doubles ---

// flakySink wraps a MemoryFGA and fails the first failWrites WriteTuples calls.
type flakySink struct {
	inner      *MemoryFGA
	failWrites int
	mu         sync.Mutex
	attempts   int
}

func (f *flakySink) ListTuples(ctx context.Context, t string) ([]Tuple, error) {
	return f.inner.ListTuples(ctx, t)
}
func (f *flakySink) WriteTuples(ctx context.Context, t string, tuples []Tuple) error {
	f.mu.Lock()
	f.attempts++
	fail := f.attempts <= f.failWrites
	f.mu.Unlock()
	if fail {
		return errors.New("openfga unavailable")
	}
	return f.inner.WriteTuples(ctx, t, tuples)
}
func (f *flakySink) DeleteTuples(ctx context.Context, t string, tuples []Tuple) error {
	return f.inner.DeleteTuples(ctx, t, tuples)
}
func (f *flakySink) writeAttempts() int { f.mu.Lock(); defer f.mu.Unlock(); return f.attempts }

// emptyConnector returns an empty (but successful) snapshot — simulates a connector
// outage that returns nothing. Must NOT trigger destructive deletes.
type emptyConnector struct{}

func (emptyConnector) ListDocuments(context.Context, string) ([]Document, error) { return nil, nil }
func (emptyConnector) GetDocumentPermissions(context.Context, string, string) (DocumentPermissions, error) {
	return DocumentPermissions{}, nil
}
func (emptyConnector) WatchPermissionChanges(context.Context, string) (<-chan PermissionChange, error) {
	return make(chan PermissionChange), nil
}
func (emptyConnector) Snapshot(context.Context, string) (PermissionSet, error) {
	return PermissionSet{TenantID: "tenant_demo"}, nil
}

type countingMetrics struct {
	drift atomic.Int64
	runs  atomic.Int64
	errs  atomic.Int64
}

func (c *countingMetrics) SyncRun(string)                     { c.runs.Add(1) }
func (c *countingMetrics) SyncError(string)                   { c.errs.Add(1) }
func (c *countingMetrics) DriftItems(string, int)             { c.drift.Add(1) }
func (c *countingMetrics) SyncDuration(string, time.Duration) {}

// --- helpers ---

func waitUntil(t *testing.T, cond func() bool, timeout time.Duration, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for: %s", msg)
}

func hasTuple(fga *MemoryFGA, tenant string, tup Tuple) bool {
	tuples, _ := fga.ListTuples(context.Background(), tenant)
	for _, x := range tuples {
		if x == tup {
			return true
		}
	}
	return false
}

const financeMemberTuple = `user:finance_user member group:finance`

var financeTuple = Tuple{User: "user:finance_user", Relation: "member", Object: "group:finance"}

// --- tests ---

func TestServiceOnceModePerformsOneSyncAndExits(t *testing.T) {
	connector := NewMockConnector()
	fga := NewMemoryFGA()
	svc := NewService(connector, NewSyncer(connector, fga, discardLogger()),
		Config{Mode: ModeOnce, TenantID: "tenant_demo"}, discardLogger(), nil)

	// Background context that is NOT cancelled: once mode must return on its own.
	done := make(chan error, 1)
	go func() { done <- svc.Run(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("once mode returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("once mode did not exit on its own")
	}
	if !hasTuple(fga, "tenant_demo", financeTuple) {
		t.Fatal("once mode should have performed the initial sync")
	}
}

func TestServiceWatchModeAppliesRevocation(t *testing.T) {
	connector := NewMockConnector()
	fga := NewMemoryFGA()
	cfg := Config{Mode: ModeWatch, TenantID: "tenant_demo", SyncInterval: time.Hour, DriftCheckInterval: time.Hour}
	svc := NewService(connector, NewSyncer(connector, fga, discardLogger()), cfg, discardLogger(), nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx) }()

	waitUntil(t, func() bool { return hasTuple(fga, "tenant_demo", financeTuple) }, 2*time.Second, "initial sync writes finance tuple")

	// Revoke at the source -> emits a change on the watch channel -> service applies delete.
	connector.RevokeGroupMember("finance", "finance_user")
	waitUntil(t, func() bool { return !hasTuple(fga, "tenant_demo", financeTuple) }, 2*time.Second, "watch applies revocation ("+financeMemberTuple+" deleted)")

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("service did not stop after cancel")
	}
}

func TestServiceRetriesOnSinkFailureInsteadOfCrashing(t *testing.T) {
	under := NewMemoryFGA()
	flaky := &flakySink{inner: under, failWrites: 2} // first two writes fail, third succeeds
	connector := NewMockConnector()
	cfg := Config{Mode: ModeOnce, TenantID: "tenant_demo", BackoffBase: time.Millisecond, BackoffMax: 5 * time.Millisecond}
	svc := NewService(connector, NewSyncer(connector, flaky, discardLogger()), cfg, discardLogger(), nil)

	if err := svc.Run(context.Background()); err != nil {
		t.Fatalf("once mode should eventually succeed, got: %v", err)
	}
	if flaky.writeAttempts() < 3 {
		t.Fatalf("expected retries (>=3 write attempts), got %d", flaky.writeAttempts())
	}
	if !hasTuple(under, "tenant_demo", financeTuple) {
		t.Fatal("sync should have eventually written tuples after retries")
	}
}

func TestServiceGracefulShutdownStopsLoop(t *testing.T) {
	under := NewMemoryFGA()
	always := &flakySink{inner: under, failWrites: 1 << 30} // effectively always fails writes
	connector := NewMockConnector()
	cfg := Config{Mode: ModeWatch, TenantID: "tenant_demo", BackoffBase: 5 * time.Millisecond, BackoffMax: 10 * time.Millisecond, SyncInterval: time.Hour, DriftCheckInterval: time.Hour}
	svc := NewService(connector, NewSyncer(connector, always, discardLogger()), cfg, discardLogger(), nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx) }()
	time.Sleep(30 * time.Millisecond) // let it retry the (failing) initial sync a few times
	cancel()

	select {
	case <-done: // returned promptly on shutdown — did not hang or crash
	case <-time.After(2 * time.Second):
		t.Fatal("service did not stop on graceful shutdown")
	}
}

func TestServiceDriftCheckRunsOnInterval(t *testing.T) {
	connector := NewMockConnector()
	fga := NewMemoryFGA()
	cm := &countingMetrics{}
	cfg := Config{Mode: ModeWatch, TenantID: "tenant_demo", SyncInterval: time.Hour, DriftCheckInterval: 20 * time.Millisecond}
	svc := NewService(connector, NewSyncer(connector, fga, discardLogger()), cfg, discardLogger(), cm)

	ctx, cancel := context.WithTimeout(context.Background(), 160*time.Millisecond)
	defer cancel()
	_ = svc.Run(ctx)

	if cm.drift.Load() < 1 {
		t.Fatalf("expected >=1 drift check on interval, got %d", cm.drift.Load())
	}
}

func TestNoDestructiveDeleteWithoutConfirmedSnapshot(t *testing.T) {
	ctx := context.Background()
	fga := NewMemoryFGA()
	// Populate the sink with a real sync first.
	if _, err := NewSyncer(NewMockConnector(), fga, discardLogger()).SyncToOpenFGA(ctx, "tenant_demo"); err != nil {
		t.Fatal(err)
	}
	before, _ := fga.ListTuples(ctx, "tenant_demo")
	if len(before) == 0 {
		t.Fatal("precondition: sink should have tuples")
	}

	// Now sync with an EMPTY connector snapshot — must NOT delete anything.
	res, err := NewSyncer(emptyConnector{}, fga, discardLogger()).SyncToOpenFGA(ctx, "tenant_demo")
	if err != nil {
		t.Fatal(err)
	}
	if res.TuplesDeleted != 0 {
		t.Fatalf("empty snapshot must not delete tuples, deleted %d", res.TuplesDeleted)
	}
	after, _ := fga.ListTuples(ctx, "tenant_demo")
	if len(after) != len(before) {
		t.Fatalf("tuples must be preserved on empty snapshot: before=%d after=%d", len(before), len(after))
	}
}
