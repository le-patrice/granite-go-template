package acme

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/time/rate"

	"example.com/app/internal/platform/outbound"
)

const maxResponseBytes = 1 << 20

// APIError is returned for a non-2xx response from Acme.
type APIError struct {
	Status int
}

func (e *APIError) Error() string {
	return fmt.Sprintf("acme: unexpected status %d", e.Status)
}

// ClientConfig configures NewClient.
type ClientConfig struct {
	BaseURL string
	APIKey  string
	// Timeout bounds each attempt (default 10s).
	Timeout time.Duration
	// MaxRetries is the number of retries for idempotent calls (default 0).
	MaxRetries int
	// RatePerSecond, when positive, limits outgoing requests.
	RatePerSecond float64
	// Transport defaults to http.DefaultTransport.
	Transport http.RoundTripper
}

// Client calls the Acme API through the shared outbound kit.
type Client struct {
	base   *url.URL
	apiKey string
	http   *http.Client
}

// NewClient validates cfg and builds a Client.
func NewClient(cfg ClientConfig) (*Client, error) {
	base, err := url.Parse(cfg.BaseURL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, errors.New("acme: base URL must be an http or https URL")
	}
	if cfg.APIKey == "" {
		return nil, errors.New("acme: API key is required")
	}

	outCfg := outbound.Config{
		Timeout:    cfg.Timeout,
		MaxRetries: cfg.MaxRetries,
		Transport:  cfg.Transport,
	}
	if cfg.RatePerSecond > 0 {
		outCfg.Limiter = rate.NewLimiter(rate.Limit(cfg.RatePerSecond), max(1, int(cfg.RatePerSecond)))
	}
	httpClient, err := outbound.NewClient(outCfg)
	if err != nil {
		return nil, fmt.Errorf("acme: %w", err)
	}
	return &Client{base: base, apiKey: cfg.APIKey, http: httpClient}, nil
}

// GetEvent fetches an event by ID. GET is idempotent, so it is retried.
func (c *Client) GetEvent(ctx context.Context, id string) (Event, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/v1/events/"+url.PathEscape(id), nil)
	if err != nil {
		return Event{}, err
	}
	var event Event
	if err := c.do(req, &event); err != nil {
		return Event{}, err
	}
	return event, nil
}

// Notify posts a notification. idempotencyKey is sent as Idempotency-Key, which
// is what makes the POST safe to retry.
func (c *Client) Notify(ctx context.Context, idempotencyKey string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("acme: encode notification: %w", err)
	}
	req, err := c.newRequest(ctx, http.MethodPost, "/v1/notifications", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", idempotencyKey)
	return c.do(req, nil)
}

func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	target := c.base.JoinPath(path)
	req, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return nil, fmt.Errorf("acme: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	return req, nil
}

func (c *Client) do(req *http.Request, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("acme: %s %s: %w", req.Method, req.URL.Path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
		return &APIError{Status: resp.StatusCode}
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(out); err != nil {
		return fmt.Errorf("acme: decode response: %w", err)
	}
	return nil
}
