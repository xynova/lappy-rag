// Package localai provides an HTTP client for the LocalAI OpenAI-compatible inference API.
package localai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/xynova/lappy-rag/internal/clients/httpfailsafe"
	derrors "github.com/xynova/lappy-rag/internal/errors"
)

// ChatRequest is the payload sent to the LocalAI /v1/chat/completions endpoint.
type ChatRequest = map[string]any

// Client is an HTTP client for the LocalAI inference service.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// Config holds LocalAI client construction inputs.
type Config struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

// CreateClient builds a Client from Config.
func (cfg Config) CreateClient() (*Client, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = "http://localai:8080/v1"
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	return &Client{
		baseURL:    baseURL,
		apiKey:     strings.TrimSpace(cfg.APIKey),
		httpClient: httpClient,
	}, nil
}

// New creates a Client from the LOCALAI_BASE_URL and LOCALAI_API_KEY environment variables.
// Falls back to http://localai:8080/v1 when LOCALAI_BASE_URL is unset.
func New() (*Client, error) {
	return Config{
		BaseURL: os.Getenv("LOCALAI_BASE_URL"),
		APIKey:  os.Getenv("LOCALAI_API_KEY"),
	}.CreateClient()
}

// NewWithConfig creates a Client with an explicit base URL, API key, and HTTP client.
func NewWithConfig(baseURL, apiKey string, httpClient *http.Client) *Client {
	if httpClient == nil {
		panic("httpClient cannot be nil")
	}
	c, err := Config{BaseURL: baseURL, APIKey: apiKey, HTTPClient: httpClient}.CreateClient()
	if err != nil {
		panic(err)
	}
	return c
}

// HTTPClient returns the underlying *http.Client for reuse by callers that need
// to construct a one-off client with a different base URL but the same transport.
func (c *Client) HTTPClient() *http.Client {
	return c.httpClient
}

// PingEmbed posts a minimal embeddings request to verify the embed endpoint.
func (c *Client) PingEmbed(ctx context.Context, embedURL, model string) error {
	embedURL = strings.TrimRight(strings.TrimSpace(embedURL), "/")
	if embedURL == "" {
		return derrors.New(derrors.CodeInvalid, "localai.PingEmbed", "embed URL is required")
	}
	if strings.TrimSpace(model) == "" {
		model = "bge-base-en-v1.5"
	}
	payload, err := json.Marshal(map[string]any{
		"model": model,
		"input": "ping",
	})
	if err != nil {
		return derrors.Wrap(err, derrors.CodeInternal, "localai.PingEmbed", "marshal payload")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, embedURL+"/embeddings", bytes.NewReader(payload))
	if err != nil {
		return derrors.Wrap(err, derrors.CodeInternal, "localai.PingEmbed", "build request")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpfailsafe.Do(ctx, c.httpClient, req, "localai")
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return derrors.New(derrors.CodeUnavailable, "localai.PingEmbed", fmt.Sprintf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body))))
	}
	return nil
}

// ChatCompletions sends a chat completions request and returns the raw status code and body.
// The caller is responsible for interpreting the response body.
func (c *Client) ChatCompletions(ctx context.Context, payload ChatRequest) (int, []byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, derrors.Wrap(err, derrors.CodeInternal, "localai.ChatCompletions", "marshal payload")
	}

	endpoint := c.baseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return 0, nil, derrors.Wrap(err, derrors.CodeInternal, "localai.ChatCompletions", "build request")
	}

	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := httpfailsafe.Do(ctx, c.httpClient, req, "localai")
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, derrors.Wrap(err, derrors.CodeUnavailable, "localai.ChatCompletions", "read body")
	}

	return resp.StatusCode, body, nil
}
