// Package ingest provides an HTTP client for the ingest-api service.
package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/xynova/lappy-rag/internal/clients/httpfailsafe"
	derrors "github.com/xynova/lappy-rag/internal/errors"
)

// JobResponse mirrors the ingest-api Job JSON shape.
type JobResponse struct {
	ID             string `json:"id"`
	ProjectID      string `json:"project_id"`
	IndexName      string `json:"index_name"`
	Mode           string `json:"mode"`
	TTLMinutes     int    `json:"ttl_minutes,omitempty"`
	Status         string `json:"status"`
	Error          string `json:"error,omitempty"`
	FilesScanned   int    `json:"files_scanned"`
	FilesChanged   int    `json:"files_changed"`
	FilesSkipped   int    `json:"files_skipped"`
	FilesDeleted   int    `json:"files_deleted"`
	ChunksPlanned  int    `json:"chunks_planned"`
	ChunksIndexed  int    `json:"chunks_indexed"`
	QueuedAt       string `json:"queued_at"`
	StartedAt      string `json:"started_at,omitempty"`
	CompletedAt    string `json:"completed_at,omitempty"`
	DurationMillis int64  `json:"duration_millis,omitempty"`
}

// SearchResponse mirrors the ingest-api /query/search response shape.
type SearchResponse struct {
	Hits []map[string]any `json:"hits"`
}

// AskResponse mirrors the ingest-api /query/ask response shape.
type AskResponse struct {
	Question      string           `json:"question"`
	PlanID        string           `json:"plan_id"`
	RoutedIndexes []string         `json:"routed_indexes"`
	QueryPlan     []string         `json:"query_plan"`
	Answer        string           `json:"answer"`
	Sources       []map[string]any `json:"sources"`
	Model         string           `json:"model"`
}

// AvailableIndex mirrors ingest-api index discovery metadata.
type AvailableIndex struct {
	ProjectID   string   `json:"project_id"`
	IndexName   string   `json:"index_name"`
	Description string   `json:"description"`
	Topics      []string `json:"topics"`
}

// PlanResponse mirrors the ingest-api /query/plan response shape.
type PlanResponse struct {
	Question         string           `json:"question"`
	PlanID           string           `json:"plan_id"`
	ReuseSource      string           `json:"reuse_source"`
	RoutedIndexes    []string         `json:"routed_indexes"`
	AvailableIndexes []AvailableIndex `json:"available_indexes"`
	QueryPlan        []string         `json:"query_plan"`
	CandidateSources []map[string]any `json:"candidate_sources"`
}

// Client is an HTTP client for the ingest-api service.
type Client struct {
	host       string
	httpClient *http.Client
}

// Config holds ingest-api client construction inputs.
type Config struct {
	Host       string
	HTTPClient *http.Client
}

// CreateClient builds a Client from Config.
func (cfg Config) CreateClient() (*Client, error) {
	host := strings.TrimRight(strings.TrimSpace(cfg.Host), "/")
	if host == "" {
		host = "http://ingest-api:8090"
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	return &Client{host: host, httpClient: httpClient}, nil
}

// New creates a Client from the INGEST_API_HOST environment variable.
// Falls back to http://ingest-api:8090 when INGEST_API_HOST is unset.
func New() (*Client, error) {
	return Config{Host: os.Getenv("INGEST_API_HOST")}.CreateClient()
}

// NewWithHost creates a Client with an explicit host and HTTP client.
func NewWithHost(host string, httpClient *http.Client) *Client {
	if httpClient == nil {
		panic("httpClient cannot be nil")
	}

	return &Client{
		host:       strings.TrimRight(strings.TrimSpace(host), "/"),
		httpClient: httpClient,
	}
}

// Search calls /query/search and returns the hits.
func (c *Client) Search(ctx context.Context, query string, topK int) ([]map[string]any, error) {
	var resp SearchResponse
	payload := map[string]any{
		"query": query,
		"top_k": topK,
	}

	if err := c.postJSON(ctx, "/query/search", payload, &resp); err != nil {
		return nil, derrors.Wrap(err, derrors.CodeUnavailable, "ingest", "ingest search")
	}

	return resp.Hits, nil
}

// Plan calls /query/plan and returns the proposed plan and candidate sources.
func (c *Client) Plan(ctx context.Context, question string, topK int, sessionID string) (PlanResponse, error) {
	var resp PlanResponse
	payload := map[string]any{
		"question": question,
		"top_k":    topK,
	}
	if strings.TrimSpace(sessionID) != "" {
		payload["session_id"] = strings.TrimSpace(sessionID)
	}

	if err := c.postJSON(ctx, "/query/plan", payload, &resp); err != nil {
		return PlanResponse{}, derrors.Wrap(err, derrors.CodeUnavailable, "ingest", "ingest plan")
	}

	if strings.TrimSpace(resp.Question) == "" {
		resp.Question = strings.TrimSpace(question)
	}
	if strings.TrimSpace(resp.PlanID) == "" {
		return PlanResponse{}, derrors.New(derrors.CodeUnavailable, "ingest", "ingest plan: empty plan_id in response")
	}
	if len(resp.QueryPlan) == 0 {
		return PlanResponse{}, derrors.New(derrors.CodeUnavailable, "ingest", "ingest plan: empty query plan in response")
	}

	return resp, nil
}

// Ask calls /query/ask and returns the answer text.
func (c *Client) Ask(ctx context.Context, question string, topK int) (string, error) {
	resp, err := c.AskDetailed(ctx, question, topK)
	if err != nil {
		return "", err
	}

	return resp.Answer, nil
}

// AskDetailed calls /query/ask and returns the full response payload.
func (c *Client) AskDetailed(ctx context.Context, question string, topK int) (AskResponse, error) {
	return c.AskDetailedWithPlan(ctx, question, topK, "", "")
}

// AskDetailedWithPlan calls /query/ask and returns the full response payload.
// When planID is set, ingest-api executes exactly the confirmed plan snapshot.
func (c *Client) AskDetailedWithPlan(ctx context.Context, question string, topK int, planID, sessionID string) (AskResponse, error) {
	var resp AskResponse
	payload := map[string]any{
		"question": question,
		"top_k":    topK,
	}
	if strings.TrimSpace(planID) != "" {
		payload["plan_id"] = strings.TrimSpace(planID)
	}
	if strings.TrimSpace(sessionID) != "" {
		payload["session_id"] = strings.TrimSpace(sessionID)
	}

	if err := c.postJSON(ctx, "/query/ask", payload, &resp); err != nil {
		return AskResponse{}, derrors.Wrap(err, derrors.CodeUnavailable, "ingest", "ingest ask")
	}

	answer := strings.TrimSpace(resp.Answer)
	if answer == "" {
		return AskResponse{}, derrors.New(derrors.CodeUnavailable, "ingest", "ingest ask: empty answer in response")
	}
	resp.Answer = answer

	return resp, nil
}

// Enqueue triggers a new ingest job and returns the created job.
func (c *Client) Enqueue(ctx context.Context, projectID, mode string) (JobResponse, error) {
	var job JobResponse
	payload := map[string]any{
		"project_id": projectID,
		"mode":       mode,
	}

	if err := c.postJSON(ctx, "/jobs/ingest", payload, &job); err != nil {
		return JobResponse{}, derrors.Wrap(err, derrors.CodeUnavailable, "ingest", "enqueue ingest job")
	}

	if strings.TrimSpace(job.ID) == "" {
		return JobResponse{}, derrors.New(derrors.CodeUnavailable, "ingest", "enqueue ingest job: missing job id in response")
	}

	return job, nil
}

// GetJob retrieves the current status of an ingest job by ID.
func (c *Client) GetJob(ctx context.Context, jobID string) (JobResponse, error) {
	endpoint := fmt.Sprintf("%s/jobs/%s", c.host, jobID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return JobResponse{}, derrors.Wrap(err, derrors.CodeUnavailable, "ingest", "create get-job request")
	}

	resp, err := httpfailsafe.Do(ctx, c.httpClient, req, "ingest-api")
	if err != nil {
		return JobResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return JobResponse{}, derrors.Wrap(err, derrors.CodeUnavailable, "ingest", "read get-job response")
	}

	if resp.StatusCode >= 400 {
		return JobResponse{}, derrors.New(derrors.CodeUnavailable, "ingest", fmt.Sprintf("get-job request failed: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body))))
	}

	var job JobResponse
	if err := json.Unmarshal(body, &job); err != nil {
		return JobResponse{}, derrors.Wrap(err, derrors.CodeUnavailable, "ingest", "parse get-job response")
	}

	return job, nil
}

// PollJob polls /jobs/{id} until the job reaches a terminal status, the context
// is cancelled, or the poll interval fires.
func (c *Client) PollJob(ctx context.Context, jobID string, pollInterval time.Duration) (JobResponse, error) {
	for {
		job, err := c.GetJob(ctx, jobID)
		if err != nil {
			return JobResponse{}, err
		}

		switch strings.ToLower(strings.TrimSpace(job.Status)) {
		case "completed", "failed":
			return job, nil
		}

		select {
		case <-ctx.Done():
			return JobResponse{}, derrors.Wrap(ctx.Err(), derrors.CodeDeadline, "ingest.PollJob", "poll ingest job timed out")
		case <-time.After(pollInterval):
		}
	}
}

func (c *Client) postJSON(ctx context.Context, path string, payload, out any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeUnavailable, "ingest", "marshal request payload")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.host+path, bytes.NewReader(raw))
	if err != nil {
		return derrors.Wrap(err, derrors.CodeUnavailable, "ingest", "create request")
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := httpfailsafe.Do(ctx, c.httpClient, req, "ingest-api")
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeUnavailable, "ingest", "read response")
	}

	if resp.StatusCode >= 400 {
		return derrors.New(derrors.CodeUnavailable, "ingest", fmt.Sprintf("request failed: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body))))
	}

	if len(body) == 0 {
		return nil
	}

	if err := json.Unmarshal(body, out); err != nil {
		return derrors.Wrap(err, derrors.CodeUnavailable, "ingest", "parse response")
	}

	return nil
}
