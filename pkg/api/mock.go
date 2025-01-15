package api

import (
	"context"
	"net/http"
	"time"
)

type MockClient struct {
	*Client
}

func NewMockClient() *MockClient {
	mock := &Client{
		HttpClient:   http.Client{},
		baseURL:      SolanaEpochStatsAPI,
		cacheTimeout: CacheTimeout,
	}
	return &MockClient{
		Client: mock,
	}
}

func (m *MockClient) SetMinRequiredVersion(version string) {
	m.cache.version = version
	m.cache.lastCheck = time.Now()
}

func (m *MockClient) GetMinRequiredVersion(ctx context.Context, cluster string) (string, error) {
	return m.cache.version, nil
}
