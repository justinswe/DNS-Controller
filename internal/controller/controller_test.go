package controller

import (
	"net/http"
	"testing"
)

func TestNewControllerValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		config  CloudflareConfig
		wantErr bool
	}{
		{name: "API token", config: CloudflareConfig{APIToken: "token"}},
		{name: "API key", config: CloudflareConfig{APIKey: "key", Email: "owner@example.com"}},
		{name: "empty credentials", config: CloudflareConfig{}, wantErr: true},
		{name: "both credential types", config: CloudflareConfig{APIToken: "token", APIKey: "key"}, wantErr: true},
		{name: "API key without email", config: CloudflareConfig{APIKey: "key"}, wantErr: true},
		{name: "API token with email", config: CloudflareConfig{APIToken: "token", Email: "owner@example.com"}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := NewController(test.config)
			if test.wantErr && err == nil {
				t.Fatal("NewController() accepted invalid credentials")
			}
			if !test.wantErr && err != nil {
				t.Fatalf("NewController() returned an unexpected error: %v", err)
			}
		})
	}
}

func TestAddAuthHeaders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		config     CloudflareConfig
		wantHeader http.Header
	}{
		{
			name:       "API token",
			config:     CloudflareConfig{APIToken: "token"},
			wantHeader: http.Header{"Authorization": []string{"Bearer token"}},
		},
		{
			name:   "API key",
			config: CloudflareConfig{APIKey: "key", Email: "owner@example.com"},
			wantHeader: http.Header{
				"X-Auth-Email": []string{"owner@example.com"},
				"X-Auth-Key":   []string{"key"},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			ctrl, err := NewController(test.config)
			if err != nil {
				t.Fatalf("NewController() returned an unexpected error: %v", err)
			}
			req, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
			if err != nil {
				t.Fatalf("create request: %v", err)
			}
			ctrl.addAuthHeaders(req)
			for name, values := range test.wantHeader {
				if got, want := req.Header.Values(name), values; len(got) != 1 || got[0] != want[0] {
					t.Errorf("header %s = %v, want %v", name, got, want)
				}
			}
		})
	}
}

func TestApexFromFQDN(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"home.example.com": "example.com",
		"example.com":      "example.com",
		"localhost":        "localhost",
	}
	for fqdn, want := range tests {
		if got := apexFromFQDN(fqdn); got != want {
			t.Errorf("apexFromFQDN(%q) = %q, want %q", fqdn, got, want)
		}
	}
}
