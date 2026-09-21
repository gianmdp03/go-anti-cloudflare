package proxy

import (
	"fmt"
	"strings"
	"sync"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

// ProfileMapping defines client profile details with corresponding User-Agent
type ProfileInfo struct {
	Profile   profiles.ClientProfile
	UserAgent string
}

var supportedProfiles = map[string]ProfileInfo{
	"firefox_120": {
		Profile:   profiles.Firefox_120,
		UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:120.0) Gecko/20100101 Firefox/120.0",
	},
	"firefox_123": {
		Profile:   profiles.Firefox_123,
		UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:123.0) Gecko/20100101 Firefox/123.0",
	},
	"firefox_133": {
		Profile:   profiles.Firefox_133,
		UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:133.0) Gecko/20100101 Firefox/133.0",
	},
	"chrome_120": {
		Profile:   profiles.Chrome_120,
		UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	},
	"chrome_131": {
		Profile:   profiles.Chrome_131,
		UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	},
}

// TLSEngine defines the contract for sending spoofed TLS HTTP requests.
type TLSEngine interface {
	Do(req *fhttp.Request) (*fhttp.Response, error)
	CloseIdleConnections()
	ProfileName() string
	UserAgent() string
}

// Engine implements TLSEngine using bogdanfinn/tls-client.
type Engine struct {
	client      tls_client.HttpClient
	profileName string
	userAgent   string
	mu          sync.RWMutex
}

// NewTLSEngine builds a high-performance, JA4-spoofed TLS engine.
func NewTLSEngine(profileName string, timeout time.Duration, proxyURL ...string) (*Engine, error) {
	norm := strings.ToLower(strings.TrimSpace(profileName))
	pInfo, exists := supportedProfiles[norm]
	if !exists {
		// Fallback to default Chrome 131
		pInfo = supportedProfiles["chrome_131"]
		norm = "chrome_131"
	}

	timeoutSec := int(timeout.Seconds())
	if timeoutSec <= 0 {
		timeoutSec = 15
	}

	options := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(timeoutSec),
		tls_client.WithClientProfile(pInfo.Profile),
		tls_client.WithNotFollowRedirects(),
		tls_client.WithCatchPanics(),
	}

	if len(proxyURL) > 0 && strings.TrimSpace(proxyURL[0]) != "" {
		options = append(options, tls_client.WithProxyUrl(strings.TrimSpace(proxyURL[0])))
	}

	client, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), options...)
	if err != nil {
		return nil, fmt.Errorf("tls-engine: failed to create tls-client: %w", err)
	}

	return &Engine{
		client:      client,
		profileName: norm,
		userAgent:   pInfo.UserAgent,
	}, nil
}

// Do executes an HTTP request through the spoofed TLS stack.
func (e *Engine) Do(req *fhttp.Request) (*fhttp.Response, error) {
	return e.client.Do(req)
}

// CloseIdleConnections closes all idle keep-alive connections.
func (e *Engine) CloseIdleConnections() {
	e.client.CloseIdleConnections()
}

// ProfileName returns the active profile name.
func (e *Engine) ProfileName() string {
	return e.profileName
}

// UserAgent returns the authentic User-Agent corresponding to the active TLS profile.
func (e *Engine) UserAgent() string {
	return e.userAgent
}

// ClientPool manages multiple TLS engines for rotation if needed.
type ClientPool struct {
	engines []*Engine
	index   uint64
	mu      sync.RWMutex
}

// NewClientPool initializes a pool of engines across distinct profiles for JA4 rotation.
func NewClientPool(profilesToUse []string, timeout time.Duration) (*ClientPool, error) {
	if len(profilesToUse) == 0 {
		profilesToUse = []string{"chrome_131"}
	}

	engines := make([]*Engine, 0, len(profilesToUse))
	for _, p := range profilesToUse {
		eng, err := NewTLSEngine(p, timeout)
		if err != nil {
			return nil, err
		}
		engines = append(engines, eng)
	}

	return &ClientPool{
		engines: engines,
	}, nil
}

// Get returns the next available TLSEngine round-robin.
func (p *ClientPool) Get() TLSEngine {
	p.mu.Lock()
	defer p.mu.Unlock()
	eng := p.engines[p.index%uint64(len(p.engines))]
	p.index++
	return eng
}

// CloseIdleConnections closes idle connections for all pooled engines.
func (p *ClientPool) CloseIdleConnections() {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, eng := range p.engines {
		eng.CloseIdleConnections()
	}
}
