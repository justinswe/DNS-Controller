package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.uber.org/zap"
)

// CloudflareConfig contains credentials used to authenticate with Cloudflare.
// Exactly one of APIToken or APIKey must be set. Email is required with APIKey.
type CloudflareConfig struct {
	APIToken string
	APIKey   string
	Email    string
}

type Controller struct {
	httpClient *http.Client
	apiToken   string
	apiKey     string
	apiEmail   string
	baseURL    string
}

const (
	perPage float64 = 100.0
)

// NewController constructs a Cloudflare DNS controller.
func NewController(config CloudflareConfig) (*Controller, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	apiToken := strings.TrimSpace(config.APIToken)
	apiKey := strings.TrimSpace(config.APIKey)
	email := strings.TrimSpace(config.Email)

	return &Controller{
		httpClient: &http.Client{Timeout: 15 * time.Second},
		apiToken:   apiToken,
		apiKey:     apiKey,
		apiEmail:   email,
		baseURL:    "https://api.cloudflare.com/client/v4",
	}, nil
}

// Validate checks whether the Cloudflare credentials select one supported
// authentication method.
func (config CloudflareConfig) Validate() error {
	apiToken := strings.TrimSpace(config.APIToken)
	apiKey := strings.TrimSpace(config.APIKey)
	email := strings.TrimSpace(config.Email)
	hasToken := apiToken != ""
	hasKey := apiKey != ""
	if hasToken == hasKey {
		return errors.New("exactly one of cloudflare API token or API key is required")
	}
	if hasKey && email == "" {
		return errors.New("cloudflare email is required with an API key")
	}
	if hasToken && email != "" {
		return errors.New("cloudflare email cannot be used with an API token")
	}
	return nil
}

// Handle ensures the configured A records contain the current public IP address.
func (c *Controller) Handle(ctx context.Context, records []string) error {
	currentIP, err := c.getPublicIP(ctx)
	if err != nil {
		return err
	}
	zap.L().Info("Current IP retrieved", zap.String("ip", currentIP))

	for _, record := range records {
		name := strings.TrimSpace(record)
		if name == "" {
			return errors.New("record name is required")
		}
		zap.L().Info("Processing A record", zap.String("name", name))
		if err := c.ensureARecord(ctx, name, currentIP); err != nil {
			return err
		}
	}

	return nil
}

func (c *Controller) getPublicIP(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.ipify.org", nil)
	if err != nil {
		return "", fmt.Errorf("create public IP request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("retrieve public IP: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("retrieve public IP: unexpected status %d", resp.StatusCode)
	}

	ip, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read public IP response: %w", err)
	}

	return strings.TrimSpace(string(ip)), nil
}

// Cloudflare REST types
type cfAPIResponse[T any] struct {
	Success    bool          `json:"success"`
	Errors     []cfError     `json:"errors"`
	Messages   []string      `json:"messages"`
	Result     T             `json:"result"`
	ResultInfo *cfResultInfo `json:"result_info,omitempty"`
}

type cfError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type cfResultInfo struct {
	Page       int `json:"page"`
	PerPage    int `json:"per_page"`
	TotalPages int `json:"total_pages"`
	Count      int `json:"count"`
	TotalCount int `json:"total_count"`
}

type cfZone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type cfDNSRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Proxied *bool  `json:"proxied,omitempty"`
}

type cfCreateDNSRecordRequest struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Proxied bool   `json:"proxied"`
}

type cfUpdateDNSRecordRequest struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Proxied bool   `json:"proxied"`
}

func (c *Controller) ensureARecord(ctx context.Context, name, ip string) error {
	apex := apexFromFQDN(name)
	zoneID, err := c.getZoneIDByName(ctx, apex)
	if err != nil {
		return err
	}

	existing, err := c.findARecord(ctx, zoneID, name)
	if err != nil {
		return err
	}

	if existing == nil {
		if err := c.createARecord(ctx, zoneID, name, ip); err != nil {
			return err
		}
		zap.L().Info("Created A record", zap.String("name", name), zap.String("ip", ip), zap.String("zone", zoneID))
		return nil
	}

	if existing.Content == ip {
		zap.L().Info("A record already up to date; no update needed", zap.String("name", name), zap.String("ip", ip))
		return nil
	}

	if err := c.updateARecord(ctx, zoneID, existing.ID, name, ip); err != nil {
		return err
	}
	zap.L().Info("Updated A record", zap.String("name", name), zap.String("old_ip", existing.Content), zap.String("new_ip", ip), zap.String("zone", zoneID))
	return nil
}

func (c *Controller) getZoneIDByName(ctx context.Context, zoneName string) (string, error) {
	endpoint := fmt.Sprintf("%s/zones?name=%s", c.baseURL, url.QueryEscape(zoneName))
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	c.addAuthHeaders(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("zones list failed: status=%d body=%s", resp.StatusCode, string(body))
	}
	var parsed cfAPIResponse[[]cfZone]
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", err
	}
	if !parsed.Success || len(parsed.Result) == 0 {
		return "", fmt.Errorf("zone not found for name %s", zoneName)
	}
	return parsed.Result[0].ID, nil
}

func (c *Controller) findARecord(ctx context.Context, zoneID, fqdn string) (*cfDNSRecord, error) {
	endpoint := fmt.Sprintf("%s/zones/%s/dns_records?type=A&name=%s", c.baseURL, zoneID, url.QueryEscape(fqdn))
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	c.addAuthHeaders(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("dns_records list failed: status=%d body=%s", resp.StatusCode, string(body))
	}
	var parsed cfAPIResponse[[]cfDNSRecord]
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	if !parsed.Success || len(parsed.Result) == 0 {
		return nil, nil
	}
	rec := parsed.Result[0]
	return &rec, nil
}

func (c *Controller) createARecord(ctx context.Context, zoneID, fqdn, ip string) error {
	endpoint := fmt.Sprintf("%s/zones/%s/dns_records", c.baseURL, zoneID)
	bodyBytes, _ := json.Marshal(cfCreateDNSRecordRequest{
		Type:    "A",
		Name:    fqdn,
		Content: ip,
		TTL:     300,
		Proxied: false,
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(bodyBytes)))
	c.addAuthHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("create dns_record failed: status=%d body=%s", resp.StatusCode, string(body))
	}
	return nil
}

func (c *Controller) updateARecord(ctx context.Context, zoneID, recordID, fqdn, ip string) error {
	endpoint := fmt.Sprintf("%s/zones/%s/dns_records/%s", c.baseURL, zoneID, recordID)
	bodyBytes, _ := json.Marshal(cfUpdateDNSRecordRequest{
		Type:    "A",
		Name:    fqdn,
		Content: ip,
		TTL:     300,
		Proxied: false,
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, strings.NewReader(string(bodyBytes)))
	c.addAuthHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("update dns_record failed: status=%d body=%s", resp.StatusCode, string(body))
	}
	return nil
}

func (c *Controller) addAuthHeaders(req *http.Request) {
	if c.apiToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiToken)
	} else {
		req.Header.Set("X-Auth-Email", c.apiEmail)
		req.Header.Set("X-Auth-Key", c.apiKey)
	}
	req.Header.Set("User-Agent", "dns-controller/1.0")
}

func apexFromFQDN(fqdn string) string {
	parts := strings.Split(fqdn, ".")
	if len(parts) >= 2 {
		return parts[len(parts)-2] + "." + parts[len(parts)-1]
	}
	return fqdn
}
