package proxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	"github.com/gcast/go-anti-cloudflare/internal/config"
	"github.com/gcast/go-anti-cloudflare/internal/session"
)

const (
	maxRequestBodyBytes = 10 * 1024 * 1024 // 10 MB limit to prevent OOM DOS
	defaultReferer      = "https://appsl.mardelplata.gob.ar/app_cuando_llega/"
	defaultOrigin       = "https://appsl.mardelplata.gob.ar"
)

// Global sync.Pool for bytes.Buffer to eliminate heap allocations and GC pauses
var bufferPool = sync.Pool{
	New: func() any {
		return bytes.NewBuffer(make([]byte, 0, 4096))
	},
}

// Hop-by-hop headers that must not be forwarded by proxies according to RFC 2616 / RFC 7230.
var hopByHopHeaders = map[string]struct{}{
	"connection":          {},
	"content-encoding":    {},
	"content-length":      {},
	"keep-alive":          {},
	"proxy-authenticate":  {},
	"proxy-authorization": {},
	"te":                  {},
	"trailer":             {},
	"transfer-encoding":   {},
	"upgrade":             {},
}

// Handler forwards incoming requests from Spring Boot backend to the municipal upstream
// using Bogdanfinn's TLS-spoofed client.
type Handler struct {
	cfg        *config.Config
	tlsEngine  TLSEngine
	sessionMgr *session.Manager
	logger     *slog.Logger
}

// NewHandler constructs a production-ready proxy handler.
func NewHandler(cfg *config.Config, engine TLSEngine, sessionMgr *session.Manager, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		cfg:        cfg,
		tlsEngine:  engine,
		sessionMgr: sessionMgr,
		logger:     logger,
	}
}

// ServeHTTP handles /proxy requests by streaming the body, spoofing TLS and browser headers,
// executing the upstream call and streaming back the response.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 1. Enforce allowed HTTP methods (typically POST or GET for transit query)
	if r.Method != http.MethodPost && r.Method != http.MethodGet && r.Method != http.MethodOptions {
		http.Error(w, `{"error":"Method Not Allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// 2. Read request body efficiently using sync.Pool buffer and io.LimitReader
	buf := bufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer bufferPool.Put(buf)

	if r.Body != nil {
		defer r.Body.Close()
		limitedReader := io.LimitReader(r.Body, maxRequestBodyBytes)
		n, err := buf.ReadFrom(limitedReader)
		if err != nil {
			h.logger.Error("failed to read client request body", "error", err)
			http.Error(w, `{"error":"Failed to read request body"}`, http.StatusBadRequest)
			return
		}
		if n >= maxRequestBodyBytes {
			h.logger.Warn("request body exceeded maximum allowed limit", "limit_bytes", maxRequestBodyBytes)
			http.Error(w, `{"error":"Request body too large"}`, http.StatusRequestEntityTooLarge)
			return
		}
	}

	// 3. Build upstream URL (preserving incoming query parameters if present)
	upstreamURL := h.cfg.UpstreamURL()
	if upstreamURL == nil {
		http.Error(w, `{"error":"Upstream URL unconfigured"}`, http.StatusInternalServerError)
		return
	}

	targetURL := upstreamURL.String()
	if r.URL.RawQuery != "" {
		if strings.Contains(targetURL, "?") {
			targetURL += "&" + r.URL.RawQuery
		} else {
			targetURL += "?" + r.URL.RawQuery
		}
	}

	// 4. Construct spoofed fhttp.Request
	ctx, cancel := context.WithTimeout(r.Context(), h.cfg.Timeout())
	defer cancel()

	bodyBytes := buf.Bytes()
	var bodyReader io.Reader
	if len(bodyBytes) > 0 {
		bodyReader = bytes.NewReader(bodyBytes)
	}

	fReq, err := fhttp.NewRequestWithContext(ctx, r.Method, targetURL, bodyReader)
	if err != nil {
		h.logger.Error("failed to create upstream fhttp request", "error", err)
		http.Error(w, `{"error":"Failed to build upstream request"}`, http.StatusInternalServerError)
		return
	}

	// Set exact ContentLength if payload exists
	if len(bodyBytes) > 0 {
		fReq.ContentLength = int64(len(bodyBytes))
	}

	// 5. Inject realistic navigation headers & preserve Content-Type
	h.injectSpoofedHeaders(fReq, r)

	// 6. Execute upstream request through spoofed TLS engine
	startTime := time.Now()
	resp, err := h.tlsEngine.Do(fReq)
	duration := time.Since(startTime)

	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			h.logger.Error("upstream request timed out", "duration_ms", duration.Milliseconds(), "error", err)
			http.Error(w, `{"error":"Upstream Gateway Timeout"}`, http.StatusGatewayTimeout)
			return
		}
		h.logger.Error("upstream TLS request failed", "duration_ms", duration.Milliseconds(), "error", err)
		http.Error(w, `{"error":"Bad Gateway - TLS handshake or network failure"}`, http.StatusBadGateway)
		return
	}

	// Dynamic session synchronization fallback on HTTP 403 Forbidden
	if resp != nil && resp.StatusCode == http.StatusForbidden && h.sessionMgr != nil {
		h.logger.Warn("MGP rechazó la consulta (HTTP 403); renovando sesión y reintentando una vez",
			"accion", requestAction(bodyBytes))
		newCreds, refreshErr := h.sessionMgr.Refresh(r.Context())
		if refreshErr != nil {
			h.logger.Error("no se pudo renovar la sesión", "error", refreshErr)
		} else {
			h.logger.Info("sesión renovada; reintentando consulta a MGP", "accion", requestAction(bodyBytes))
			_ = resp.Body.Close()

			fReq.Header.Set("Cookie", newCreds.Cookie)
			fReq.Header.Set("User-Agent", newCreds.UserAgent)
			fReq.Body = io.NopCloser(bytes.NewReader(bodyBytes))

			// Re-execute once
			resp, err = h.tlsEngine.Do(fReq)
			if err != nil {
				h.logger.Error("falló el reintento luego de renovar la sesión", "error", err)
				http.Error(w, `{"error":"Bad Gateway - retry after session refresh failed"}`, http.StatusBadGateway)
				return
			}
		}
	}

	defer func() {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
	}()

	h.logUpstreamOutcome(bodyBytes, resp, time.Since(startTime))

	// 7. Copy safe response headers from upstream to client
	for key, values := range resp.Header {
		lowerKey := strings.ToLower(key)
		if _, isHop := hopByHopHeaders[lowerKey]; isHop {
			continue
		}
		for _, v := range values {
			w.Header().Add(key, v)
		}
	}

	// Ensure Content-Encoding and Content-Length are never forwarded to client
	w.Header().Del("Content-Encoding")
	w.Header().Del("Content-Length")

	// Forward exact status code
	w.WriteHeader(resp.StatusCode)

	// 8. Stream response body directly back to client (Zero-buffering for low latency)
	if _, err := io.Copy(w, resp.Body); err != nil {
		h.logger.Error("error streaming upstream response body to client", "error", err)
	}
}

func (h *Handler) logUpstreamOutcome(body []byte, resp *fhttp.Response, duration time.Duration) {
	action := requestAction(body)
	attributes := []any{
		"accion", action,
		"status", resp.StatusCode,
		"duracion_ms", duration.Milliseconds(),
	}

	switch {
	case resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices:
		h.logger.Info("MGP respondió correctamente", attributes...)
	case resp.StatusCode == http.StatusTooManyRequests:
		h.logger.Warn("MGP aplicó un límite de solicitudes (HTTP 429); esperá antes de reintentar", attributes...)
	case resp.StatusCode == http.StatusForbidden && isCloudflareResponse(resp):
		h.logger.Warn("Cloudflare rechazó la consulta; la IP o sesión requiere validación", attributes...)
	case resp.StatusCode == http.StatusForbidden:
		h.logger.Warn("MGP rechazó la consulta con HTTP 403", attributes...)
	case resp.StatusCode >= http.StatusInternalServerError:
		h.logger.Error("MGP devolvió un error de servidor", attributes...)
	default:
		h.logger.Warn("MGP devolvió una respuesta no esperada", attributes...)
	}
}

func requestAction(body []byte) string {
	values, err := url.ParseQuery(string(body))
	if err != nil || values.Get("accion") == "" {
		return "(sin accion)"
	}
	return values.Get("accion")
}

func isCloudflareResponse(resp *fhttp.Response) bool {
	server := strings.ToLower(resp.Header.Get("Server"))
	return strings.Contains(server, "cloudflare") || resp.Header.Get("Cf-Mitigated") != ""
}

// injectSpoofedHeaders configures HTTP/2 pseudo-headers and headers wire-order matching
// the active browser fingerprint.
func (h *Handler) injectSpoofedHeaders(fReq *fhttp.Request, origReq *http.Request) {
	// Base real-browser headers
	fReq.Header = make(fhttp.Header)

	// 1. User-Agent from active TLS Engine profile
	userAgent := h.tlsEngine.UserAgent()

	// Read dynamic session credentials if available
	var sessionCookie string
	if h.sessionMgr != nil {
		creds := h.sessionMgr.GetCredentials()
		if creds.Cookie != "" {
			sessionCookie = creds.Cookie
		}
		if creds.UserAgent != "" {
			userAgent = creds.UserAgent
		}
	}

	fReq.Header.Set("User-Agent", userAgent)

	// 2. Accept
	accept := origReq.Header.Get("Accept")
	if accept == "" || accept == "*/*" {
		accept = "application/json, text/javascript, */*; q=0.01"
	}
	fReq.Header.Set("Accept", accept)

	// 3. Accept-Language
	acceptLang := origReq.Header.Get("Accept-Language")
	if acceptLang == "" {
		acceptLang = "es-AR,es;q=0.9,en;q=0.8"
	}
	fReq.Header.Set("Accept-Language", acceptLang)

	// 4. Content-Type (preserve or default to application/x-www-form-urlencoded)
	cType := origReq.Header.Get("Content-Type")
	if cType == "" {
		cType = "application/x-www-form-urlencoded; charset=UTF-8"
	}
	fReq.Header.Set("Content-Type", cType)

	// 5. Anti-bot browser indicators
	fReq.Header.Set("X-Requested-With", "XMLHttpRequest")
	fReq.Header.Set("Origin", defaultOrigin)
	fReq.Header.Set("Referer", defaultReferer)
	fReq.Header.Set("Sec-Fetch-Dest", "empty")
	fReq.Header.Set("Sec-Fetch-Mode", "cors")
	fReq.Header.Set("Sec-Fetch-Site", "same-origin")

	// 6. Cookie handling: prioritize session credentials, then incoming request cookie, then default cookie
	cookieVal := sessionCookie
	if cookieVal == "" {
		cookieVal = origReq.Header.Get("Cookie")
	}
	if cookieVal == "" && h.cfg != nil {
		cookieVal = h.cfg.DefaultCookie()
	}
	if cookieVal != "" {
		fReq.Header.Set("Cookie", cookieVal)
	}

	// 7. Header wire order definition for HTTP/2 JA4 fingerprint fidelity
	headerOrder := []string{
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
	if cookieVal != "" {
		headerOrder = append(headerOrder, "Cookie")
	}
	fReq.Header[fhttp.HeaderOrderKey] = headerOrder
}
