package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	mockEnv := map[string]string{}
	lookup := func(k string) (string, bool) {
		v, ok := mockEnv[k]
		return v, ok
	}

	cfg, err := LoadFromLookup(lookup)
	if err != nil {
		t.Fatalf("expected no error for defaults, got: %v", err)
	}

	if cfg.Port() != 8080 {
		t.Errorf("expected default port 8080, got %d", cfg.Port())
	}
	if cfg.ListenAddr() != ":8080" {
		t.Errorf("expected ListenAddr ':8080', got '%s'", cfg.ListenAddr())
	}
	if cfg.UpstreamURLString() != "https://appsl.mardelplata.gob.ar/app_cuando_llega/webWS.php" {
		t.Errorf("expected default upstream URL, got '%s'", cfg.UpstreamURLString())
	}
	if cfg.Timeout() != 45*time.Second {
		t.Errorf("expected default timeout 45s, got %v", cfg.Timeout())
	}
	if cfg.AllowedOrigin() != "http://localhost:8400" {
		t.Errorf("expected default origin 'http://localhost:8400', got '%s'", cfg.AllowedOrigin())
	}
	if cfg.TLSProfile() != "Chrome_131" {
		t.Errorf("expected default profile 'Chrome_131', got '%s'", cfg.TLSProfile())
	}
	if cfg.LogLevel() != "info" {
		t.Errorf("expected default log level 'info', got '%s'", cfg.LogLevel())
	}
	if cfg.AuthServiceURL() != "http://localhost:8191/v1" {
		t.Errorf("expected default auth service URL 'http://localhost:8191/v1', got '%s'", cfg.AuthServiceURL())
	}
}

func TestLoadCustomValid(t *testing.T) {
	mockEnv := map[string]string{
		"PORT":            "9090",
		"UPSTREAM_URL":    "https://custom.endpoint.example.com/api",
		"TIMEOUT_SECONDS": "30",
		"ALLOWED_ORIGIN":  "http://localhost:8400",
		"TLS_PROFILE":     "Chrome_120",
		"LOG_LEVEL":       "DEBUG",
	}
	lookup := func(k string) (string, bool) {
		v, ok := mockEnv[k]
		return v, ok
	}

	cfg, err := LoadFromLookup(lookup)
	if err != nil {
		t.Fatalf("expected no error for custom valid env, got: %v", err)
	}

	if cfg.Port() != 9090 {
		t.Errorf("expected port 9090, got %d", cfg.Port())
	}
	if cfg.ListenAddr() != ":9090" {
		t.Errorf("expected ':9090', got '%s'", cfg.ListenAddr())
	}
	if cfg.UpstreamURLString() != "https://custom.endpoint.example.com/api" {
		t.Errorf("expected custom URL, got '%s'", cfg.UpstreamURLString())
	}
	if cfg.Timeout() != 30*time.Second {
		t.Errorf("expected timeout 30s, got %v", cfg.Timeout())
	}
	if cfg.AllowedOrigin() != "http://localhost:8400" {
		t.Errorf("expected 'http://localhost:8400', got '%s'", cfg.AllowedOrigin())
	}
	if cfg.TLSProfile() != "Chrome_120" {
		t.Errorf("expected 'Chrome_120', got '%s'", cfg.TLSProfile())
	}
	if cfg.LogLevel() != "debug" {
		t.Errorf("expected normalized log level 'debug', got '%s'", cfg.LogLevel())
	}
}

func TestFailFastValidation(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr bool
	}{
		{
			name:    "invalid port non-numeric",
			env:     map[string]string{"PORT": "abc"},
			wantErr: true,
		},
		{
			name:    "invalid port zero",
			env:     map[string]string{"PORT": "0"},
			wantErr: true,
		},
		{
			name:    "invalid port negative",
			env:     map[string]string{"PORT": "-80"},
			wantErr: true,
		},
		{
			name:    "invalid port too large",
			env:     map[string]string{"PORT": "70000"},
			wantErr: true,
		},
		{
			name:    "invalid upstream url not an absolute url",
			env:     map[string]string{"UPSTREAM_URL": "::invalid"},
			wantErr: true,
		},
		{
			name:    "invalid upstream url missing scheme",
			env:     map[string]string{"UPSTREAM_URL": "example.com/api"},
			wantErr: true,
		},
		{
			name:    "invalid upstream url ftp scheme",
			env:     map[string]string{"UPSTREAM_URL": "ftp://example.com/api"},
			wantErr: true,
		},
		{
			name:    "invalid timeout zero",
			env:     map[string]string{"TIMEOUT_SECONDS": "0"},
			wantErr: true,
		},
		{
			name:    "invalid timeout negative",
			env:     map[string]string{"TIMEOUT_SECONDS": "-5"},
			wantErr: true,
		},
		{
			name:    "invalid timeout non-numeric",
			env:     map[string]string{"TIMEOUT_SECONDS": "ten"},
			wantErr: true,
		},
		{
			name:    "invalid log level",
			env:     map[string]string{"LOG_LEVEL": "super_verbose"},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lookup := func(k string) (string, bool) {
				v, ok := tc.env[k]
				return v, ok
			}
			_, err := LoadFromLookup(lookup)
			if (err != nil) != tc.wantErr {
				t.Fatalf("expected error: %v, got: %v", tc.wantErr, err)
			}
		})
	}
}
