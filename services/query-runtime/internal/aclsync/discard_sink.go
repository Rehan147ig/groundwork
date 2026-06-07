package aclsync

import (
	"context"
	"log/slog"
	"sync"
)

// DiscardSink is a TupleSink that records writes/deletes in memory but never
// contacts OpenFGA. It exists for scaffolding and wire-up validation: PR #18
// of the Microsoft Graph pilot uses it so the connector can authenticate,
// snapshot the directory, and execute the full Service.RunOnce pipeline
// WITHOUT producing any real OpenFGA state.
//
// Production deployments use OpenFGASink. Tests typically use MemoryFGA
// (which also implements runtime.ACLChecker so the engine can be exercised).
// DiscardSink is the right choice when neither is desired — usually when the
// goal is to prove the upstream wiring works while leaving OpenFGA untouched.
type DiscardSink struct {
	mu      sync.Mutex
	logger  *slog.Logger
	writes  []Tuple
	deletes []Tuple
}

// NewDiscardSink returns a DiscardSink that logs every operation it would
// have performed. A nil logger defaults to slog.Default().
func NewDiscardSink(logger *slog.Logger) *DiscardSink {
	if logger == nil {
		logger = slog.Default()
	}
	return &DiscardSink{logger: logger}
}

// ListTuples always returns an empty slice. The Syncer's diff treats this as
// "OpenFGA currently has nothing", so every desired tuple becomes a write —
// which DiscardSink records but does not propagate.
func (d *DiscardSink) ListTuples(_ context.Context, _ string) ([]Tuple, error) {
	return nil, nil
}

// WriteTuples records the tuples that would have been written.
func (d *DiscardSink) WriteTuples(_ context.Context, tenantID string, tuples []Tuple) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.writes = append(d.writes, tuples...)
	d.logger.Info("discard_sink_write_skipped",
		"tenant", tenantID, "count", len(tuples), "total_observed", len(d.writes))
	return nil
}

// DeleteTuples records the tuples that would have been deleted.
func (d *DiscardSink) DeleteTuples(_ context.Context, tenantID string, tuples []Tuple) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.deletes = append(d.deletes, tuples...)
	d.logger.Info("discard_sink_delete_skipped",
		"tenant", tenantID, "count", len(tuples), "total_observed", len(d.deletes))
	return nil
}

// WrittenCount returns the cumulative number of tuples WriteTuples was called
// with — useful for the binary's summary line.
func (d *DiscardSink) WrittenCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.writes)
}

// DeletedCount returns the cumulative number of tuples DeleteTuples was
// called with.
func (d *DiscardSink) DeletedCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.deletes)
}

// compile-time check
var _ TupleSink = (*DiscardSink)(nil)
