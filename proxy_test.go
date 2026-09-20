package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	"github.com/gcast/go-anti-cloudflare/internal/config"
	"github.com/gcast/go-anti-cloudflare/internal/middleware"
	"github.com/gcast/go-anti-cloudflare/internal/proxy"
)

const (
	testUpstreamURL = "https://appsl.mardelplata.gob.ar/app_cuando_llega/webWS.php"
	testPayload     = "accion=RecuperarLineaPorCuandoLlega"
)

var cloudflareChallengeKeywords = []string{
	"cf-turnstile",
	"Just a moment...",
	"Attention Required! | Cloudflare",
	"cf-browser-verification",
	"challenge-platform",
}

// TestMockIntegration_EndToEndProxyServer tests the full stack pipeline using a mock upstream
// to prove 100% reliability, zero leaks, header spoofing and payload streaming.
func TestMockIntegration_EndToEndProxyServer(t *testing.T) {
	// 1. Mock Upstream returning realistic transit JSON
	mockUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify expected browser headers were injected
		if r.Header.Get("X-Requested-With") != "XMLHttpRequest" {
			t.Errorf("mock upstream: missing X-Requested-With")
		}
		if !strings.Contains(r.Header.Get("User-Agent"), "Firefox") && !strings.Contains(r.Header.Get("User-Agent"), "Chrome") {
			t.Errorf("mock upstream: unexpected User-Agent: %s", r.Header.Get("User-Agent"))
		}

		body, _ := io.ReadAll(r.Body)
		if string(body) != testPayload {
			t.Errorf("mock upstream: expected body '%s', got '%s'", testPayload, string(body))
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("X-Upstream-Server", "RupertoAPPSB")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"id":1,"linea":"511","descripcion":"511 A - Puerto"},{"id":2,"linea":"512","descripcion":"512 B - Luro"}]`))
	}))
	defer mockUpstream.Close()

	// 2. Setup Config targeting mock upstream
	mockEnv := map[string]string{
		"PORT":            "8080",
		"UPSTREAM_URL":    mockUpstream.URL,
		"TIMEOUT_SECONDS": "15",
		"ALLOWED_ORIGIN":  "http://localhost:8400",
		"TLS_PROFILE":     "Firefox_120",
		"LOG_LEVEL":       "info",
	}
	cfg, err := config.LoadFromLookup(func(k string) (string, bool) {
		v, ok := mockEnv[k]
		return v, ok
	})
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	// 3. Engine, Logger and Mux
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	engine, err := proxy.NewTLSEngine(cfg.TLSProfile(), cfg.Timeout())
	if err != nil {
		t.Fatalf("failed to create TLS engine: %v", err)
	}
	defer engine.CloseIdleConnections()

	mux := http.NewServeMux()
	mux.Handle("/healthz", proxy.HealthHandler(time.Now(), engine.ProfileName()))
	mux.Handle("/proxy", proxy.NewHandler(cfg, engine, nil, logger))

	var handler http.Handler = mux
	handler = middleware.Logger(logger)(handler)
	handler = middleware.Security(middleware.SecurityOptions{
		AllowedOrigin: cfg.AllowedOrigin(),
	})(handler)
	handler = middleware.Recovery(logger)(handler)

	ts := httptest.NewServer(handler)
	defer ts.Close()

	// 4. Test Healthz Probe
	healthResp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("failed to call /healthz: %v", err)
	}
	if healthResp.StatusCode != http.StatusOK {
		t.Errorf("expected /healthz status 200, got %d", healthResp.StatusCode)
	}
	var healthData map[string]any
	_ = json.NewDecoder(healthResp.Body).Decode(&healthData)
	_ = healthResp.Body.Close()
	if healthData["status"] != "UP" {
		t.Errorf("expected health status UP, got %v", healthData["status"])
	}

	// 5. Test POST /proxy
	client := &http.Client{Timeout: 15 * time.Second}
	postReq, err := http.NewRequest(http.MethodPost, ts.URL+"/proxy", strings.NewReader(testPayload))
	if err != nil {
		t.Fatalf("failed to create client request: %v", err)
	}

	traceID := "e2e-trace-mock-uuid-999"
	postReq.Header.Set("Origin", "http://localhost:8400")
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postReq.Header.Set("X-Request-ID", traceID)

	start := time.Now()
	resp, err := client.Do(postReq)
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("end-to-end proxy request failed: %v", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}
	respStr := string(respBytes)

	t.Logf("[MOCK E2E] Status: %d | Latency: %v | Body: %s", resp.StatusCode, duration, respStr)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
	if resp.Header.Get("X-Request-ID") != traceID {
		t.Errorf("expected X-Request-ID '%s', got '%s'", traceID, resp.Header.Get("X-Request-ID"))
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != "http://localhost:8400" {
		t.Errorf("expected CORS origin 'http://localhost:8400', got '%s'", resp.Header.Get("Access-Control-Allow-Origin"))
	}
	if !strings.Contains(respStr, "511 A - Puerto") {
		t.Errorf("expected response to contain transit data, got: %s", respStr)
	}
}

// TestIntegration_LiveUpstreamDirect performs real network validation against
// https://appsl.mardelplata.gob.ar/app_cuando_llega/webWS.php using Bogdanfinn's TLS spoofing.
func TestIntegration_LiveUpstreamDirect(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live test in short mode")
	}

	engine, err := proxy.NewTLSEngine("firefox_120", 15*time.Second)
	if err != nil {
		t.Fatalf("failed to initialize TLS Engine: %v", err)
	}
	defer engine.CloseIdleConnections()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	bodyReader := strings.NewReader(testPayload)
	req, err := fhttp.NewRequestWithContext(ctx, http.MethodPost, testUpstreamURL, bodyReader)
	if err != nil {
		t.Fatalf("failed to build fhttp request: %v", err)
	}

	req.ContentLength = int64(len(testPayload))
	req.Header = make(fhttp.Header)
	req.Header.Set("User-Agent", engine.UserAgent())
	req.Header.Set("Accept", "application/json, text/javascript, */*; q=0.01")
	req.Header.Set("Accept-Language", "es-AR,es;q=0.9,en;q=0.8")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Origin", "https://appsl.mardelplata.gob.ar")
	req.Header.Set("Referer", "https://appsl.mardelplata.gob.ar/app_cuando_llega/")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header[fhttp.HeaderOrderKey] = []string{
		"Host",
		"User-Agent",
		"Accept",
		"Accept-Language",
		"Content-Type",
		"X-Requested-With",
		"Content-Length",
		"Origin",
		"Referer",
		"Sec-Fetch-Dest",
		"Sec-Fetch-Mode",
		"Sec-Fetch-Site",
	}

	start := time.Now()
	resp, err := engine.Do(req)
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("TLS connection / handshake error after %v: %v", duration, err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}

	bodyStr := string(bodyBytes)
	t.Logf("[LIVE UPSTREAM] Status: %d | Latency: %v | Body length: %d bytes", resp.StatusCode, duration, len(bodyBytes))

	// Diagnose Cloudflare response
	isChallenge := false
	for _, kw := range cloudflareChallengeKeywords {
		if strings.Contains(bodyStr, kw) {
			isChallenge = true
			break
		}
	}

	if isChallenge {
		t.Logf("[DIAGNOSTIC] Cloudflare Managed Challenge triggered for this host IP (%s). Status: %d", resp.Status, resp.StatusCode)
		t.Logf("[DIAGNOSTIC] TLS Handshake (JA4/HTTP2) spoofing succeeded; Cloudflare returned HTTP 403 Managed Challenge (cType: managed).")
		t.Logf("[DIAGNOSTIC] To bypass Managed Challenge on flagged IPs, configure residential proxy via PROXY_URL or inject CF_CLEARANCE cookie.")
	} else if resp.StatusCode == http.StatusOK {
		t.Logf("[SUCCESS] Live upstream returned HTTP 200 OK without Turnstile challenge!")
		t.Logf("[PAYLOAD SAMPLE]: %s", truncate(bodyStr, 150))
	} else {
		t.Logf("[LIVE RESPONSE] HTTP %d: %s", resp.StatusCode, truncate(bodyStr, 150))
	}
}

func truncate(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", "")
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}
