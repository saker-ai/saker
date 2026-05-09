package server

import (
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "saker",
			Name:      "http_requests_total",
			Help:      "Total number of HTTP requests processed.",
		},
		[]string{"method", "path", "status"},
	)
	httpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "saker",
			Name:      "http_request_duration_seconds",
			Help:      "HTTP request latency in seconds.",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"method", "path"},
	)
	wsConnectionsActive = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "saker",
			Name:      "websocket_connections_active",
			Help:      "Number of currently active WebSocket connections.",
		},
	)
)

func init() {
	prometheus.MustRegister(httpRequestsTotal)
	prometheus.MustRegister(httpRequestDuration)
	prometheus.MustRegister(wsConnectionsActive)
}

// PrometheusMiddleware records request latency and counts for every HTTP request.
func PrometheusMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		duration := time.Since(start).Seconds()
		status := c.Writer.Status()
		method := c.Request.Method
		path := c.FullPath()
		if path == "" {
			path = "unknown"
		}
		httpRequestsTotal.WithLabelValues(method, path, fmt.Sprintf("%d", status)).Inc()
		httpRequestDuration.WithLabelValues(method, path).Observe(duration)
	}
}

// PrometheusHandler returns an http.Handler that serves the /metrics endpoint.
func PrometheusHandler() gin.HandlerFunc {
	return gin.WrapH(promhttp.Handler())
}

// IncWSConnections increments the active WebSocket gauge.
func IncWSConnections() {
	wsConnectionsActive.Inc()
}

// DecWSConnections decrements the active WebSocket gauge.
func DecWSConnections() {
	wsConnectionsActive.Dec()
}