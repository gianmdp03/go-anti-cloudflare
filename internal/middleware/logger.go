package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"time"
)

type contextKey string

const (
	RequestIDHeader             = "X-Request-ID"
	requestIDKey     contextKey = "requestID"
)

// responseRecorder wraps http.ResponseWriter to capture HTTP status code and bytes written.
type responseRecorder struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int64
}

func (rw *responseRecorder) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseRecorder) Write(b []byte) (int, error) {
	if rw.statusCode == 0 {
		rw.statusCode = http.StatusOK
	}
	n, err := rw.ResponseWriter.Write(b)
	rw.bytesWritten += int64(n)
	return n, err
}

// GenerateRequestID generates a cryptographically secure 16-byte random hex string.
func GenerateRequestID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp if entropy fails
		return hex.EncodeToString([]byte(time.Now().Format("20060102150405.000000")))
	}
	return hex.EncodeToString(b)
}

// GetRequestID extracts the request ID from context.
func GetRequestID(ctx context.Context) string {
	if val, ok := ctx.Value(requestIDKey).(string); ok {
		return val
	}
	return ""
}

// Logger returns an HTTP middleware providing structured JSON logging (via slog)
// with request correlation and latency tracking.
func Logger(logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			startTime := time.Now()

			// 1. Resolve or generate Request ID
			reqID := r.Header.Get(RequestIDHeader)
			if reqID == "" {
				reqID = GenerateRequestID()
			}

			// Propagate Request ID header on response
			w.Header().Set(RequestIDHeader, reqID)

			// Store in context for downstream handlers
			ctx := context.WithValue(r.Context(), requestIDKey, reqID)
			r = r.WithContext(ctx)

			// Wrap ResponseWriter to capture status and size
			rec := &responseRecorder{
				ResponseWriter: w,
				statusCode:     http.StatusOK,
			}

			// Process request
			next.ServeHTTP(rec, r)

			// Calculate latency
			duration := time.Since(startTime)
			durationMs := float64(duration.Microseconds()) / 1000.0

			// Emit structured log entry
			level := slog.LevelInfo
			if rec.statusCode >= 500 {
				level = slog.LevelError
			} else if rec.statusCode >= 400 {
				level = slog.LevelWarn
			}

			logger.Log(ctx, level, "HTTP request handled",
				slog.String("request_id", reqID),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.String("query", r.URL.RawQuery),
				slog.Int("status", rec.statusCode),
				slog.Float64("latency_ms", durationMs),
				slog.Int64("bytes_written", rec.bytesWritten),
				slog.String("remote_addr", r.RemoteAddr),
				slog.String("user_agent", r.UserAgent()),
			)
		})
	}
}
