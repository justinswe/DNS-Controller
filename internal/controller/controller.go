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

type Record struct {
	ID         string `mapstructure:"id"`
	RecordType string `mapstructure:"recordType"`
	Name       string `mapstructure:"name"`
}

type Config struct {
	Records []Record `mapstructure:"records"`
}

type Controller struct {
	httpClient *http.Client
	apiKey     string
	apiEmail   string
	baseURL    string
	authType   string
}

const (
	perPage float64 = 100.0
)

func NewController(cloudflareKey, email, authType string) (*Controller, error) {
	if strings.TrimSpace(cloudflareKey) == "" {
		return nil, errors.New("cloudflare key or token is required")
	}
	at := strings.ToLower(strings.TrimSpace(authType))
	if at == "" {
		if strings.TrimSpace(email) != "" {
			at = "key"
		} else {
			at = "token"
		}
	}
	if at == "key" && strings.TrimSpace(email) == "" {
		return nil, errors.New("cloudflare email is required when using auth type 'key'")
	}
	return &Controller{
		httpClient: &http.Client{Timeout: 15 * time.Second},
		apiKey:     cloudflareKey,
		apiEmail:   email,
		baseURL:    "https://api.cloudflare.com/client/v4",
		authType:   at,
	}, nil
}

// Handle checks if DNS records match the current public IP and updates them if needed
func (c *Controller) Handle(ctx context.Context, config Config) error {
	// Get current public IP
	currentIP, err := c.getPublicIP()
	if err != nil {
		return err
	}
	zap.L().Info("Current IP retrieved", zap.String("ip", currentIP))

	for _, cfg := range config.Records {
		if cfg.Name == "" {
			return errors.New("record name is required")
		}
		if strings.ToUpper(cfg.RecordType) != "A" {
			zap.L().Info("Skipping non-A record type", zap.String("name", cfg.Name), zap.String("type", cfg.RecordType))
			continue
		}
		zap.L().Info("Processing record", zap.String("id", cfg.ID), zap.String("type", cfg.RecordType), zap.String("name", cfg.Name))
		if err := c.ensureARecord(ctx, cfg, currentIP); err != nil {
			return err
		}
	}

	return nil // Return nil when processing completes successfully
}

// getPublicIP retrieves the current public IP address of the machine
func (c *Controller) getPublicIP() (string, error) {
	// Using a service that returns the public IP
	resp, err := http.Get("https://api.ipify.org")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	ip, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
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

func (c *Controller) ensureARecord(ctx context.Context, rec Record, ip string) error {
	zoneID := strings.TrimSpace(rec.ID)
	if zoneID == "" {
		apex := apexFromFQDN(rec.Name)
		zid, err := c.getZoneIDByName(ctx, apex)
		if err != nil {
			return err
		}
		zoneID = zid
	}

	existing, err := c.findARecord(ctx, zoneID, rec.Name)
	if err != nil {
		return err
	}

	if existing == nil {
		if err := c.createARecord(ctx, zoneID, rec.Name, ip); err != nil {
			return err
		}
		zap.L().Info("Created A record", zap.String("name", rec.Name), zap.String("ip", ip), zap.String("zone", zoneID))
		return nil
	}

	if existing.Content == ip {
		zap.L().Info("A record already up to date; no update needed", zap.String("name", rec.Name), zap.String("ip", ip))
		return nil
	}

	if err := c.updateARecord(ctx, zoneID, existing.ID, rec.Name, ip); err != nil {
		return err
	}
	zap.L().Info("Updated A record", zap.String("name", rec.Name), zap.String("old_ip", existing.Content), zap.String("new_ip", ip), zap.String("zone", zoneID))
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
	if c.authType == "token" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
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
