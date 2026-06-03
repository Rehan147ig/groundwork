package metrics

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	QueryTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "groundwork_queries_total", Help: "Total queries processed"},
		[]string{"tenant_id", "outcome"},
	)
	ACLCheckDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "groundwork_acl_check_duration_seconds",
			Help:    "Duration of ACL checks",
			Buckets: []float64{.005, .01, .025, .05, .1},
		},
		[]string{"tenant_id", "result"},
	)
	ChunksBlockedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "groundwork_chunks_blocked_total", Help: "Total chunks blocked"},
		[]string{"tenant_id", "reason"},
	)
	CircuitBreakerState = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Name: "groundwork_circuit_breaker_state", Help: "Circuit breaker state"},
		[]string{"service"},
	)
	OpenFGAUnreachable = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "groundwork_openfga_unreachable_total", Help: "OpenFGA unreachable count"},
		[]string{"tenant_id"},
	)
	TenantQueryLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "groundwork_query_latency_seconds",
			Help:    "End-to-end query latency",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"tenant_id"},
	)

	registerOnce sync.Once
)

func RegisterAll() {
	registerOnce.Do(func() {
		prometheus.MustRegister(
			QueryTotal,
			ACLCheckDuration,
			ChunksBlockedTotal,
			CircuitBreakerState,
			OpenFGAUnreachable,
			TenantQueryLatency,
		)
	})
}

func RecordQuery(tenantID, outcome string) {
	QueryTotal.WithLabelValues(tenantID, outcome).Inc()
}

func RecordACLCheck(tenantID, result string, duration time.Duration) {
	ACLCheckDuration.WithLabelValues(tenantID, result).Observe(duration.Seconds())
}

func RecordBlockedChunks(tenantID, reason string, count int) {
	if count <= 0 {
		return
	}
	ChunksBlockedTotal.WithLabelValues(tenantID, reason).Add(float64(count))
}

func SetCircuitBreakerState(service string, state float64) {
	CircuitBreakerState.WithLabelValues(service).Set(state)
}

func RecordOpenFGAUnreachable(tenantID string) {
	OpenFGAUnreachable.WithLabelValues(tenantID).Inc()
}

func RecordQueryLatency(tenantID string, duration time.Duration) {
	TenantQueryLatency.WithLabelValues(tenantID).Observe(duration.Seconds())
}
