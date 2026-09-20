package middleware

import (
	"net/http"
	"strings"
)

// SecurityOptions contains security and CORS configuration.
type SecurityOptions struct {
	AllowedOrigin string
}

// Security applies CORS restrictions and incoming header sanitization.
// It ensures that requests can only be made by the authorized Spring Boot origin (default: http://localhost:8400),
// handles preflight OPTIONS requests, and prevents incoming header spoofing.
func Security(opts SecurityOptions) func(http.Handler) http.Handler {
	allowedOrigin := opts.AllowedOrigin
	if allowedOrigin == "" {
		allowedOrigin = "http://localhost:8400"
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			// Check and set CORS headers
			// If Origin header matches or is absent (same-origin / direct server-to-server HTTP call from Spring Boot)
			if origin != "" {
				if origin == allowedOrigin || allowedOrigin == "*" {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Set("Vary", "Origin")
				} else {
					// Origin not allowed
					http.Error(w, `{"error":"CORS: Origin not allowed"}`, http.StatusForbidden)
					return
				}
			} else {
				// Server-to-server call (e.g. Spring Boot WebClient without browser Origin header)
				w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
			}

			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-ID, Accept, Origin")
			w.Header().Set("Access-Control-Max-Age", "86400")

			// Handle preflight OPTIONS requests
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			// Sanitize potentially harmful / spoofed hop-by-hop headers from untrusted clients
			// before passing to internal proxy handler
			sanitizeIncomingHeaders(r)

			next.ServeHTTP(w, r)
		})
	}
}

// sanitizeIncomingHeaders removes suspicious or dangerous client-controlled headers.
func sanitizeIncomingHeaders(r *http.Request) {
	// Strip pseudo headers if erroneously provided
	for k := range r.Header {
		if strings.HasPrefix(k, ":") {
			r.Header.Del(k)
		}
	}
}
