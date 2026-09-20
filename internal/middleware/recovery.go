package middleware

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
)

// Recovery returns a panic recovery middleware that logs the stack trace in structured JSON
// and returns a safe HTTP 500 error without crashing the server.
func Recovery(logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					reqID := GetRequestID(r.Context())
					stack := string(debug.Stack())

					logger.ErrorContext(r.Context(), "Unhandled panic recovered in HTTP handler",
						slog.String("request_id", reqID),
						slog.Any("panic", rec),
						slog.String("stack", stack),
						slog.String("method", r.Method),
						slog.String("path", r.URL.Path),
					)

					w.Header().Set("Content-Type", "application/json; charset=utf-8")
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = fmt.Fprintf(w, `{"error":"Internal Server Error","request_id":"%s"}`+"\n", reqID)
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}
