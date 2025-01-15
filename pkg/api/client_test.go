package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestClient_GetMinRequiredVersion(t *testing.T) {
	tests := []struct {
		name       string
		cluster    string
		mockJSON   string
		wantErr    bool
		wantErrMsg string
		want       string
	}{
		{
			name:    "valid mainnet response",
			cluster: "mainnet-beta",
			mockJSON: `{
				"stats": {
					"config": {
						"min_version": "2.0.20"
					}
				}
			}`,
			want: "2.0.20",
		},
		{
			name:    "valid testnet response",
			cluster: "testnet",
			mockJSON: `{
				"stats": {
					"config": {
						"min_version": "2.1.6"
					}
				}
			}`,
			want: "2.1.6",
		},
		{
			name:       "invalid json response",
			cluster:    "mainnet-beta",
			mockJSON:   `{"invalid": "json"`,
			wantErr:    true,
			wantErrMsg: "failed to decode response",
		},
		{
			name:       "missing version in response",
			cluster:    "mainnet-beta",
			mockJSON:   `{"stats": {"config": {}}}`,
			wantErr:    true,
			wantErrMsg: "min_version not found in response",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test server
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Verify request
				assert.Equal(t, "/api/validators/epoch-stats", r.URL.Path)
				assert.Equal(t, tt.cluster, r.URL.Query().Get("cluster"))
				assert.Equal(t, "latest", r.URL.Query().Get("epoch"))

				// Send response
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(tt.mockJSON))
			}))
			defer server.Close()

			// Create client with test server URL
			client := &Client{
				HttpClient:   http.Client{},
				baseURL:      server.URL + "/api/validators/epoch-stats",
				cacheTimeout: time.Hour,
			}

			// Test GetMinRequiredVersion
			got, err := client.GetMinRequiredVersion(context.Background(), tt.cluster)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErrMsg)
				return
			}

			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)

			// Test caching
			cachedVersion, err := client.GetMinRequiredVersion(context.Background(), tt.cluster)
			assert.NoError(t, err)
			assert.Equal(t, tt.want, cachedVersion)
		})
	}
}
