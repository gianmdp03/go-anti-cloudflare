package proxy

import (
	"encoding/json"
	"net/http"
	"time"
)

// HealthResponse represents the payload returned by /healthz
type HealthResponse struct {
	Status        string  `json:"status"`
	UptimeSeconds float64 `json:"uptime_seconds"`
	Timestamp     string  `json:"timestamp"`
	GoVersion     string  `json:"go_version"`
	TLSProfile    string  `json:"tls_profile"`
}

// HealthHandler returns an HTTP handler for health probes (/healthz).
func HealthHandler(startTime time.Time, profileName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, `{"error":"Method Not Allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		uptime := time.Since(startTime).Seconds()
		resp := HealthResponse{
			Status:        "UP",
			UptimeSeconds: uptime,
			Timestamp:     time.Now().UTC().Format(time.RFC3339),
			GoVersion:     "go1.27.1",
			TLSProfile:    profileName,
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.WriteHeader(http.StatusOK)

		_ = json.NewEncoder(w).Encode(resp)
	}
}
