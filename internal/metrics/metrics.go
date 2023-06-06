// Package metrics exports some utility functions for handling prometheus metrics
// from ConditionOrc
package metrics

import (
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	apiLatencySeconds    *prometheus.HistogramVec
	dependencyErrorCount *prometheus.CounterVec
)

func init() {
	dependencyErrorCount = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "conditionorc",
			Subsystem: "dependencies",
			Name:      "errors_total",
			Help:      "a count of all errors attempting to reach conditionorc dependencies",
		}, []string{
			"dependency_name",
		},
	)
	apiLatencySeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "conditionorc",
			Subsystem: "api",
			Name:      "latency_seconds",
			Help:      "api latency measurements in seconds",
			// XXX: will need to tune these buckets once we understand common behaviors better
			// buckets between 25ms to 10 s
			Buckets: []float64{0.025, 0.05, 0.1, 0.25, 0.5, 0.75, 1.0, 2.5, 5.0, 7.5, 10.0},
		}, []string{
			"endpoint",
			"response_code",
		},
	)
}

// ListenAndServeMetrics exposes prometheus metrics as /metrics on port 9090
func ListenAndServe() {
	endpoint := "0.0.0.0:9090"

	go func() {
		http.Handle("/metrics", promhttp.Handler())

		server := &http.Server{
			Addr:              endpoint,
			ReadHeaderTimeout: 2 * time.Second,
		}

		if err := server.ListenAndServe(); err != nil {
			log.Println(err)
		}
	}()
}

// DependencyError provides a convenience method to hide some prometheus implementation
// details.
func DependencyError(name string) {
	dependencyErrorCount.WithLabelValues(name).Inc()
}

// APICallEpilog observes the results and latency of an API call
func APICallEpilog(start time.Time, endpoint string, responseCode int) {
	code := strconv.Itoa(responseCode)
	elapsed := time.Since(start).Seconds()
	apiLatencySeconds.WithLabelValues(endpoint, code).Observe(elapsed)
}
