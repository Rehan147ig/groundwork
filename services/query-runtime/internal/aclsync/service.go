package aclsync

import (
	"context"
	"log/slog"
	"math/rand"
	"time"
)

// Mode selects how the sync service runs.
type Mode string

const (
	ModeOnce  Mode = "once"  // one full sync, then exit (safe default)
	ModeWatch Mode = "watch" // continuous: initial sync, then watch + periodic reconcile/drift
)

// Config configures the continuous sync service.
type Config struct {
	Mode               Mode
	TenantID           string
	SyncInterval       time.Duration // periodic full reconcile (watch mode)
	DriftCheckInterval time.Duration // periodic drift check (watch mode)
	BackoffBase        time.Duration // retry backoff base
	BackoffMax         time.Duration // retry backoff cap
}

func (c Config) withDefaults() Config {
	if c.Mode == "" {
		c.Mode = ModeOnce
	}
	if c.TenantID == "" {
		c.TenantID = "tenant_demo"
	}
	if c.SyncInterval <= 0 {
		c.SyncInterval = 60 * time.Second
	}
	if c.DriftCheckInterval <= 0 {
		c.DriftCheckInterval = 300 * time.Second
	}
	if c.BackoffBase <= 0 {
		c.BackoffBase = 500 * time.Millisecond
	}
	if c.BackoffMax <= 0 {
		c.BackoffMax = 30 * time.Second
	}
	return c
}

// Metrics receives sync telemetry. A no-op default is used when none is provided; the
// Prometheus implementation lives in cmd/acl-sync (so this package stays dependency-light).
type Metrics interface {
	SyncRun(tenantID string)
	SyncError(tenantID string)
	DriftItems(tenantID string, n int)
	SyncDuration(tenantID string, d time.Duration)
}

type nopMetrics struct{}

func (nopMetrics) SyncRun(string)                     {}
func (nopMetrics) SyncError(string)                   {}
func (nopMetrics) DriftItems(string, int)             {}
func (nopMetrics) SyncDuration(string, time.Duration) {}

// Service runs the ACL sync either once or continuously. It reuses the Syncer (and thus
// the same OpenFGA tuple model and query path) — it does not touch the query engine.
type Service struct {
	Connector Connector
	Syncer    *Syncer
	Config    Config
	Logger    *slog.Logger
	Metrics   Metrics
}

func NewService(connector Connector, syncer *Syncer, cfg Config, logger *slog.Logger, m Metrics) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	if m == nil {
		m = nopMetrics{}
	}
	return &Service{Connector: connector, Syncer: syncer, Config: cfg.withDefaults(), Logger: logger, Metrics: m}
}

// Run performs an initial full sync, then (in watch mode) keeps OpenFGA reconciled with
// source permission changes until the context is cancelled (graceful shutdown).
func (s *Service) Run(ctx context.Context) error {
	tenant := s.Config.TenantID
	s.Logger.Info("acl_sync_service_started",
		"tenant", tenant, "mode", string(s.Config.Mode),
		"sync_interval", s.Config.SyncInterval.String(),
		"drift_interval", s.Config.DriftCheckInterval.String(),
	)
	defer s.Logger.Info("acl_sync_service_stopped", "tenant", tenant)

	// Initial full sync (retried until success or shutdown).
	s.Logger.Info("initial_sync_started", "tenant", tenant)
	if err := s.reconcile(ctx, tenant); err != nil {
		return err // only context cancellation reaches here; other errors are retried
	}
	s.Logger.Info("initial_sync_completed", "tenant", tenant)

	if s.Config.Mode == ModeOnce {
		return nil
	}

	changes, err := s.Connector.WatchPermissionChanges(ctx, tenant)
	if err != nil {
		return err
	}
	syncTicker := time.NewTicker(s.Config.SyncInterval)
	defer syncTicker.Stop()
	driftTicker := time.NewTicker(s.Config.DriftCheckInterval)
	defer driftTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil // graceful shutdown
		case change, ok := <-changes:
			if !ok {
				changes = nil // channel closed; keep periodic reconcile/drift running
				continue
			}
			s.Logger.Info("permission_change_received",
				"tenant", tenant, "type", string(change.Type), "subject", change.Subject, "object", change.Object)
			s.applyChange(ctx, tenant, change)
		case <-syncTicker.C:
			_ = s.reconcile(ctx, tenant)
		case <-driftTicker.C:
			s.driftCheck(ctx, tenant)
		}
	}
}

// reconcile runs a full snapshot-based sync with retry/backoff.
func (s *Service) reconcile(ctx context.Context, tenant string) error {
	return s.withRetry(ctx, "sync", func() error {
		start := time.Now()
		res, err := s.Syncer.SyncToOpenFGA(ctx, tenant)
		if err != nil {
			return err
		}
		s.Metrics.SyncRun(tenant)
		s.Metrics.SyncDuration(tenant, time.Since(start))
		if res.TuplesWritten > 0 {
			s.Logger.Info("tuple_write_success", "tenant", tenant, "count", res.TuplesWritten, "via", "reconcile")
		}
		if res.TuplesDeleted > 0 {
			s.Logger.Info("tuple_delete_success", "tenant", tenant, "count", res.TuplesDeleted, "via", "reconcile")
		}
		return nil
	})
}

// applyChange applies a single source permission change as a targeted tuple write/delete.
// Revocations are explicit deletes (safe). Retried with backoff until applied or shutdown.
func (s *Service) applyChange(ctx context.Context, tenant string, change PermissionChange) {
	writes, deletes := changeToTuples(change)
	if len(writes) == 0 && len(deletes) == 0 {
		return
	}
	_ = s.withRetry(ctx, "apply_change", func() error {
		if len(writes) > 0 {
			if err := s.Syncer.Sink.WriteTuples(ctx, tenant, writes); err != nil {
				return err
			}
			s.Logger.Info("tuple_write_success", "tenant", tenant, "count", len(writes), "via", "change")
		}
		if len(deletes) > 0 {
			if err := s.Syncer.Sink.DeleteTuples(ctx, tenant, deletes); err != nil {
				return err
			}
			s.Logger.Info("tuple_delete_success", "tenant", tenant, "count", len(deletes), "via", "change")
		}
		s.Metrics.SyncRun(tenant)
		return nil
	})
}

func (s *Service) driftCheck(ctx context.Context, tenant string) {
	s.Logger.Info("drift_check_started", "tenant", tenant)
	report, err := s.Syncer.DetectDrift(ctx, tenant)
	if err != nil {
		s.Metrics.SyncError(tenant)
		s.Logger.Error("drift_check_failed", "tenant", tenant, "err", err.Error())
		return
	}
	n := len(report.SourceMissingInFGA) + len(report.FGAExtraNotInSource) +
		len(report.DocumentsMissingInFGA) + len(report.OrphanedFGADocuments)
	s.Metrics.DriftItems(tenant, n)
	s.Logger.Info("drift_check_completed", "tenant", tenant, "drift_items", n, "has_drift", report.HasDrift())
}

// withRetry runs op, retrying with exponential backoff + jitter until it succeeds or the
// context is cancelled. It never crashes on transient backend (e.g. OpenFGA) failures.
func (s *Service) withRetry(ctx context.Context, label string, op func() error) error {
	attempt := 0
	for {
		if err := op(); err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		attempt++
		s.Metrics.SyncError(s.Config.TenantID)
		delay := backoffDelay(s.Config.BackoffBase, s.Config.BackoffMax, attempt)
		s.Logger.Warn("acl_sync_retry",
			"tenant", s.Config.TenantID, "op", label, "attempt", attempt, "delay_ms", delay.Milliseconds())
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
}

// backoffDelay = exponential (base * 2^(attempt-1)) capped at max, with equal jitter.
func backoffDelay(base, max time.Duration, attempt int) time.Duration {
	d := base
	for i := 1; i < attempt && d < max; i++ {
		d *= 2
	}
	if d > max {
		d = max
	}
	if d <= 0 {
		d = base
	}
	half := d / 2
	return half + time.Duration(rand.Int63n(int64(half)+1))
}

// changeToTuples maps a source permission change to OpenFGA tuple writes/deletes.
func changeToTuples(c PermissionChange) (writes, deletes []Tuple) {
	switch c.Type {
	case ChangeAddGroupMember:
		writes = []Tuple{{User: c.Subject, Relation: "member", Object: c.Object}}
	case ChangeRevokeGroupMember:
		deletes = []Tuple{{User: c.Subject, Relation: "member", Object: c.Object}}
	case ChangeRevokeFolderViewer, ChangeRevokeDocumentViewer:
		deletes = []Tuple{{User: c.Subject, Relation: "viewer", Object: c.Object}}
	}
	return writes, deletes
}
