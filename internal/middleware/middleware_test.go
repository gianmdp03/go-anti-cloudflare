package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLoggerMiddleware_PreservesAndGeneratesRequestID(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))

	handler := Logger(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := GetRequestID(r.Context())
		if reqID == "" {
			t.Error("expected non-empty request ID in context")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))

	// Case 1: No incoming X-Request-ID -> generated
	req1 := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr1 := httptest.NewRecorder()
	handler.ServeHTTP(rr1, req1)

	respID1 := rr1.Header().Get(RequestIDHeader)
	if respID1 == "" {
		t.Error("expected X-Request-ID in response header")
	}

	// Case 2: Incoming X-Request-ID -> preserved
	req2 := httptest.NewRequest(http.MethodGet, "/test", nil)
	req2.Header.Set(RequestIDHeader, "custom-trace-id-1234")
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)

	respID2 := rr2.Header().Get(RequestIDHeader)
	if respID2 != "custom-trace-id-1234" {
		t.Errorf("expected 'custom-trace-id-1234', got '%s'", respID2)
	}

	// Check log output format
	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "custom-trace-id-1234") {
		t.Errorf("expected logs to contain custom-trace-id-1234, got:\n%s", logOutput)
	}
	if !strings.Contains(logOutput, "latency_ms") {
		t.Errorf("expected logs to contain latency_ms, got:\n%s", logOutput)
	}
}

func TestRecoveryMiddleware_RecoversPanic(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))

	panicHandler := Recovery(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("unexpected critical nil pointer dereference")
	}))

	req := httptest.NewRequest(http.MethodPost, "/critical", nil)
	ctx := context.WithValue(req.Context(), requestIDKey, "test-panic-req-id")
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()

	// Must not panic
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Recovery middleware failed to recover panic: %v", r)
		}
	}()

	panicHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", rr.Code)
	}

	var errPayload map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &errPayload); err != nil {
		t.Fatalf("expected valid JSON error response, got error: %v", err)
	}

	if errPayload["error"] != "Internal Server Error" {
		t.Errorf("expected error 'Internal Server Error', got '%s'", errPayload["error"])
	}
	if errPayload["request_id"] != "test-panic-req-id" {
		t.Errorf("expected request_id 'test-panic-req-id', got '%s'", errPayload["request_id"])
	}

	if !strings.Contains(logBuf.String(), "unexpected critical nil pointer dereference") {
		t.Errorf("expected log to contain panic description, got:\n%s", logBuf.String())
	}
}

func TestSecurityMiddleware_CORSAndPreflight(t *testing.T) {
	opts := SecurityOptions{
		AllowedOrigin: "http://localhost:8400",
	}
	secMiddleware := Security(opts)
	nextCalled := false
	handler := secMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))

	// Case 1: Preflight OPTIONS request
	reqOptions := httptest.NewRequest(http.MethodOptions, "/proxy", nil)
	reqOptions.Header.Set("Origin", "http://localhost:8400")
	rrOptions := httptest.NewRecorder()
	handler.ServeHTTP(rrOptions, reqOptions)

	if rrOptions.Code != http.StatusNoContent {
		t.Errorf("expected status 204 for OPTIONS preflight, got %d", rrOptions.Code)
	}
	if rrOptions.Header().Get("Access-Control-Allow-Origin") != "http://localhost:8400" {
		t.Errorf("expected CORS origin 'http://localhost:8400', got '%s'", rrOptions.Header().Get("Access-Control-Allow-Origin"))
	}
	if nextCalled {
		t.Error("preflight OPTIONS should not call next handler")
	}

	// Case 2: Allowed origin POST
	nextCalled = false
	reqAllowed := httptest.NewRequest(http.MethodPost, "/proxy", strings.NewReader("accion=test"))
	reqAllowed.Header.Set("Origin", "http://localhost:8400")
	rrAllowed := httptest.NewRecorder()
	handler.ServeHTTP(rrAllowed, reqAllowed)

	if rrAllowed.Code != http.StatusOK {
		t.Errorf("expected status 200 for allowed origin, got %d", rrAllowed.Code)
	}
	if !nextCalled {
		t.Error("expected next handler to be called for allowed origin")
	}

	// Case 3: Disallowed origin
	nextCalled = false
	reqForbidden := httptest.NewRequest(http.MethodPost, "/proxy", strings.NewReader("accion=test"))
	reqForbidden.Header.Set("Origin", "http://malicious-attacker.com")
	rrForbidden := httptest.NewRecorder()
	handler.ServeHTTP(rrForbidden, reqForbidden)

	if rrForbidden.Code != http.StatusForbidden {
		t.Errorf("expected status 403 Forbidden for disallowed origin, got %d", rrForbidden.Code)
	}
	if nextCalled {
		t.Error("disallowed origin should not call next handler")
	}

	// Case 4: Direct server-to-server call (no Origin header)
	nextCalled = false
	reqServer := httptest.NewRequest(http.MethodPost, "/proxy", strings.NewReader("accion=test"))
	rrServer := httptest.NewRecorder()
	handler.ServeHTTP(rrServer, reqServer)

	if rrServer.Code != http.StatusOK {
		t.Errorf("expected status 200 for direct server call, got %d", rrServer.Code)
	}
	if !nextCalled {
		t.Error("expected next handler to be called for direct server call")
	}
}

