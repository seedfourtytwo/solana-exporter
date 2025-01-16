package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

const (
	// CacheTimeout defines how often to refresh the minimum required version (6 hours)
	CacheTimeout = 6 * time.Hour

	// SolanaEpochStatsAPI is the base URL for the Solana validators epoch stats API
	SolanaEpochStatsAPI = "https://api.solana.org/api/validators/epoch-stats"
)

type Client struct {
	HttpClient http.Client
	baseURL    string
	cache      struct {
		version   string
		lastCheck time.Time
	}
	mu sync.RWMutex
	// How often to refresh the cache
	cacheTimeout time.Duration
}

func NewClient() *Client {
	return &Client{
		HttpClient:   http.Client{},
		cacheTimeout: CacheTimeout,
		baseURL:      SolanaEpochStatsAPI,
	}
}

func (c *Client) GetMinRequiredVersion(ctx context.Context, cluster string) (string, error) {
	// Check cache first
	c.mu.RLock()
	if !c.cache.lastCheck.IsZero() && time.Since(c.cache.lastCheck) < c.cacheTimeout {
		version := c.cache.version
		c.mu.RUnlock()
		return version, nil
	}
	c.mu.RUnlock()

	// Make API request
	url := fmt.Sprintf("%s?cluster=%s&epoch=latest", c.baseURL, cluster)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.HttpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch min required version: %w", err)
	}
	defer resp.Body.Close()

	var stats ValidatorEpochStats
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	// Validate the response
	if stats.Stats.Config.MinVersion == "" {
		return "", fmt.Errorf("min_version not found in response")
	}

	// Update cache
	c.mu.Lock()
	c.cache.version = stats.Stats.Config.MinVersion
	c.cache.lastCheck = time.Now()
	c.mu.Unlock()

	return stats.Stats.Config.MinVersion, nil
}
