// Package metrics owns the process-wide Prometheus registry and every custom
// metric collector exposed by the server. It is intentionally a leaf package:
// it must never import any other internal package (agent, ws, ghsync,
// tasksource, ghclient, storage, ...) so that any of those packages can
// import metrics without risking an import cycle.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// registry is a dedicated registry (rather than the global
// prometheus.DefaultRegisterer) so the /metrics handler only ever exposes
// metrics this package explicitly registers, plus the standard Go/process
// collectors registered below.
var registry = prometheus.NewRegistry()

func init() {
	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
}

// Handler returns an http.Handler that serves the Prometheus text exposition
// format for every metric registered on this package's registry.
func Handler() http.Handler {
	return promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
}

var factory = promauto.With(registry)

// Dispatcher / pool metrics.
var (
	// DispatchEligibleTasks is the number of tasks the dispatcher found
	// eligible for pickup on the most recent sweep.
	DispatchEligibleTasks = factory.NewGauge(prometheus.GaugeOpts{
		Name: "ate_dispatch_eligible_tasks",
		Help: "Number of tasks eligible for agent pickup on the most recent dispatcher sweep.",
	})

	// DispatchedRunsTotal counts every run successfully started by the
	// dispatcher, whether from a sweep pickup or a human-reply dispatch.
	DispatchedRunsTotal = factory.NewCounter(prometheus.CounterOpts{
		Name: "ate_dispatched_runs_total",
		Help: "Total number of agent runs successfully started by the dispatcher.",
	})

	// PoolQueueDepth is the current number of jobs buffered in the pool's
	// job channel, waiting for a free worker.
	PoolQueueDepth = factory.NewGauge(prometheus.GaugeOpts{
		Name: "ate_pool_queue_depth",
		Help: "Current number of jobs queued in the worker pool, waiting for a free worker.",
	})

	// PoolBusyWorkers is the current number of workers actively running a job.
	PoolBusyWorkers = factory.NewGauge(prometheus.GaugeOpts{
		Name: "ate_pool_busy_workers",
		Help: "Current number of worker pool goroutines actively running an agent job.",
	})

	// PoolMaxWorkers is the configured size of the worker pool (MAX_WORKERS).
	PoolMaxWorkers = factory.NewGauge(prometheus.GaugeOpts{
		Name: "ate_pool_max_workers",
		Help: "Configured maximum number of concurrent worker pool goroutines.",
	})

	// PoolSubmitRejectedTotal counts jobs dropped because the pool's queue was full.
	PoolSubmitRejectedTotal = factory.NewCounter(prometheus.CounterOpts{
		Name: "ate_pool_submit_rejected_total",
		Help: "Total number of jobs rejected because the worker pool queue was full.",
	})
)

// Run metrics.
var (
	// RunTerminalTotal counts agent runs by terminal status.
	RunTerminalTotal = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "ate_run_terminal_total",
		Help: "Total number of agent runs reaching a terminal status, labeled by status.",
	}, []string{"status"})

	// RunClassificationTotal counts failed runs by failure classification.
	RunClassificationTotal = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "ate_run_classification_total",
		Help: "Total number of agent run failures, labeled by classification (genuine/transient/rate_limit/auth).",
	}, []string{"classification"})

	// RunDurationSeconds observes the wall-clock duration of an agent run
	// from start to terminal outcome, labeled by provider.
	RunDurationSeconds = factory.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "ate_run_duration_seconds",
		Help:    "Agent run duration in seconds from start to terminal outcome, labeled by provider.",
		Buckets: prometheus.ExponentialBuckets(1, 2, 14), // 1s .. ~2h16m
	}, []string{"provider"})
)

// Cost / token metrics.
var (
	// RunCostUSDTotal accumulates recorded run cost in USD, labeled by
	// provider and agent config name. Both label sets are small and
	// operator-controlled — never label by task_id/run_id (unbounded cardinality).
	RunCostUSDTotal = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "ate_run_cost_usd_total",
		Help: "Total recorded agent run cost in USD, labeled by provider and agent config name.",
	}, []string{"provider", "agent_config_name"})

	// RunInputTokensTotal accumulates input tokens consumed, labeled by
	// provider and agent config name.
	RunInputTokensTotal = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "ate_run_input_tokens_total",
		Help: "Total input tokens consumed by agent runs, labeled by provider and agent config name.",
	}, []string{"provider", "agent_config_name"})

	// RunOutputTokensTotal accumulates output tokens produced, labeled by
	// provider and agent config name.
	RunOutputTokensTotal = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "ate_run_output_tokens_total",
		Help: "Total output tokens produced by agent runs, labeled by provider and agent config name.",
	}, []string{"provider", "agent_config_name"})
)

// WebSocket metrics.
var (
	// WSConnectedClients is the current number of connected WebSocket clients.
	WSConnectedClients = factory.NewGauge(prometheus.GaugeOpts{
		Name: "ate_ws_connected_clients",
		Help: "Current number of connected WebSocket clients.",
	})

	// WSBroadcastDroppedTotal counts events dropped because a client's send
	// buffer was full.
	WSBroadcastDroppedTotal = factory.NewCounter(prometheus.CounterOpts{
		Name: "ate_ws_broadcast_dropped_total",
		Help: "Total number of WebSocket events dropped because a client's send buffer was full.",
	})
)

// Sync-loop metrics.
var (
	// GhsyncSweepDurationSeconds observes the wall-clock duration of each
	// ghsync (GitHub PR status) sweep.
	GhsyncSweepDurationSeconds = factory.NewHistogram(prometheus.HistogramOpts{
		Name:    "ate_ghsync_sweep_duration_seconds",
		Help:    "Duration in seconds of each ghsync PR-status sweep.",
		Buckets: prometheus.DefBuckets,
	})

	// TasksourceSweepDurationSeconds observes the wall-clock duration of each
	// tasksource (GitHub issue import) sweep.
	TasksourceSweepDurationSeconds = factory.NewHistogram(prometheus.HistogramOpts{
		Name:    "ate_tasksource_sweep_duration_seconds",
		Help:    "Duration in seconds of each tasksource issue-import sweep.",
		Buckets: prometheus.DefBuckets,
	})

	// GhCallsTotal counts `gh` CLI invocations, labeled by logical command
	// name, serving as an early warning signal for GitHub API rate limiting.
	GhCallsTotal = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "ate_gh_calls_total",
		Help: "Total number of `gh` CLI invocations, labeled by logical command.",
	}, []string{"command"})
)
