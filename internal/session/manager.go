package session

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

const TargetServiceURL = "https://appsl.mardelplata.gob.ar/app_cuando_llega/"

type ProviderRequest struct {
	Cmd        string `json:"cmd"`
	URL        string `json:"url"`
	MaxTimeout int    `json:"maxTimeout"`
}

type ProviderResponse struct {
	Status   string `json:"status"`
	Solution struct {
		UserAgent string `json:"userAgent"`
		Cookies   []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"cookies"`
	} `json:"solution"`
}

type Credentials struct {
	Cookie    string
	UserAgent string
}

type Manager struct {
	authServiceURL string
	creds          Credentials
	mu             sync.RWMutex
	refreshMu      sync.Mutex
	client         *http.Client
}

func NewManager(authServiceURL string) *Manager {
	if authServiceURL == "" {
		authServiceURL = "http://localhost:8191/v1"
	}
	return &Manager{
		authServiceURL: authServiceURL,
		client:         &http.Client{Timeout: 65 * time.Second},
	}
}

func (m *Manager) GetCredentials() Credentials {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.creds
}

func (m *Manager) Refresh(ctx context.Context) (Credentials, error) {
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()

	reqBody, err := json.Marshal(ProviderRequest{
		Cmd:        "request.get",
		URL:        TargetServiceURL,
		MaxTimeout: 60000,
	})
	if err != nil {
		return Credentials{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.authServiceURL, bytes.NewReader(reqBody))
	if err != nil {
		return Credentials{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.client.Do(req)
	if err != nil {
		return Credentials{}, fmt.Errorf("auth provider call failed: %w", err)
	}
	defer resp.Body.Close()

	var pResp ProviderResponse
	if err := json.NewDecoder(resp.Body).Decode(&pResp); err != nil {
		return Credentials{}, fmt.Errorf("failed to decode provider response: %w", err)
	}

	if pResp.Status != "ok" {
		return Credentials{}, fmt.Errorf("provider returned status: %s", pResp.Status)
	}

	for _, c := range pResp.Solution.Cookies {
		if c.Name == "cf_clearance" {
			m.mu.Lock()
			m.creds = Credentials{
				Cookie:    "cf_clearance=" + c.Value,
				UserAgent: pResp.Solution.UserAgent,
			}
			m.mu.Unlock()
			return m.creds, nil
		}
	}

	return Credentials{}, fmt.Errorf("clearance token not found in provider solution")
}
