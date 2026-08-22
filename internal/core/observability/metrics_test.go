package observability

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestHTTPMetricsUseBoundedRouteTemplate(t *testing.T) {
	t.Parallel()
	metrics, err := NewMetrics(nil)
	if err != nil {
		t.Fatalf("create metrics: %v", err)
	}
	handler := metrics.HTTPMiddleware()(TrackRoute(
		"/api/v1/trees/{tree_id}",
		http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusNoContent)
		}),
	))
	request := httptest.NewRequest(
		http.MethodDelete,
		"/api/v1/trees/5cf83d4d-ef50-46e4-b2c2-6ae1388b6ef7?token=secret",
		nil,
	)
	handler.ServeHTTP(httptest.NewRecorder(), request)

	if got := testutil.ToFloat64(metrics.httpRequests.WithLabelValues(
		http.MethodDelete,
		"/api/v1/trees/{tree_id}",
		"204",
	)); got != 1 {
		t.Fatalf("request counter = %v, want 1", got)
	}
	metricFamilies, err := metrics.registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, family := range metricFamilies {
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				if strings.Contains(label.GetValue(), "5cf83d4d") || strings.Contains(label.GetValue(), "secret") {
					t.Fatalf("metric %s contains raw URL data", family.GetName())
				}
			}
		}
	}
}

func TestWorkerMetricsRecordControlledOutcome(t *testing.T) {
	t.Parallel()
	metrics, err := NewMetrics(nil)
	if err != nil {
		t.Fatalf("create metrics: %v", err)
	}
	metrics.JobClaimed("media.process")
	metrics.JobFinished("media.process", "succeeded", 250*time.Millisecond)

	if got := testutil.ToFloat64(metrics.workerJobsClaimed.WithLabelValues("media.process")); got != 1 {
		t.Fatalf("claimed counter = %v, want 1", got)
	}
	if got := testutil.ToFloat64(metrics.workerJobsFinished.WithLabelValues("media.process", "succeeded")); got != 1 {
		t.Fatalf("finished counter = %v, want 1", got)
	}
}

func TestTrackRouteFallsBackToUnmatched(t *testing.T) {
	t.Parallel()
	request := httptest.NewRequest(http.MethodGet, "/missing", nil)
	if got, want := Route(request), "unmatched"; got != want {
		t.Fatalf("Route() = %q, want %q", got, want)
	}
}
