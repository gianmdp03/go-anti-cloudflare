package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	"github.com/gcast/go-anti-cloudflare/internal/config"
	"github.com/gcast/go-anti-cloudflare/internal/session"
)

// MockTLSEngine provides a controllable mock for TLSEngine.
type MockTLSEngine struct {
	DoFunc                 func(req *fhttp.Request) (*fhttp.Response, error)
	CloseIdleConnectionsFn func()
	ProfileNameVal         string
	UserAgentVal           string
	LastRequest            *fhttp.Request
	LastBody               []byte
}

func (m *MockTLSEngine) Do(req *fhttp.Request) (*fhttp.Response, error) {
	m.LastRequest = req
	if req.Body != nil {
		bodyBytes, _ := io.ReadAll(req.Body)
		m.LastBody = bodyBytes
		// Restore body if needed
		req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	}
	if m.DoFunc != nil {
		return m.DoFunc(req)
	}
	return &fhttp.Response{
		StatusCode: http.StatusOK,
		Header:     make(fhttp.Header),
		Body:       io.NopCloser(strings.NewReader(`{"status":"success"}`)),
	}, nil
}

func (m *MockTLSEngine) CloseIdleConnections() {
	if m.CloseIdleConnectionsFn != nil {
		m.CloseIdleConnectionsFn()
	}
}

func (m *MockTLSEngine) ProfileName() string {
	if m.ProfileNameVal != "" {
		return m.ProfileNameVal
	}
	return "chrome_131"
}

func (m *MockTLSEngine) UserAgent() string {
	if m.UserAgentVal != "" {
		return m.UserAgentVal
	}
	return "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
}

func TestHealthHandler(t *testing.T) {
	start := time.Now().Add(-10 * time.Second)
	handler := HealthHandler(start, "chrome_131")

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var resp HealthResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json response: %v", err)
	}

	if resp.Status != "UP" {
		t.Errorf("expected status 'UP', got '%s'", resp.Status)
	}
	if resp.TLSProfile != "chrome_131" {
		t.Errorf("expected profile 'chrome_131', got '%s'", resp.TLSProfile)
	}
	if resp.UptimeSeconds < 9.0 {
		t.Errorf("expected uptime >= 9.0s, got %f", resp.UptimeSeconds)
	}
}

func TestHandler_ForwardingAndHeaderSpoofing(t *testing.T) {
	mockEnv := map[string]string{
		"PORT":         "8080",
		"UPSTREAM_URL": "https://appsl.mardelplata.gob.ar/app_cuando_llega/webWS.php",
	}
	cfg, err := config.LoadFromLookup(func(k string) (string, bool) {
		v, ok := mockEnv[k]
		return v, ok
	})
	if err != nil {
		t.Fatalf("failed to create test config: %v", err)
	}

	mockEngine := &MockTLSEngine{
		DoFunc: func(req *fhttp.Request) (*fhttp.Response, error) {
			respHeader := make(fhttp.Header)
			respHeader.Set("Content-Type", "application/json; charset=utf-8")
			respHeader.Set("Content-Encoding", "br")
			respHeader.Set("Content-Length", "1234")
			respHeader.Set("X-Custom-Upstream", "response-header-value")

			return &fhttp.Response{
				StatusCode: http.StatusOK,
				Header:     respHeader,
				Body:       io.NopCloser(strings.NewReader(`{"lineas":[{"id":1,"nombre":"511"}]}`)),
			}, nil
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewHandler(cfg, mockEngine, nil, logger)

	payload := "accion=RecuperarLineaPorCuandoLlega"
	req := httptest.NewRequest(http.MethodPost, "/proxy", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	// Verify status code
	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	// Verify Content-Encoding and Content-Length are stripped
	if rr.Header().Get("Content-Encoding") != "" {
		t.Errorf("expected Content-Encoding to be stripped, got '%s'", rr.Header().Get("Content-Encoding"))
	}
	if rr.Header().Get("Content-Length") != "" {
		t.Errorf("expected Content-Length to be stripped, got '%s'", rr.Header().Get("Content-Length"))
	}
	if rr.Header().Get("X-Custom-Upstream") != "response-header-value" {
		t.Errorf("expected X-Custom-Upstream to be preserved, got '%s'", rr.Header().Get("X-Custom-Upstream"))
	}

	// Verify response body streamed
	expectedBody := `{"lineas":[{"id":1,"nombre":"511"}]}`
	if rr.Body.String() != expectedBody {
		t.Errorf("expected body '%s', got '%s'", expectedBody, rr.Body.String())
	}

	// Verify header spoofing in intercepted request
	if mockEngine.LastRequest == nil {
		t.Fatal("expected mock engine to receive request")
	}

	fReq := mockEngine.LastRequest
	if fReq.Header.Get("User-Agent") != mockEngine.UserAgent() {
		t.Errorf("expected UA '%s', got '%s'", mockEngine.UserAgent(), fReq.Header.Get("User-Agent"))
	}
	if fReq.Header.Get("X-Requested-With") != "XMLHttpRequest" {
		t.Errorf("expected X-Requested-With 'XMLHttpRequest', got '%s'", fReq.Header.Get("X-Requested-With"))
	}
	if fReq.Header.Get("Origin") != "https://appsl.mardelplata.gob.ar" {
		t.Errorf("expected Origin 'https://appsl.mardelplata.gob.ar', got '%s'", fReq.Header.Get("Origin"))
	}
	if fReq.Header.Get("Referer") != "https://appsl.mardelplata.gob.ar/app_cuando_llega/" {
		t.Errorf("expected Referer 'https://appsl.mardelplata.gob.ar/app_cuando_llega/', got '%s'", fReq.Header.Get("Referer"))
	}

	// Verify payload forwarded
	if string(mockEngine.LastBody) != payload {
		t.Errorf("expected payload '%s', got '%s'", payload, string(mockEngine.LastBody))
	}
}

func TestHandler_UpstreamErrorPropagation(t *testing.T) {
	cfg, _ := config.LoadFromLookup(func(string) (string, bool) { return "", false })

	mockEngine := &MockTLSEngine{
		DoFunc: func(req *fhttp.Request) (*fhttp.Response, error) {
			return nil, errors.New("tls dial handshake failure")
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewHandler(cfg, mockEngine, nil, logger)

	req := httptest.NewRequest(http.MethodPost, "/proxy", strings.NewReader("accion=test"))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Errorf("expected status 502 Bad Gateway on TLS failure, got %d", rr.Code)
	}
}

func TestTLSEngine_Initialization(t *testing.T) {
	eng, err := NewTLSEngine("chrome_131", 5*time.Second)
	if err != nil {
		t.Fatalf("failed to create TLSEngine: %v", err)
	}
	defer eng.CloseIdleConnections()

	if eng.ProfileName() != "chrome_131" {
		t.Errorf("expected profile 'chrome_131', got '%s'", eng.ProfileName())
	}
	if !strings.Contains(eng.UserAgent(), "Chrome/131.0.0.0") {
		t.Errorf("expected Chrome 131 User-Agent, got '%s'", eng.UserAgent())
	}
}

func TestHandler_SessionRetryOn403(t *testing.T) {
	// Mock Auth Service Provider
	mockAuthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"status": "ok",
			"solution": {
				"userAgent": "Aligned-Custom-Browser/1.0",
				"cookies": [
					{"name": "cf_clearance", "value": "refreshed-token-999"}
				]
			}
		}`))
	}))
	defer mockAuthServer.Close()

	sessionMgr := session.NewManager(mockAuthServer.URL)
	cfg, _ := config.LoadFromLookup(func(string) (string, bool) { return "", false })

	callCount := 0
	var receivedUA string
	var receivedCookie string
	var lastBodyOnRetry []byte

	mockEngine := &MockTLSEngine{
		DoFunc: func(req *fhttp.Request) (*fhttp.Response, error) {
			callCount++
			receivedUA = req.Header.Get("User-Agent")
			receivedCookie = req.Header.Get("Cookie")

			if callCount == 1 {
				// First call simulates 403 Forbidden Cloudflare block
				return &fhttp.Response{
					StatusCode: http.StatusForbidden,
					Header:     make(fhttp.Header),
					Body:       io.NopCloser(strings.NewReader(`Just a moment...`)),
				}, nil
			}

			// Second call: read body to ensure it was restored and not drained
			if req.Body != nil {
				b, _ := io.ReadAll(req.Body)
				lastBodyOnRetry = b
			}

			// Second call (after refresh) returns 200 OK
			return &fhttp.Response{
				StatusCode: http.StatusOK,
				Header:     make(fhttp.Header),
				Body:       io.NopCloser(strings.NewReader(`{"status":"success_after_refresh"}`)),
			}, nil
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewHandler(cfg, mockEngine, sessionMgr, logger)

	req := httptest.NewRequest(http.MethodPost, "/proxy", strings.NewReader("accion=test"))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200 after retry, got %d. Body: %s", rr.Code, rr.Body.String())
	}

	if callCount != 2 {
		t.Errorf("expected exactly 2 upstream calls (initial + 1 retry), got %d", callCount)
	}

	if receivedUA != "Aligned-Custom-Browser/1.0" {
		t.Errorf("expected aligned User-Agent 'Aligned-Custom-Browser/1.0', got '%s'", receivedUA)
	}

	if receivedCookie != "cf_clearance=refreshed-token-999" {
		t.Errorf("expected aligned Cookie 'cf_clearance=refreshed-token-999', got '%s'", receivedCookie)
	}

	if string(lastBodyOnRetry) != "accion=test" {
		t.Errorf("expected retried body to be 'accion=test', got '%s'", string(lastBodyOnRetry))
	}

	if !strings.Contains(rr.Body.String(), "success_after_refresh") {
		t.Errorf("expected body to contain 'success_after_refresh', got: %s", rr.Body.String())
	}
}

