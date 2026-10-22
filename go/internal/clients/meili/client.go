package meili

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/xynova/lappy-rag/internal/clients/httpfailsafe"
	derrors "github.com/xynova/lappy-rag/internal/errors"
)

// Client is a Meilisearch HTTP API client.
type Client struct {
	host       string
	masterKey  string
	httpClient *http.Client
}

// Config holds Meilisearch client construction inputs.
type Config struct {
	Host       string
	MasterKey  string
	HTTPClient *http.Client
}

// CreateClient builds a Client from Config.
func (cfg Config) CreateClient() (*Client, error) {
	host := strings.TrimRight(strings.TrimSpace(cfg.Host), "/")
	if host == "" {
		return nil, derrors.New(derrors.CodeInvalid, "meili.CreateClient", "MEILI_HOST is required")
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	return &Client{
		host:       host,
		masterKey:  strings.TrimSpace(cfg.MasterKey),
		httpClient: httpClient,
	}, nil
}

// New creates a Client from environment variables MEILI_HOST and MEILI_MASTER_KEY.
func New() (*Client, error) {
	return Config{
		Host:      os.Getenv("MEILI_HOST"),
		MasterKey: os.Getenv("MEILI_MASTER_KEY"),
	}.CreateClient()
}

// Health checks the Meilisearch /health endpoint.
func (c *Client) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.host+"/health", nil)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeInternal, "meili.Health", "build request")
	}
	resp, err := httpfailsafe.Do(ctx, c.httpClient, req, "meili")
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeUnavailable, "meili.Health", "read body")
	}
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "available") {
		return derrors.New(derrors.CodeUnavailable, "meili.Health", fmt.Sprintf("unexpected response %d", resp.StatusCode))
	}
	return nil
}

// ListIndexUIDs returns index UIDs from Meilisearch (up to limit).
func (c *Client) ListIndexUIDs(ctx context.Context, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 100
	}
	path := fmt.Sprintf("/indexes?limit=%d", limit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.host+path, nil)
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInternal, "meili.ListIndexUIDs", "build request")
	}
	if c.masterKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.masterKey)
	}
	resp, err := httpfailsafe.Do(ctx, c.httpClient, req, "meili")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var result struct {
		Results []struct {
			UID string `json:"uid"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, derrors.Wrap(err, derrors.CodeUnavailable, "meili.ListIndexUIDs", "decode response")
	}
	uids := make([]string, 0, len(result.Results))
	for _, r := range result.Results {
		uids = append(uids, r.UID)
	}
	return uids, nil
}

// indexCreateRequest is the payload for the Meilisearch create-index endpoint.
type indexCreateRequest struct {
	UID        string `json:"uid"`
	PrimaryKey string `json:"primaryKey"`
}

// Document is a single indexed content chunk.
type Document struct {
	ID             string `json:"id"`
	ProjectID      string `json:"project_id"`
	DocID          string `json:"doc_id"`
	Title          string `json:"title"`
	SectionHeading string `json:"section_heading"`
	EmbedText      string `json:"embed_text"`
	Content        string `json:"content"`
	Snippet        string `json:"snippet"`
	Path           string `json:"path"`
	SourceName     string `json:"source_name"`
	DocType        string `json:"doc_type"`
	ChunkStrategy  string `json:"chunk_strategy"`
	ChunkIndex     int    `json:"chunk_index"`
	UpdatedAt      string `json:"updated_at"`
}

// EnsureIndex creates the index if it does not already exist.
func (c *Client) EnsureIndex(ctx context.Context, indexName string) error {
	body := indexCreateRequest{UID: indexName, PrimaryKey: "id"}
	status, _, err := c.request(ctx, http.MethodPost, "/indexes", body)
	if err != nil {
		if status == http.StatusConflict || status == http.StatusBadRequest {
			return nil
		}
		return fmt.Errorf("ensure meilisearch index %q: %w", indexName, err)
	}

	if status != http.StatusAccepted && status != http.StatusCreated {
		return fmt.Errorf("ensure meilisearch index %q: unexpected status %d", indexName, status)
	}

	return nil
}

// ApplySettings configures searchable, filterable, and sortable attributes.
func (c *Client) ApplySettings(ctx context.Context, indexName string) error {
	settings := map[string]any{
		"searchableAttributes": []string{
			"title",
			"section_heading",
			"content",
			"snippet",
			"source_name",
			"doc_type",
		},
		"filterableAttributes": []string{
			"project_id",
			"path",
			"source_name",
			"doc_type",
			"chunk_strategy",
			"chunk_index",
		},
		"sortableAttributes": []string{"updated_at"},
	}

	status, task, err := c.request(ctx, http.MethodPatch, fmt.Sprintf("/indexes/%s/settings", indexName), settings)
	if err != nil {
		return fmt.Errorf("apply index settings for %q: %w", indexName, err)
	}

	if status != http.StatusAccepted {
		return fmt.Errorf("apply index settings for %q: unexpected status %d", indexName, status)
	}

	return c.waitTask(ctx, task.TaskUID)
}

// EnableChatCompletions enables Meilisearch's experimental chatCompletions feature.
func (c *Client) EnableChatCompletions(ctx context.Context) error {
	featureKey, err := c.resolveEmbeddingFeatureKey(ctx)
	if err != nil {
		return err
	}

	payload := map[string]any{featureKey: true}

	status, _, err := c.request(ctx, http.MethodPatch, "/experimental-features", payload)
	if err != nil {
		return fmt.Errorf("enable meilisearch embedding experimental feature %q: %w", featureKey, err)
	}

	if status != http.StatusOK {
		return fmt.Errorf("enable meilisearch embedding experimental feature %q: unexpected status %d", featureKey, status)
	}

	return nil
}

func (c *Client) resolveEmbeddingFeatureKey(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.host+"/experimental-features", nil)
	if err != nil {
		return "", fmt.Errorf("create experimental features request: %w", err)
	}

	if c.masterKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.masterKey)
	}

	resp, err := httpfailsafe.Do(ctx, c.httpClient, req, "meili")
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read experimental features response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("read experimental features: status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	features := map[string]any{}
	if err := json.Unmarshal(raw, &features); err != nil {
		return "", fmt.Errorf("unmarshal experimental features: %w", err)
	}

	if _, ok := features["chatCompletions"]; ok {
		return "chatCompletions", nil
	}

	if _, ok := features["compositeEmbedders"]; ok {
		return "compositeEmbedders", nil
	}

	keys := make([]string, 0, len(features))
	for key := range features {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	return "", fmt.Errorf("no supported meilisearch embedding feature key found (expected chatCompletions or compositeEmbedders; available: %s)", strings.Join(keys, ", "))
}

// CancelIndexTasks cancels all enqueued and processing tasks for the given index.
// This is used before applying settings to clear any stale background tasks (e.g.
// a stuck re-embedding loop) that would otherwise block the settings task queue.
func (c *Client) CancelIndexTasks(ctx context.Context, indexName string) error {
	path := fmt.Sprintf("/tasks/cancel?indexUids=%s&statuses=enqueued,processing", indexName)
	status, task, err := c.request(ctx, http.MethodPost, path, nil)
	if err != nil {
		return fmt.Errorf("cancel tasks for index %q: %w", indexName, err)
	}
	if status != http.StatusOK {
		return fmt.Errorf("cancel tasks for index %q: unexpected status %d", indexName, status)
	}
	_ = task
	return nil
}

// ApplyEmbedders configures the REST embedder from environment variables EMBED_URL, EMBED_MODEL,
// EMBED_DIMENSIONS, and EMBED_DOCUMENT_TEMPLATE.
func (c *Client) ApplyEmbedders(ctx context.Context, indexName string) error {
	embedURL := strings.TrimRight(strings.TrimSpace(os.Getenv("EMBED_URL")), "/")
	if embedURL == "" {
		return fmt.Errorf("EMBED_URL is required")
	}

	embedModel := strings.TrimSpace(os.Getenv("EMBED_MODEL"))
	if embedModel == "" {
		embedModel = "bge-base-en-v1.5"
	}

	dimensions := envInt("EMBED_DIMENSIONS", 768)

	documentTemplate := strings.TrimSpace(os.Getenv("EMBED_DOCUMENT_TEMPLATE"))
	if documentTemplate == "" {
		documentTemplate = "{{doc.embed_text | truncatewords: 140}}"
	}

	payload := map[string]any{
		"default": map[string]any{
			"source": "rest",
			"url":    embedURL + "/embeddings",
			"request": map[string]any{
				"model": embedModel,
				"input": "{{text}}",
			},
			"response": map[string]any{
				"data": []map[string]any{{
					"embedding": "{{embedding}}",
				}},
			},
			"dimensions":       dimensions,
			"documentTemplate": documentTemplate,
		},
	}

	status, task, err := c.request(ctx, http.MethodPatch, fmt.Sprintf("/indexes/%s/settings/embedders", indexName), payload)
	if err != nil {
		return fmt.Errorf("apply embedders for %q: %w", indexName, err)
	}

	if status != http.StatusAccepted {
		return fmt.Errorf("apply embedders for %q: unexpected status %d", indexName, status)
	}

	return c.waitTask(ctx, task.TaskUID)
}

// DeleteAllDocuments removes every document from the index.
func (c *Client) DeleteAllDocuments(ctx context.Context, indexName string) error {
	status, task, err := c.request(ctx, http.MethodDelete, fmt.Sprintf("/indexes/%s/documents", indexName), nil)
	if err != nil {
		return fmt.Errorf("delete documents for %q: %w", indexName, err)
	}

	if status != http.StatusAccepted {
		return fmt.Errorf("delete documents for %q: unexpected status %d", indexName, status)
	}

	return c.waitTask(ctx, task.TaskUID)
}

// AddDocuments indexes a batch of documents and returns the count written.
func (c *Client) AddDocuments(ctx context.Context, indexName string, docs []Document) (int, error) {
	status, task, err := c.request(ctx, http.MethodPost, fmt.Sprintf("/indexes/%s/documents?primaryKey=id", indexName), docs)
	if err != nil {
		return 0, fmt.Errorf("add documents to %q: %w", indexName, err)
	}

	if status != http.StatusAccepted {
		return 0, fmt.Errorf("add documents to %q: unexpected status %d", indexName, status)
	}

	if err := c.waitTask(ctx, task.TaskUID); err != nil {
		return 0, err
	}

	return len(docs), nil
}

// SearchHit is a single document returned from a Meilisearch search response.
type SearchHit = map[string]any

// SearchPayload is an arbitrary JSON object sent to the Meilisearch search endpoint.
type SearchPayload = map[string]any

// SearchResponse holds hits returned from a Meilisearch search.
type SearchResponse struct {
	Hits []SearchHit `json:"hits"`
}

// FacetResponse holds the raw response including facetDistribution from a Meilisearch search.
type FacetResponse struct {
	FacetDistribution map[string]map[string]int `json:"facetDistribution"`
}

// Search performs a search against the given index and returns matching hits.
func (c *Client) Search(ctx context.Context, indexName string, payload SearchPayload) ([]SearchHit, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal meili search payload: %w", err)
	}

	endpoint := fmt.Sprintf("%s/indexes/%s/search", c.host, indexName)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("create meili search request for %q: %w", indexName, err)
	}

	req.Header.Set("Content-Type", "application/json")
	if c.masterKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.masterKey)
	}

	resp, err := httpfailsafe.Do(ctx, c.httpClient, req, "meili")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read meili search response for %q: %w", indexName, err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("meili search failed for %q: status %d: %s", indexName, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed SearchResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse meili search response for %q: %w", indexName, err)
	}

	return parsed.Hits, nil
}

// SearchFacets performs a facet-only query and returns the facetDistribution map.
func (c *Client) SearchFacets(ctx context.Context, indexName string, payload SearchPayload) (map[string]map[string]int, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal meili facet payload: %w", err)
	}

	endpoint := fmt.Sprintf("%s/indexes/%s/search", c.host, indexName)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("create meili facet request for %q: %w", indexName, err)
	}

	req.Header.Set("Content-Type", "application/json")
	if c.masterKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.masterKey)
	}

	resp, err := httpfailsafe.Do(ctx, c.httpClient, req, "meili")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read meili facet response for %q: %w", indexName, err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("meili facet query failed for %q: status %d: %s", indexName, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed FacetResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse meili facet response for %q: %w", indexName, err)
	}

	return parsed.FacetDistribution, nil
}

// DeleteDocumentsByPath removes all chunks for a given project and file path.
func (c *Client) DeleteDocumentsByPath(ctx context.Context, indexName, projectID, relativePath string) error {
	filter := fmt.Sprintf(
		`project_id = "%s" AND path = "%s"`,
		escapeFilterValue(projectID),
		escapeFilterValue(relativePath),
	)

	payload := map[string]any{
		"filter": []string{filter},
	}

	status, task, err := c.request(ctx, http.MethodPost, fmt.Sprintf("/indexes/%s/documents/delete", indexName), payload)
	if err != nil {
		return fmt.Errorf("delete documents for %q path %q: %w", indexName, relativePath, err)
	}

	if status != http.StatusAccepted {
		return fmt.Errorf("delete documents for %q path %q: unexpected status %d", indexName, relativePath, status)
	}

	return c.waitTask(ctx, task.TaskUID)
}

type taskEnvelope struct {
	TaskUID int64 `json:"taskUid"`
}

func (c *Client) request(ctx context.Context, method, path string, payload any) (int, taskEnvelope, error) {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return 0, taskEnvelope{}, fmt.Errorf("marshal request payload: %w", err)
		}
		body = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.host+path, body)
	if err != nil {
		return 0, taskEnvelope{}, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if c.masterKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.masterKey)
	}

	resp, err := httpfailsafe.Do(ctx, c.httpClient, req, "meili")
	if err != nil {
		return 0, taskEnvelope{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, taskEnvelope{}, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode >= 400 {
		return resp.StatusCode, taskEnvelope{}, fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	if len(raw) == 0 {
		return resp.StatusCode, taskEnvelope{}, nil
	}

	var task taskEnvelope
	if err := json.Unmarshal(raw, &task); err != nil {
		return resp.StatusCode, taskEnvelope{}, fmt.Errorf("unmarshal meilisearch response: %w", err)
	}

	return resp.StatusCode, task, nil
}

// waitTask polls until the Meilisearch task succeeds, fails, or the context deadline is reached.
func (c *Client) waitTask(ctx context.Context, taskUID int64) error {
	if taskUID <= 0 {
		return nil
	}

	tctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	attempt := 0

	for {
		select {
		case <-tctx.Done():
			return fmt.Errorf("timeout waiting for meilisearch task %d: %w", taskUID, tctx.Err())
		default:
		}

		status, raw, err := c.taskStatus(tctx, taskUID)
		if err != nil {
			if isTransientError(err) {
				attempt++
				time.Sleep(retryBackoff(attempt))
				continue
			}
			return fmt.Errorf("query meilisearch task %d status: %w", taskUID, err)
		}
		attempt = 0

		if status == "succeeded" {
			return nil
		}

		if status == "failed" {
			return fmt.Errorf("meilisearch task %d failed: %s", taskUID, raw)
		}

		time.Sleep(300 * time.Millisecond)
	}
}

func (c *Client) taskStatus(ctx context.Context, taskUID int64) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/tasks/%d", c.host, taskUID), nil)
	if err != nil {
		return "", "", fmt.Errorf("create task status request: %w", err)
	}

	if c.masterKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.masterKey)
	}

	resp, err := httpfailsafe.Do(ctx, c.httpClient, req, "meili")
	if err != nil {
		return "", "", err
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("read task status body: %w", err)
	}

	if resp.StatusCode >= 400 {
		return "", string(raw), fmt.Errorf("task lookup failed: status %d", resp.StatusCode)
	}

	var payload struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", string(raw), fmt.Errorf("unmarshal task status: %w", err)
	}

	return payload.Status, string(raw), nil
}

func escapeFilterValue(value string) string {
	return strings.ReplaceAll(value, `"`, `\"`)
}

func retryBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 6 {
		attempt = 6
	}

	multiplier := 1 << (attempt - 1)
	return time.Duration(multiplier) * 100 * time.Millisecond
}

func isTransientError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return false
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}

	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "connection reset") ||
		strings.Contains(lower, "connection refused") ||
		strings.Contains(lower, "broken pipe") ||
		strings.Contains(lower, "timeout") ||
		strings.Contains(lower, "temporary")
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}

	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}

	return parsed
}
