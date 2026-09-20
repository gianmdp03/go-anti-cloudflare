package session

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestManager_GetCredentials_Initial(t *testing.T) {
	mgr := NewManager("http://localhost:8191/v1")
	creds := mgr.GetCredentials()
	if creds.Cookie != "" || creds.UserAgent != "" {
		t.Errorf("expected empty credentials initially, got: %+v", creds)
	}
}

func TestManager_Refresh_Success(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected application/json, got %s", r.Header.Get("Content-Type"))
		}

		var pReq ProviderRequest
		if err := json.NewDecoder(r.Body).Decode(&pReq); err != nil {
			t.Errorf("failed to decode request body: %v", err)
		}
		if pReq.Cmd != "request.get" {
			t.Errorf("expected Cmd 'request.get', got '%s'", pReq.Cmd)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"status": "ok",
			"solution": {
				"userAgent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
				"cookies": [
					{"name": "some_cookie", "value": "123"},
					{"name": "cf_clearance", "value": "test-clearance-token-456"}
				]
			}
		}`))
	}))
	defer mockServer.Close()

	mgr := NewManager(mockServer.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	creds, err := mgr.Refresh(ctx)
	if err != nil {
		t.Fatalf("unexpected error on Refresh: %v", err)
	}

	expectedCookie := "cf_clearance=test-clearance-token-456"
	expectedUA := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

	if creds.Cookie != expectedCookie {
		t.Errorf("expected Cookie '%s', got '%s'", expectedCookie, creds.Cookie)
	}
	if creds.UserAgent != expectedUA {
		t.Errorf("expected UserAgent '%s', got '%s'", expectedUA, creds.UserAgent)
	}

	// Verify cached
	cached := mgr.GetCredentials()
	if cached != creds {
		t.Errorf("expected cached credentials to match returned credentials")
	}
}

func TestManager_Refresh_StatusNotOk(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"status": "error",
			"message": "Challenge resolution failed"
		}`))
	}))
	defer mockServer.Close()

	mgr := NewManager(mockServer.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := mgr.Refresh(ctx)
	if err == nil {
		t.Fatal("expected error when provider returns status != ok, got nil")
	}
}

func TestManager_Refresh_MissingClearanceToken(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"status": "ok",
			"solution": {
				"userAgent": "TestUA",
				"cookies": []
			}
		}`))
	}))
	defer mockServer.Close()

	mgr := NewManager(mockServer.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := mgr.Refresh(ctx)
	if err == nil {
		t.Fatal("expected error when cf_clearance is missing, got nil")
	}
}
