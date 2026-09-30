package web

import (
	"feedme/internal/pipeline"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds the Prometheus metrics for the server.
type Metrics struct {
	// FeedsBuilt counts how many feeds were successfully built.
	FeedsBuilt *prometheus.CounterVec
	// FeedsFailed counts how many feed builds failed.
	FeedsFailed *prometheus.CounterVec
	// ItemsExtracted counts how many items were extracted.
	ItemsExtracted *prometheus.CounterVec
	// FulltextFetched counts how many full-text articles were fetched.
	FulltextFetched *prometheus.CounterVec
	// FulltextFailed counts how many full-text fetches failed.
	FulltextFailed *prometheus.CounterVec
	// RobotsBlocked counts how many requests were blocked by robots.txt.
	RobotsBlocked *prometheus.CounterVec
	// BuildDuration measures the duration of feed builds.
	BuildDuration *prometheus.HistogramVec
	// FetchDuration measures the duration of source fetches.
	FetchDuration *prometheus.HistogramVec
}

// NewMetrics creates and registers all metrics.
func NewMetrics() *Metrics {
	m := &Metrics{
		FeedsBuilt: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "feedme_feeds_built_total",
			Help: "Total number of feeds successfully built.",
		}, []string{"format", "detection"}),
		FeedsFailed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "feedme_feeds_failed_total",
			Help: "Total number of feed builds that failed.",
		}, []string{"error_type"}),
		ItemsExtracted: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "feedme_items_extracted_total",
			Help: "Total number of items extracted from feeds.",
		}, []string{"source"}),
		FulltextFetched: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "feedme_fulltext_fetched_total",
			Help: "Total number of full-text articles successfully fetched.",
		}, []string{"source"}),
		FulltextFailed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "feedme_fulltext_failed_total",
			Help: "Total number of full-text article fetches that failed.",
		}, []string{"source"}),
		RobotsBlocked: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "feedme_robots_blocked_total",
			Help: "Total number of requests blocked by robots.txt.",
		}, []string{"host"}),
		BuildDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "feedme_build_duration_seconds",
			Help:    "Duration of feed builds in seconds.",
			Buckets: prometheus.DefBuckets,
		}, []string{"format"}),
		FetchDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "feedme_fetch_duration_seconds",
			Help:    "Duration of source fetches in seconds.",
			Buckets: prometheus.DefBuckets,
		}, []string{"host"}),
	}

	prometheus.MustRegister(
		m.FeedsBuilt,
		m.FeedsFailed,
		m.ItemsExtracted,
		m.FulltextFetched,
		m.FulltextFailed,
		m.RobotsBlocked,
		m.BuildDuration,
		m.FetchDuration,
	)

	return m
}

// RecordFeedBuilt records a successful feed build.
func (m *Metrics) RecordFeedBuilt(format string, detection string) {
	if m == nil {
		return
	}
	m.FeedsBuilt.WithLabelValues(format, detection).Inc()
}

// RecordFeedFailed records a failed feed build.
func (m *Metrics) RecordFeedFailed(errType string) {
	if m == nil {
		return
	}
	m.FeedsFailed.WithLabelValues(errType).Inc()
}

// RecordItemsExtracted records extracted items.
func (m *Metrics) RecordItemsExtracted(source string, count int) {
	if m == nil {
		return
	}
	m.ItemsExtracted.WithLabelValues(source).Add(float64(count))
}

// RecordFulltextFetched records a successful full-text fetch.
func (m *Metrics) RecordFulltextFetched(source string, count int) {
	if m == nil {
		return
	}
	m.FulltextFetched.WithLabelValues(source).Add(float64(count))
}

// RecordFulltextFailed records a failed full-text fetch.
func (m *Metrics) RecordFulltextFailed(source string, count int) {
	if m == nil {
		return
	}
	m.FulltextFailed.WithLabelValues(source).Add(float64(count))
}

// RecordRobotsBlocked records a robots.txt block.
func (m *Metrics) RecordRobotsBlocked(host string) {
	if m == nil {
		return
	}
	m.RobotsBlocked.WithLabelValues(host).Inc()
}

// RecordBuildDuration records the build duration.
func (m *Metrics) RecordBuildDuration(format string, duration time.Duration) {
	if m == nil {
		return
	}
	m.BuildDuration.WithLabelValues(format).Observe(duration.Seconds())
}

// RecordFetchDuration records the fetch duration.
func (m *Metrics) RecordFetchDuration(host string, duration time.Duration) {
	if m == nil {
		return
	}
	m.FetchDuration.WithLabelValues(host).Observe(duration.Seconds())
}

// MetricsHandler returns an http.Handler for the /metrics endpoint.
func (m *Metrics) MetricsHandler() http.Handler {
	return promhttp.Handler()
}

// errorTypeFromError classifies an error into a stable label.
func errorTypeFromError(err error) string {
	if err == nil {
		return "unknown"
	}
	errStr := strings.ToLower(err.Error())
	switch {
	case strings.Contains(errStr, "robots"):
		return "robots"
	case strings.Contains(errStr, "timeout"):
		return "timeout"
	case strings.Contains(errStr, "blocked"):
		return "blocked"
	case strings.Contains(errStr, "private"):
		return "private"
	case strings.Contains(errStr, "too large"):
		return "too_large"
	case strings.Contains(errStr, "not found"):
		return "not_found"
	case strings.Contains(errStr, "gone"):
		return "gone"
	case strings.Contains(errStr, "rate limit"):
		return "rate_limit"
	case strings.Contains(errStr, "unauthorized"):
		return "unauthorized"
	case strings.Contains(errStr, "forbidden"):
		return "forbidden"
	case strings.Contains(errStr, "bad gateway"):
		return "bad_gateway"
	case strings.Contains(errStr, "context canceled"):
		return "canceled"
	default:
		return "other"
	}
}

// hostFromURL extracts the host from a URL string.
func hostFromURL(raw string) string {
	if raw == "" {
		return "unknown"
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "invalid"
	}
	return u.Hostname()
}

// detectionString returns a string representation of the detection for metrics.
func detectionString(res *pipeline.Result) string {
	if res == nil || res.Detection == nil {
		return "configured"
	}
	return res.Detection.Selector
}
