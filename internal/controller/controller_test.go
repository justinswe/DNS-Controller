package controller

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"
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

func TestHandleReconciliation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		response string
		method   string
		path     string
	}{
		{
			name:     "create",
			response: `{"success":true,"result":[]}`,
			method:   http.MethodPost,
			path:     "/zones/zone-id/dns_records",
		},
		{
			name:     "update",
			response: `{"success":true,"result":[{"id":"record-id","content":"192.0.2.2"}]}`,
			method:   http.MethodPut,
			path:     "/zones/zone-id/dns_records/record-id",
		},
		{
			name:     "unchanged",
			response: `{"success":true,"result":[{"id":"record-id","content":"192.0.2.1"}]}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			steps := []httpExchange{
				{url: "https://api.ipify.org", response: " 192.0.2.1\n"},
				{url: "https://api.cloudflare.com/client/v4/zones?name=example.com", response: `{"success":true,"result":[{"id":"zone-id"}]}`},
				{url: "https://api.cloudflare.com/client/v4/zones/zone-id/dns_records?type=A&name=home.example.com", response: test.response},
			}
			if test.method != "" {
				steps = append(steps, httpExchange{
					method: test.method,
					url:    "https://api.cloudflare.com/client/v4" + test.path,
					wantJSON: map[string]any{
						"type": "A", "name": "home.example.com", "content": "192.0.2.1",
						"ttl": float64(300), "proxied": false,
					},
					response: `{"success":true}`,
				})
			}

			ctrl := newReplayController(t, steps)
			if err := ctrl.Handle(context.Background(), []string{"home.example.com"}); err != nil {
				t.Fatalf("Handle() returned an unexpected error: %v", err)
			}
		})
	}
}

func TestHandleMultipleRecords(t *testing.T) {
	t.Parallel()

	steps := []httpExchange{{url: "https://api.ipify.org", response: "192.0.2.1"}}
	for _, name := range []string{"home.example.com", "vpn.example.com"} {
		steps = append(steps,
			httpExchange{url: "https://api.cloudflare.com/client/v4/zones?name=example.com", response: `{"success":true,"result":[{"id":"zone-id"}]}`},
			httpExchange{url: "https://api.cloudflare.com/client/v4/zones/zone-id/dns_records?type=A&name=" + name, response: `{"success":true,"result":[{"id":"record-id","content":"192.0.2.1"}]}`},
		)
	}
	ctrl := newReplayController(t, steps)
	if err := ctrl.Handle(context.Background(), []string{"home.example.com", "vpn.example.com"}); err != nil {
		t.Fatalf("Handle() returned an unexpected error: %v", err)
	}
}

func TestHandleFailuresStopProcessing(t *testing.T) {
	t.Parallel()

	publicIP := httpExchange{url: "https://api.ipify.org", response: "192.0.2.1"}
	zone := httpExchange{url: "https://api.cloudflare.com/client/v4/zones?name=example.com", response: `{"success":true,"result":[{"id":"zone-id"}]}`}
	absent := httpExchange{url: "https://api.cloudflare.com/client/v4/zones/zone-id/dns_records?type=A&name=home.example.com", response: `{"success":true,"result":[]}`}
	changed := httpExchange{url: absent.url, response: `{"success":true,"result":[{"id":"record-id","content":"192.0.2.2"}]}`}
	transportErr := errors.New("transport failed")
	readErr := errors.New("response read failed")
	createURL := "https://api.cloudflare.com/client/v4/zones/zone-id/dns_records"
	updateURL := createURL + "/record-id"
	tests := []struct {
		name      string
		before    []httpExchange
		failure   httpExchange
		wantError string
		wantCause error
	}{
		{
			name:      "IP HTTP status",
			failure:   httpExchange{url: publicIP.url, status: http.StatusServiceUnavailable},
			wantError: "unexpected status 503",
		},
		{
			name:      "IP transport",
			failure:   httpExchange{url: publicIP.url, transportErr: transportErr},
			wantCause: transportErr,
		},
		{
			name:      "IP body read",
			failure:   httpExchange{url: publicIP.url, readErr: readErr},
			wantCause: readErr,
		},
		{
			name:      "zone HTTP status",
			before:    []httpExchange{publicIP},
			failure:   httpExchange{url: zone.url, status: http.StatusForbidden, response: "denied"},
			wantError: "zones list failed: status=403 body=denied",
		},
		{
			name:    "zone malformed JSON",
			before:  []httpExchange{publicIP},
			failure: httpExchange{url: zone.url, response: `{"success":`},
		},
		{
			name:      "missing zone",
			before:    []httpExchange{publicIP},
			failure:   httpExchange{url: zone.url, response: `{"success":true,"result":[]}`},
			wantError: "zone not found",
		},
		{
			name:      "record HTTP status",
			before:    []httpExchange{publicIP, zone},
			failure:   httpExchange{url: absent.url, status: http.StatusForbidden, response: "denied"},
			wantError: "dns_records list failed: status=403 body=denied",
		},
		{
			name:    "record malformed JSON",
			before:  []httpExchange{publicIP, zone},
			failure: httpExchange{url: absent.url, response: `{"success":`},
		},
		{
			name:      "create HTTP status",
			before:    []httpExchange{publicIP, zone, absent},
			failure:   httpExchange{method: http.MethodPost, url: createURL, status: http.StatusForbidden, response: "denied"},
			wantError: "create dns_record failed: status=403 body=denied",
		},
		{
			name:      "create transport",
			before:    []httpExchange{publicIP, zone, absent},
			failure:   httpExchange{method: http.MethodPost, url: createURL, transportErr: transportErr},
			wantCause: transportErr,
		},
		{
			name:      "update HTTP status",
			before:    []httpExchange{publicIP, zone, changed},
			failure:   httpExchange{method: http.MethodPut, url: updateURL, status: http.StatusForbidden, response: "denied"},
			wantError: "update dns_record failed: status=403 body=denied",
		},
		{
			name:      "update transport",
			before:    []httpExchange{publicIP, zone, changed},
			failure:   httpExchange{method: http.MethodPut, url: updateURL, transportErr: transportErr},
			wantCause: transportErr,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			steps := append(append([]httpExchange(nil), test.before...), test.failure)
			ctrl := newReplayController(t, steps)
			err := ctrl.Handle(context.Background(), []string{"home.example.com", "later.example.com"})
			if err == nil {
				t.Fatal("Handle() succeeded, want an error")
			}
			if test.wantError != "" && !strings.Contains(err.Error(), test.wantError) {
				t.Errorf("Handle() error = %v, want error containing %q", err, test.wantError)
			}
			if test.wantCause != nil && !errors.Is(err, test.wantCause) {
				t.Errorf("Handle() error = %v, want cause %v", err, test.wantCause)
			}
		})
	}
}

// httpExchange describes one expected request and its simulated response.
type httpExchange struct {
	method       string
	url          string
	wantJSON     map[string]any
	status       int
	response     string
	transportErr error
	readErr      error
}

type roundTripFunc func(*http.Request) (*http.Response, error)

// RoundTrip handles a request without accessing the network.
func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type trackedBody struct {
	io.Reader
	closed bool
}

// Close records that the response body was closed.
func (b *trackedBody) Close() error {
	b.closed = true
	return nil
}

// newReplayController checks an exact request sequence and response body cleanup.
func newReplayController(t *testing.T, steps []httpExchange) *Controller {
	t.Helper()

	ctrl, err := NewController(CloudflareConfig{APIToken: "token"})
	if err != nil {
		t.Fatalf("NewController() returned an unexpected error: %v", err)
	}
	next := 0
	ctrl.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if next >= len(steps) {
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
		}
		step := steps[next]
		next++
		checkRequest(t, req, step)
		if step.transportErr != nil {
			return nil, step.transportErr
		}
		body := &trackedBody{Reader: strings.NewReader(step.response)}
		if step.readErr != nil {
			body.Reader = iotest.ErrReader(step.readErr)
		}
		t.Cleanup(func() {
			if !body.closed {
				t.Errorf("response body for %s was not closed", req.URL)
			}
		})
		status := step.status
		if status == 0 {
			status = http.StatusOK
		}
		return &http.Response{StatusCode: status, Body: body, Header: make(http.Header)}, nil
	})
	t.Cleanup(func() {
		if next != len(steps) {
			t.Errorf("request count = %d, want %d", next, len(steps))
		}
	})
	return ctrl
}

// checkRequest verifies the method, URL, authentication, and optional JSON body.
func checkRequest(t *testing.T, req *http.Request, step httpExchange) {
	t.Helper()

	method := step.method
	if method == "" {
		method = http.MethodGet
	}
	if req.Method != method || req.URL.String() != step.url {
		t.Fatalf("request = %s %s, want %s %s", req.Method, req.URL, method, step.url)
	}
	wantAuth := ""
	if req.URL.Host == "api.cloudflare.com" {
		wantAuth = "Bearer token"
	}
	if got := req.Header.Get("Authorization"); got != wantAuth {
		t.Errorf("Authorization = %q, want %q", got, wantAuth)
	}
	if step.wantJSON == nil {
		return
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	var got map[string]any
	if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
		t.Fatalf("decode request JSON: %v", err)
	}
	if !reflect.DeepEqual(got, step.wantJSON) {
		t.Errorf("request JSON = %v, want %v", got, step.wantJSON)
	}
}
