package services

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/net/http2"

	"agent_gatway/app/models"
)

// RegistryClient queries the agent_registry service
type RegistryClient struct {
	baseURL string
	http    *http.Client
}

func NewRegistryClient(baseURL string) *RegistryClient {
	// agent_registry runs HypGo's protocol: http2 with a self-signed dev cert
	// (see agent_registry/scripts/gencert); skip verification like a2a_client does.
	t1 := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} //nolint:gosec
	_ = http2.ConfigureTransport(t1)

	return &RegistryClient{
		baseURL: baseURL,
		http:    &http.Client{Timeout: 5 * time.Second, Transport: t1},
	}
}

// SearchBySkill finds agents matching the given skill keyword.
// Returns the best match (first result, ordered by last_seen_at DESC).
func (c *RegistryClient) SearchBySkill(ctx context.Context, skill string) (*models.RegistryAgent, error) {
	u := c.baseURL + "/registry/search"
	if skill != "" {
		u += "?skill=" + url.QueryEscape(skill)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("registry search: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry returned %d: %s", resp.StatusCode, body)
	}

	var result models.RegistrySearchResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode registry response: %w", err)
	}

	if result.Data.Total == 0 {
		return nil, nil
	}
	agent := result.Data.Agents[0]
	return &agent, nil
}

// GetAgent fetches a specific agent by ID
func (c *RegistryClient) GetAgent(ctx context.Context, id string) (*models.RegistryAgent, error) {
	u := c.baseURL + "/registry/agents/" + url.PathEscape(id)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("registry get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry returned %d: %s", resp.StatusCode, body)
	}

	var result models.RegistryAgentResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode registry response: %w", err)
	}
	return &result.Data, nil
}
