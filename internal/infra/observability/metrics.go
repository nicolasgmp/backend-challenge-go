package observability

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"jungle-gaming-challeng/internal/app"
)

type Metrics struct {
	registry     *prometheus.Registry
	transactions *prometheus.CounterVec
	duration     *prometheus.HistogramVec
	duplicates   *prometheus.CounterVec
	retries      *prometheus.CounterVec
	deadLetters  *prometheus.CounterVec
	conflicts    *prometheus.CounterVec
	outboxLag    prometheus.Gauge
	divergences  prometheus.Counter
}

var _ app.Metrics = (*Metrics)(nil)

func NewMetrics() *Metrics {
	m := &Metrics{
		registry: prometheus.NewRegistry(),
		transactions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "wager_transactions_total",
			Help: "Wager transactions concluded, by channel, kind, status and failure code.",
		}, []string{"channel", "kind", "status", "failure_code"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "processing_duration_seconds",
			Help:    "Time spent handling one operation, by channel.",
			Buckets: prometheus.DefBuckets,
		}, []string{"channel"}),
		duplicates: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "duplicates_total",
			Help: "Repeated deliveries recognised by the application, by source.",
		}, []string{"source"}),
		retries: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "retries_total",
			Help: "Work postponed after a transient failure, by component.",
		}, []string{"component"}),
		deadLetters: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "dlq_messages_total",
			Help: "Messages sent to the dead-letter queue, by reason.",
		}, []string{"reason"}),
		conflicts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "concurrency_conflicts_total",
			Help: "Lost races between writers, by type.",
		}, []string{"type"}),
		outboxLag: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "outbox_lag_seconds",
			Help: "Age of the oldest event not yet published.",
		}),
		divergences: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "reconciliation_divergences_total",
			Help: "Reconciliations whose stored balance differs from the ledger.",
		}),
	}
	m.registry.MustRegister(m.transactions, m.duration, m.duplicates, m.retries, m.deadLetters, m.conflicts, m.outboxLag, m.divergences)
	return m
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

func (m *Metrics) TransactionResult(channel, kind, status, failureCode string) {
	m.transactions.WithLabelValues(channel, kind, status, failureCode).Inc()
}

func (m *Metrics) ProcessingDuration(channel string, duration time.Duration) {
	m.duration.WithLabelValues(channel).Observe(duration.Seconds())
}

func (m *Metrics) Duplicate(source string) {
	m.duplicates.WithLabelValues(source).Inc()
}

func (m *Metrics) Retry(component string) {
	m.retries.WithLabelValues(component).Inc()
}

func (m *Metrics) DeadLetter(reason string) {
	m.deadLetters.WithLabelValues(reason).Inc()
}

func (m *Metrics) ConcurrencyConflict(conflict string) {
	m.conflicts.WithLabelValues(conflict).Inc()
}

func (m *Metrics) OutboxLag(age time.Duration) {
	m.outboxLag.Set(age.Seconds())
}

func (m *Metrics) ReconciliationDivergence() {
	m.divergences.Inc()
}
