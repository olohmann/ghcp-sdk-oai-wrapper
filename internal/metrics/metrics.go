package metrics

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// HTTP metrics
var (
	HTTPRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Total number of HTTP requests.",
	}, []string{"method", "path", "status"})

	HTTPRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request duration in seconds.",
		Buckets: []float64{0.1, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300},
	}, []string{"method", "path"})

	HTTPRequestsInFlight = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "http_requests_in_flight",
		Help: "Number of HTTP requests currently being processed.",
	})
)

// Copilot-specific metrics
var (
	ChatCompletionsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "copilot_chat_completions_total",
		Help: "Total number of chat completion requests.",
	}, []string{"model", "stream", "status"})

	ChatCompletionDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "copilot_chat_completion_duration_seconds",
		Help:    "Chat completion request duration in seconds.",
		Buckets: []float64{0.5, 1, 2.5, 5, 10, 30, 60, 120, 300},
	}, []string{"model", "stream"})

	ImageAttachmentsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "copilot_image_attachments_total",
		Help: "Total number of image attachments processed.",
	})
)

// RecordCompletion records metrics for a chat completion request.
func RecordCompletion(model string, stream bool, status string, duration time.Duration) {
	s := "false"
	if stream {
		s = "true"
	}
	m := sanitizeModel(model)
	ChatCompletionsTotal.WithLabelValues(m, s, status).Inc()
	ChatCompletionDuration.WithLabelValues(m, s).Observe(duration.Seconds())
}

// sanitizeModel limits the model label to prevent unbounded cardinality.
// Unknown models are grouped under "other" to avoid metrics explosion from
// user-controlled input.
func sanitizeModel(model string) string {
	if len(model) > 64 {
		return "other"
	}
	// Allow known prefixes used by Copilot models.
	for _, prefix := range []string{
		"gpt-", "claude-", "o1-", "o3-", "o4-",
		"copilot-", "gemini-",
	} {
		if strings.HasPrefix(model, prefix) {
			return model
		}
	}
	return "other"
}

// RecordImageAttachments increments the image attachment counter.
func RecordImageAttachments(count int) {
	ImageAttachmentsTotal.Add(float64(count))
}

// responseWriter wraps http.ResponseWriter to capture the status code.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

func (rw *responseWriter) WriteHeader(code int) {
	if !rw.written {
		rw.statusCode = code
		rw.written = true
	}
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.written {
		rw.statusCode = http.StatusOK
		rw.written = true
	}
	return rw.ResponseWriter.Write(b)
}

// Flush implements http.Flusher for SSE streaming support.
func (rw *responseWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// normalizePath maps request paths to label-safe metric paths.
func normalizePath(path string) string {
	switch path {
	case "/healthz",
		"/v1/models",
		"/v1/chat/completions",
		"/api/chat",
		"/api/generate",
		"/api/tags",
		"/api/show",
		"/api/version":
		return path
	default:
		return "/other"
	}
}

// Middleware returns HTTP middleware that records request metrics.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}

		start := time.Now()
		path := normalizePath(r.URL.Path)

		HTTPRequestsInFlight.Inc()
		defer HTTPRequestsInFlight.Dec()

		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rw, r)

		duration := time.Since(start)
		HTTPRequestsTotal.WithLabelValues(r.Method, path, strconv.Itoa(rw.statusCode)).Inc()
		HTTPRequestDuration.WithLabelValues(r.Method, path).Observe(duration.Seconds())
	})
}
