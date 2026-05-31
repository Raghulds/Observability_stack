package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	httpRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests processed, partitioned by method, path and status code.",
		},
		[]string{"method", "path", "status"},
	)

	httpInFlightRequests = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "http_in_flight_requests",
			Help: "Current number of HTTP requests being served.",
		},
	)

	httpRequestDurationSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request latency distribution in seconds.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path"},
	)

	httpResponseSizeBytes = promauto.NewSummaryVec(
		prometheus.SummaryOpts{
			Name:       "http_response_size_bytes",
			Help:       "HTTP response body size distribution in bytes.",
			Objectives: map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.99: 0.001},
		},
		[]string{"method", "path"},
	)
)

// ResponseRecorder is a wrapper around http.ResponseWriter that tracks the status code and the number of bytes written.
type responseRecorder struct {
	http.ResponseWriter
	status       int
	bytesWritten int64
}

func (r *responseRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytesWritten += int64(n)
	return n, err
}

func instrument(path string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()

		// Track in-flight requests
		httpInFlightRequests.Inc()
		defer httpInFlightRequests.Dec()

		rec := &responseRecorder{ResponseWriter: w}
		h(rec, req)

		status := strconv.Itoa(rec.status)
		duration := time.Since(start)
		httpRequestsTotal.WithLabelValues(req.Method, path, status).Inc()
		httpRequestDurationSeconds.WithLabelValues(req.Method, path).Observe(duration.Seconds())
		httpResponseSizeBytes.WithLabelValues(req.Method, path).Observe(float64(rec.bytesWritten))

		level := slog.LevelInfo
		if rec.status >= 500 {
			level = slog.LevelError
		} else if rec.status >= 400 {
			level = slog.LevelWarn
		}
		slog.LogAttrs(req.Context(), level, "http_request",
			slog.String("method", req.Method),
			slog.String("path", path),
			slog.Int("status", rec.status),
			slog.Int64("duration_ms", duration.Milliseconds()),
			slog.Int64("bytes", rec.bytesWritten),
			slog.String("remote_addr", req.RemoteAddr),
			slog.String("user_agent", req.UserAgent()),
		)
	}
}

func rootHandler(w http.ResponseWriter, _ *http.Request) {
	fmt.Fprintln(w, "ok")
}

func workHandler(w http.ResponseWriter, _ *http.Request) {
	time.Sleep(time.Duration(rand.Intn(500)) * time.Millisecond)
	size := 256 + rand.Intn(8*1024-256)
	payload := make([]byte, size)
	for i := range payload {
		payload[i] = 'a' + byte(rand.Intn(26))
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = w.Write(payload)
}

func failHandler(w http.ResponseWriter, _ *http.Request) {
	if rand.Float64() < 0.3 {
		http.Error(w, "synthetic failure", http.StatusInternalServerError)
		return
	}
	fmt.Fprintln(w, "ok")
}

func simulateHandler(w http.ResponseWriter, req *http.Request) {
	rps, err := strconv.Atoi(req.URL.Query().Get("rps"))
	if err != nil || rps <= 0 {
		rps = 10
	}
	seconds, err := strconv.Atoi(req.URL.Query().Get("seconds"))
	if err != nil || seconds <= 0 {
		seconds = 30
	}
	if rps > 500 {
		rps = 500
	}
	if seconds > 600 {
		seconds = 600
	}

	port := req.Context().Value(portKey{}).(string)
	base := "http://127.0.0.1:" + port
	targets := []string{base + "/work", base + "/fail"}

	var sent int64
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(seconds)*time.Second)
	go func() {
		defer cancel()
		ticker := time.NewTicker(time.Second / time.Duration(rps))
		defer ticker.Stop()
		client := &http.Client{Timeout: 5 * time.Second}
		var wg sync.WaitGroup
		for {
			select {
			case <-ctx.Done():
				wg.Wait()
				return
			case <-ticker.C:
				wg.Add(1)
				go func() {
					defer wg.Done()
					target := targets[rand.Intn(len(targets))]
					resp, err := client.Get(target)
					if err == nil {
						_ = resp.Body.Close()
					}
					atomic.AddInt64(&sent, 1)
				}()
			}
		}
	}()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":  "started",
		"rps":     rps,
		"seconds": seconds,
		"targets": targets,
	})
}

func healthzHandler(w http.ResponseWriter, _ *http.Request) {
	fmt.Fprintln(w, "ok")
}

type portKey struct{}

func main() {
	slogHandlerOpts := &slog.HandlerOptions{Level: slog.LevelInfo}
	slogJsonHandler := slog.NewJSONHandler(os.Stdout, slogHandlerOpts)
	logger := slog.New(slogJsonHandler)
	slog.SetDefault(logger)

	addr := ":2112"
	port := "2112"

	mux := http.NewServeMux()
	mux.HandleFunc("/", instrument("/", rootHandler))
	mux.HandleFunc("/work", instrument("/work", workHandler))
	mux.HandleFunc("/fail", instrument("/fail", failHandler))
	mux.HandleFunc("/simulate", instrument("/simulate", simulateHandler))
	mux.HandleFunc("/healthz", healthzHandler)
	mux.Handle("/metrics", promhttp.Handler())

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		BaseContext: func(_ net.Listener) context.Context {
			return context.WithValue(context.Background(), portKey{}, port)
		},
	}

	slog.Info("server_listening", "addr", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("server_error", "err", err.Error())
		os.Exit(1)
	}
}
