package observability

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/ZheglY/family_tree_app/internal/core/jobs"
	"github.com/ZheglY/family_tree_app/internal/core/transport/http/response"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

const namespace = "family_tree"

type Metrics struct {
	registry           *prometheus.Registry
	httpRequests       *prometheus.CounterVec
	httpDuration       *prometheus.HistogramVec
	httpInFlight       prometheus.Gauge
	workerClaimErrors  prometheus.Counter
	workerJobsClaimed  *prometheus.CounterVec
	workerJobsFinished *prometheus.CounterVec
	workerJobDuration  *prometheus.HistogramVec
}

func NewMetrics(pool *pgxpool.Pool) (*Metrics, error) {
	metrics := &Metrics{
		registry: prometheus.NewRegistry(),
		httpRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "http",
			Name:      "requests_total",
			Help:      "Total HTTP requests completed by method, route and status code.",
		}, []string{"method", "route", "status"}),
		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: "http",
			Name:      "request_duration_seconds",
			Help:      "HTTP request duration in seconds by method and route.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"method", "route"}),
		httpInFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: "http",
			Name:      "requests_in_flight",
			Help:      "Current number of HTTP requests being handled.",
		}),
		workerClaimErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "worker",
			Name:      "claim_errors_total",
			Help:      "Total background job claim failures.",
		}),
		workerJobsClaimed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "worker",
			Name:      "jobs_claimed_total",
			Help:      "Total background jobs claimed by controlled job kind.",
		}, []string{"kind"}),
		workerJobsFinished: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "worker",
			Name:      "jobs_finished_total",
			Help:      "Total background job processing outcomes.",
		}, []string{"kind", "outcome"}),
		workerJobDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: "worker",
			Name:      "job_duration_seconds",
			Help:      "Background job processing duration in seconds.",
			Buckets:   prometheus.ExponentialBuckets(0.1, 2, 12),
		}, []string{"kind", "outcome"}),
	}
	collectors := []prometheus.Collector{
		prometheus.NewGoCollector(),
		prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{Namespace: namespace}),
		metrics.httpRequests,
		metrics.httpDuration,
		metrics.httpInFlight,
		metrics.workerClaimErrors,
		metrics.workerJobsClaimed,
		metrics.workerJobsFinished,
		metrics.workerJobDuration,
	}
	if pool != nil {
		collectors = append(collectors, newPostgresCollector(pool), newJobQueueCollector(pool))
	}
	for _, collector := range collectors {
		if err := metrics.registry.Register(collector); err != nil {
			return nil, err
		}
	}
	return metrics, nil
}

func (metrics *Metrics) Registry() *prometheus.Registry {
	return metrics.registry
}

func (metrics *Metrics) HTTPMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			state := &routeState{}
			request = request.WithContext(context.WithValue(request.Context(), routeKey{}, state))
			responseWriter := response.NewResponseWriter(writer)
			startedAt := time.Now()
			metrics.httpInFlight.Inc()
			defer metrics.httpInFlight.Dec()
			next.ServeHTTP(responseWriter, request)
			route := Route(request)
			metrics.httpRequests.WithLabelValues(
				request.Method,
				route,
				strconv.Itoa(responseWriter.GetStatusCode()),
			).Inc()
			metrics.httpDuration.WithLabelValues(request.Method, route).Observe(time.Since(startedAt).Seconds())
		})
	}
}

func (metrics *Metrics) ClaimError() {
	metrics.workerClaimErrors.Inc()
}

func (metrics *Metrics) JobClaimed(kind string) {
	metrics.workerJobsClaimed.WithLabelValues(kind).Inc()
}

func (metrics *Metrics) JobFinished(kind string, outcome string, duration time.Duration) {
	metrics.workerJobsFinished.WithLabelValues(kind, outcome).Inc()
	metrics.workerJobDuration.WithLabelValues(kind, outcome).Observe(duration.Seconds())
}

type routeKey struct{}

type routeState struct {
	pattern string
}

func TrackRoute(pattern string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if state, ok := request.Context().Value(routeKey{}).(*routeState); ok {
			state.pattern = pattern
		}
		next.ServeHTTP(writer, request)
	})
}

func Route(request *http.Request) string {
	if state, ok := request.Context().Value(routeKey{}).(*routeState); ok && state.pattern != "" {
		return state.pattern
	}
	if request.Pattern != "" {
		return request.Pattern
	}
	return "unmatched"
}

type postgresCollector struct {
	pool                 *pgxpool.Pool
	connections          *prometheus.Desc
	acquires             *prometheus.Desc
	emptyAcquires        *prometheus.Desc
	acquireDuration      *prometheus.Desc
	emptyAcquireWait     *prometheus.Desc
	newConnections       *prometheus.Desc
	destroyedConnections *prometheus.Desc
}

func newPostgresCollector(pool *pgxpool.Pool) prometheus.Collector {
	return &postgresCollector{
		pool: pool,
		connections: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "postgres", "connections"),
			"PostgreSQL pool connections by state.",
			[]string{"state"}, nil,
		),
		acquires: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "postgres", "acquires_total"),
			"PostgreSQL connection acquires by terminal result.",
			[]string{"result"}, nil,
		),
		emptyAcquires: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "postgres", "empty_acquires_total"),
			"PostgreSQL acquires that had to wait because the pool was empty.",
			nil, nil,
		),
		acquireDuration: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "postgres", "acquire_duration_seconds_total"),
			"Cumulative time blocked acquiring PostgreSQL connections.",
			nil, nil,
		),
		emptyAcquireWait: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "postgres", "empty_acquire_wait_seconds_total"),
			"Cumulative time blocked because the PostgreSQL pool was empty.",
			nil, nil,
		),
		newConnections: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "postgres", "connections_created_total"),
			"Total PostgreSQL connections created by the pool.",
			nil, nil,
		),
		destroyedConnections: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "postgres", "connections_destroyed_total"),
			"PostgreSQL connections destroyed by reason.",
			[]string{"reason"}, nil,
		),
	}
}

func (collector *postgresCollector) Describe(channel chan<- *prometheus.Desc) {
	channel <- collector.connections
	channel <- collector.acquires
	channel <- collector.emptyAcquires
	channel <- collector.acquireDuration
	channel <- collector.emptyAcquireWait
	channel <- collector.newConnections
	channel <- collector.destroyedConnections
}

func (collector *postgresCollector) Collect(channel chan<- prometheus.Metric) {
	stats := collector.pool.Stat()
	for state, value := range map[string]float64{
		"max":          float64(stats.MaxConns()),
		"total":        float64(stats.TotalConns()),
		"acquired":     float64(stats.AcquiredConns()),
		"idle":         float64(stats.IdleConns()),
		"constructing": float64(stats.ConstructingConns()),
	} {
		channel <- prometheus.MustNewConstMetric(collector.connections, prometheus.GaugeValue, value, state)
	}
	for result, value := range map[string]float64{
		"success":  float64(stats.AcquireCount()),
		"canceled": float64(stats.CanceledAcquireCount()),
	} {
		channel <- prometheus.MustNewConstMetric(collector.acquires, prometheus.CounterValue, value, result)
	}
	channel <- prometheus.MustNewConstMetric(
		collector.emptyAcquires,
		prometheus.CounterValue,
		float64(stats.EmptyAcquireCount()),
	)
	channel <- prometheus.MustNewConstMetric(
		collector.acquireDuration,
		prometheus.CounterValue,
		stats.AcquireDuration().Seconds(),
	)
	channel <- prometheus.MustNewConstMetric(
		collector.emptyAcquireWait,
		prometheus.CounterValue,
		stats.EmptyAcquireWaitTime().Seconds(),
	)
	channel <- prometheus.MustNewConstMetric(
		collector.newConnections,
		prometheus.CounterValue,
		float64(stats.NewConnsCount()),
	)
	channel <- prometheus.MustNewConstMetric(
		collector.destroyedConnections,
		prometheus.CounterValue,
		float64(stats.MaxLifetimeDestroyCount()),
		"max_lifetime",
	)
	channel <- prometheus.MustNewConstMetric(
		collector.destroyedConnections,
		prometheus.CounterValue,
		float64(stats.MaxIdleDestroyCount()),
		"max_idle",
	)
}

type jobQueueCollector struct {
	pool              *pgxpool.Pool
	jobs              *prometheus.Desc
	oldestRunnableAge *prometheus.Desc
}

func newJobQueueCollector(pool *pgxpool.Pool) prometheus.Collector {
	return &jobQueueCollector{
		pool: pool,
		jobs: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "job_queue", "jobs"),
			"Current background jobs by state.",
			[]string{"status"}, nil,
		),
		oldestRunnableAge: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "job_queue", "oldest_runnable_age_seconds"),
			"Age in seconds of the oldest runnable queued or failed job.",
			nil, nil,
		),
	}
}

func (collector *jobQueueCollector) Describe(channel chan<- *prometheus.Desc) {
	channel <- collector.jobs
	channel <- collector.oldestRunnableAge
}

func (collector *jobQueueCollector) Collect(channel chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var queued, running, failed, succeeded, dead int64
	var oldestRunnableAge float64
	err := collector.pool.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE status = 'queued'),
			COUNT(*) FILTER (WHERE status = 'running'),
			COUNT(*) FILTER (WHERE status = 'failed'),
			COUNT(*) FILTER (WHERE status = 'succeeded'),
			COUNT(*) FILTER (WHERE status = 'dead'),
			COALESCE(EXTRACT(EPOCH FROM (
				CURRENT_TIMESTAMP - MIN(available_at) FILTER (
					WHERE status IN ('queued', 'failed') AND available_at <= CURRENT_TIMESTAMP
				)
			)), 0)
		FROM background_jobs
	`).Scan(&queued, &running, &failed, &succeeded, &dead, &oldestRunnableAge)
	if err != nil {
		channel <- prometheus.NewInvalidMetric(collector.jobs, err)
		return
	}
	for status, value := range map[string]float64{
		jobs.StatusQueued:    float64(queued),
		jobs.StatusRunning:   float64(running),
		jobs.StatusFailed:    float64(failed),
		jobs.StatusSucceeded: float64(succeeded),
		jobs.StatusDead:      float64(dead),
	} {
		channel <- prometheus.MustNewConstMetric(collector.jobs, prometheus.GaugeValue, value, status)
	}
	channel <- prometheus.MustNewConstMetric(
		collector.oldestRunnableAge,
		prometheus.GaugeValue,
		oldestRunnableAge,
	)
}
