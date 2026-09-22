package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultPort           = 8079
	defaultUpstreamURL    = "https://appsl.mardelplata.gob.ar/app_cuando_llega/webWS.php"
	defaultTimeoutSeconds = 45
	defaultAllowedOrigin  = "http://localhost:8400"
	defaultTLSProfile     = "Chrome_131"
	defaultLogLevel       = "info"
	defaultAuthServiceURL = "http://localhost:8191/v1"
)

// Config represents the immutable application configuration validated at startup (Fail-Fast).
type Config struct {
	port          int
	upstreamURL   *url.URL
	timeout       time.Duration
	allowedOrigin string
	tlsProfile    string
	logLevel       string
	proxyURL       string
	defaultCookie  string
	authServiceURL string
}

// Port returns the TCP port the proxy server listens on.
func (c *Config) Port() int {
	return c.port
}

// ListenAddr returns the host:port string suitable for http.Server.
func (c *Config) ListenAddr() string {
	return fmt.Sprintf(":%d", c.port)
}

// UpstreamURL returns the parsed upstream URL.
func (c *Config) UpstreamURL() *url.URL {
	// Return a copy so caller cannot mutate the internal pointer
	if c.upstreamURL == nil {
		return nil
	}
	u := *c.upstreamURL
	return &u
}

// UpstreamURLString returns the string representation of upstream URL.
func (c *Config) UpstreamURLString() string {
	if c.upstreamURL == nil {
		return ""
	}
	return c.upstreamURL.String()
}

// Timeout returns the HTTP client request timeout.
func (c *Config) Timeout() time.Duration {
	return c.timeout
}

// AllowedOrigin returns the CORS allowed origin header value (e.g. http://localhost:8400).
func (c *Config) AllowedOrigin() string {
	return c.allowedOrigin
}

// TLSProfile returns the configured client TLS spoof profile name.
func (c *Config) TLSProfile() string {
	return c.tlsProfile
}

// LogLevel returns the configured logging level ("debug", "info", "warn", "error").
func (c *Config) LogLevel() string {
	return c.logLevel
}

// ProxyURL returns the optional upstream proxy URL (HTTP/HTTPS/SOCKS5).
func (c *Config) ProxyURL() string {
	return c.proxyURL
}

// DefaultCookie returns the optional default Cookie header to inject (e.g. cf_clearance).
func (c *Config) DefaultCookie() string {
	return c.defaultCookie
}

// AuthServiceURL returns the local auth provider URL (e.g. http://localhost:8191/v1).
func (c *Config) AuthServiceURL() string {
	return c.authServiceURL
}

// Load loads and validates configuration from environment variables.
// Follows Fail-Fast principles: if any configuration parameter is invalid, an error is returned immediately.
func Load() (*Config, error) {
	return LoadFromLookup(os.LookupEnv)
}

// LookupFunc defines a function signature for reading environment variables.
type LookupFunc func(key string) (string, bool)

// LoadFromLookup loads and validates configuration using a custom lookup function (facilitates unit testing).
func LoadFromLookup(lookup LookupFunc) (*Config, error) {
	cfg := &Config{}

	// 1. PORT
	portStr, ok := lookup("PORT")
	if !ok || strings.TrimSpace(portStr) == "" {
		cfg.port = defaultPort
	} else {
		parsedPort, err := strconv.Atoi(strings.TrimSpace(portStr))
		if err != nil {
			return nil, fmt.Errorf("config: invalid PORT '%s': must be a valid integer: %w", portStr, err)
		}
		if parsedPort < 1 || parsedPort > 65535 {
			return nil, fmt.Errorf("config: invalid PORT %d: must be between 1 and 65535", parsedPort)
		}
		cfg.port = parsedPort
	}

	// 2. UPSTREAM_URL
	rawURL, ok := lookup("UPSTREAM_URL")
	if !ok || strings.TrimSpace(rawURL) == "" {
		rawURL = defaultUpstreamURL
	}
	parsedURL, err := url.ParseRequestURI(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("config: invalid UPSTREAM_URL '%s': %w", rawURL, err)
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return nil, fmt.Errorf("config: UPSTREAM_URL '%s' must have 'http' or 'https' scheme", rawURL)
	}
	if parsedURL.Host == "" {
		return nil, fmt.Errorf("config: UPSTREAM_URL '%s' missing host", rawURL)
	}
	cfg.upstreamURL = parsedURL

	// 3. TIMEOUT_SECONDS
	timeoutStr, ok := lookup("TIMEOUT_SECONDS")
	if !ok || strings.TrimSpace(timeoutStr) == "" {
		cfg.timeout = time.Duration(defaultTimeoutSeconds) * time.Second
	} else {
		sec, err := strconv.Atoi(strings.TrimSpace(timeoutStr))
		if err != nil {
			return nil, fmt.Errorf("config: invalid TIMEOUT_SECONDS '%s': must be an integer: %w", timeoutStr, err)
		}
		if sec <= 0 {
			return nil, fmt.Errorf("config: invalid TIMEOUT_SECONDS %d: must be greater than 0", sec)
		}
		cfg.timeout = time.Duration(sec) * time.Second
	}

	// 4. ALLOWED_ORIGIN
	origin, ok := lookup("ALLOWED_ORIGIN")
	if !ok || strings.TrimSpace(origin) == "" {
		cfg.allowedOrigin = defaultAllowedOrigin
	} else {
		trimmed := strings.TrimSpace(origin)
		if trimmed == "" {
			return nil, errors.New("config: ALLOWED_ORIGIN cannot be blank")
		}
		cfg.allowedOrigin = trimmed
	}

	// 5. TLS_PROFILE
	tlsProf, ok := lookup("TLS_PROFILE")
	if !ok || strings.TrimSpace(tlsProf) == "" {
		cfg.tlsProfile = defaultTLSProfile
	} else {
		cfg.tlsProfile = strings.TrimSpace(tlsProf)
	}

	// 6. LOG_LEVEL
	logLevel, ok := lookup("LOG_LEVEL")
	if !ok || strings.TrimSpace(logLevel) == "" {
		cfg.logLevel = defaultLogLevel
	} else {
		norm := strings.ToLower(strings.TrimSpace(logLevel))
		switch norm {
		case "debug", "info", "warn", "error":
			cfg.logLevel = norm
		default:
			return nil, fmt.Errorf("config: invalid LOG_LEVEL '%s': must be debug, info, warn, or error", logLevel)
		}
	}

	// 7. PROXY_URL (Optional residential proxy)
	if pURL, ok := lookup("PROXY_URL"); ok && strings.TrimSpace(pURL) != "" {
		cfg.proxyURL = strings.TrimSpace(pURL)
	}

	// 8. DEFAULT_COOKIE or CF_CLEARANCE (Optional cookie injection)
	if cookie, ok := lookup("DEFAULT_COOKIE"); ok && strings.TrimSpace(cookie) != "" {
		cfg.defaultCookie = strings.TrimSpace(cookie)
	} else if clearance, ok := lookup("CF_CLEARANCE"); ok && strings.TrimSpace(clearance) != "" {
		cfg.defaultCookie = "cf_clearance=" + strings.TrimSpace(clearance)
	}

	// 9. AUTH_SERVICE_URL (Local session auth provider)
	authURL, ok := lookup("AUTH_SERVICE_URL")
	if !ok || strings.TrimSpace(authURL) == "" {
		cfg.authServiceURL = defaultAuthServiceURL
	} else {
		cfg.authServiceURL = strings.TrimSpace(authURL)
	}

	return cfg, nil
}
