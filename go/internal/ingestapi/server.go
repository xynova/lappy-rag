package ingestapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xynova/lappy-rag/internal/clients/httpfailsafe"
	"github.com/xynova/lappy-rag/internal/clients/localai"
	"github.com/xynova/lappy-rag/internal/clients/meili"
	"github.com/xynova/lappy-rag/internal/dspygo"
	derrors "github.com/xynova/lappy-rag/internal/errors"
	"github.com/xynova/lappy-rag/internal/ingestapi/sessionstore"
	"github.com/xynova/lappy-rag/internal/jobs"
	"github.com/xynova/lappy-rag/internal/observability"
	"github.com/xynova/lappy-rag/internal/registry"

	"go.opentelemetry.io/otel/sdk/trace"

	"gopkg.in/yaml.v3"
)

var encodeLogger = observability.NewLogger("ingest-api")

type ingestRequest struct {
	ProjectID  string `json:"project_id"`
	Mode       string `json:"mode"`
	TTLMinutes int    `json:"ttl_minutes"`
}

type cancelIngestRequest struct {
	ProjectID string `json:"project_id"`
}

// Server is the ingest-api HTTP service.
type Server struct {
	registryPath     string
	chatPath         string
	traceService     string
	inferenceTimeout time.Duration
	chatDefaults     queryChatConfig
	chatPersonas     map[string]chatPersonaSettings
	chatModels       map[string]chatModelSettings
	manager          *jobs.Manager
	queryPlanner     *dspygo.Planner
	meiliClient      *meili.Client
	localAIClient    *localai.Client
	sessionStore     *sessionstore.Store
	clock            func() time.Time
	logger           *observability.Logger
	httpClient       *http.Client
	planMu           sync.RWMutex
	plannedQueries   map[string]plannedQuery
	sessionPlans     map[string][]plannedQuery
	planTTL          time.Duration
}

type chatPersonaSettings struct {
	Style       string   `yaml:"style"`
	Model       string   `yaml:"model"`
	Temperature *float64 `yaml:"temperature"`
	MaxTokens   *int     `yaml:"max_tokens"`
}

type chatModelSettings struct {
	Temperature *float64 `yaml:"temperature" json:"temperature,omitempty"`
	MaxTokens   *int     `yaml:"max_tokens" json:"max_tokens,omitempty"`
}

type chatSettings struct {
	OpenInference observability.OpenInferenceConfig `yaml:"openinference"`
	Defaults      queryChatConfig                   `yaml:"defaults"`
	Personas      map[string]chatPersonaSettings    `yaml:"personas"`
	Models        map[string]chatModelSettings      `yaml:"models"`
}

type querySearchRequest struct {
	Query         string  `json:"query"`
	TopK          int     `json:"top_k"`
	ProjectID     string  `json:"project_id"`
	IndexName     string  `json:"index_name"`
	SemanticRatio float64 `json:"semantic_ratio"`
}

type querySearchResponse struct {
	Query         string           `json:"query"`
	RoutedIndexes []string         `json:"routed_indexes"`
	Chat          queryChatConfig  `json:"chat"`
	Hits          []map[string]any `json:"hits"`
}

type queryChatPrompts struct {
	System              string `json:"system,omitempty"`
	SearchDescription   string `json:"search_description,omitempty"`
	SearchQParam        string `json:"search_q_param,omitempty"`
	SearchFilterParam   string `json:"search_filter_param,omitempty"`
	SearchIndexUIDParam string `json:"search_index_uid_param,omitempty"`
}

type queryChatConfig struct {
	Workspace   string           `json:"workspace,omitempty"`
	Source      string           `json:"source,omitempty"`
	BaseURL     string           `json:"base_url,omitempty"`
	APIKey      string           `json:"api_key,omitempty"`
	Model       string           `json:"model,omitempty"`
	Temperature *float64         `json:"temperature,omitempty"`
	MaxTokens   *int             `json:"max_tokens,omitempty"`
	Prompts     queryChatPrompts `json:"prompts,omitempty"`
}

type queryAskRequest struct {
	Question      string   `json:"question"`
	SessionID     string   `json:"session_id"`
	PlanID        string   `json:"plan_id"`
	Query         string   `json:"query"`
	TopK          int      `json:"top_k"`
	ProjectID     string   `json:"project_id"`
	IndexName     string   `json:"index_name"`
	Temperature   *float64 `json:"temperature"`
	MaxTokens     *int     `json:"max_tokens"`
	SemanticRatio float64  `json:"semantic_ratio"`
}

type queryPlanRequest struct {
	Question      string  `json:"question"`
	SessionID     string  `json:"session_id"`
	Query         string  `json:"query"`
	TopK          int     `json:"top_k"`
	ProjectID     string  `json:"project_id"`
	IndexName     string  `json:"index_name"`
	SemanticRatio float64 `json:"semantic_ratio"`
}

type queryPlanResponse struct {
	Question         string           `json:"question"`
	PlanID           string           `json:"plan_id"`
	ReuseSource      string           `json:"reuse_source"`
	RoutedIndexes    []string         `json:"routed_indexes"`
	AvailableIndexes []availableIndex `json:"available_indexes"`
	QueryPlan        []string         `json:"query_plan"`
	CandidateSources []map[string]any `json:"candidate_sources"`
}

type availableIndex struct {
	ProjectID   string   `json:"project_id"`
	IndexName   string   `json:"index_name"`
	Description string   `json:"description"`
	Topics      []string `json:"topics"`
}

type plannedQuery struct {
	SessionID        string
	Question         string
	RoutedIndexes    []string
	QueryPlan        []string
	CandidateSources []map[string]any
	TopK             int
	SemanticRatio    float64
	CreatedAt        time.Time
}

type queryAskResponse struct {
	Question      string           `json:"question"`
	PlanID        string           `json:"plan_id"`
	RoutedIndexes []string         `json:"routed_indexes"`
	QueryPlan     []string         `json:"query_plan"`
	Answer        string           `json:"answer"`
	Sources       []map[string]any `json:"sources"`
	Model         string           `json:"model"`
}

// Run starts the ingest-api HTTP server (blocking). Prefer Serve from CLI wiring.
func Run() error {
	return Serve(context.Background())
}

// Serve runs the HTTP server until the context is canceled or the server fails.
func Serve(ctx context.Context) error {
	registryPath := detectProjectsPath()
	chatPath := detectChatSettingsPath()
	defaults, personas, models, traceCfg, err := loadChatSettings(chatPath)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeUnavailable, "ingestapi.Serve", "load chat settings")
	}
	traceServiceName := observability.ServiceNameFromConfig(&traceCfg)
	logger := observability.NewLogger("ingest-api")

	var otelProvider *trace.TracerProvider
	if traceCfg.Enabled {
		provider, err := observability.InitOTEL(traceServiceName, traceCfg.Endpoint)
		if err != nil {
			return derrors.Wrap(err, derrors.CodeUnavailable, "ingestapi.Serve", "initialize openinference tracer provider")
		}
		otelProvider = provider
	}

	projectsCfg, err := registry.LoadMany(registryPath)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeUnavailable, "ingestapi.Serve", "load projects config")
	}

	maxGlobal := getenvInt("MAX_CONCURRENT_JOBS", 3)
	maxPerProject := getenvInt("MAX_CONCURRENT_PER_PROJECT", 1)

	searchClient, err := meili.New()
	if err != nil {
		return derrors.Wrap(err, derrors.CodeUnavailable, "ingestapi.Serve", "initialize meilisearch client")
	}

	allowSymlinks := strings.EqualFold(strings.TrimSpace(os.Getenv("ALLOW_FOLLOW_SYMLINKS")), "true")
	stateDir := getenv("STATE_DIR", "/app/state")
	stateStore := jobs.NewFileStateStore(stateDir)
	clock := jobs.NewRealClock()

	manager := jobs.ManagerConfig{
		MaxGlobal:     maxGlobal,
		MaxPerProject: maxPerProject,
		AllowSymlinks: allowSymlinks,
		Projects:      projectsCfg,
		IndexAdmin:    searchClient,
		Documents:     searchClient,
		State:         stateStore,
		Clock:         clock,
	}.CreateManager()

	inferenceTimeout := time.Duration(getenvInt("INFERENCE_TIMEOUT_SECONDS", 240)) * time.Second

	localAIClient, err := localai.New()
	if err != nil {
		return derrors.Wrap(err, derrors.CodeUnavailable, "ingestapi.Serve", "initialize localai client")
	}

	sessionStore, err := sessionstore.Open(sessionstore.Config{
		DBPath: filepath.Join(stateDir, "query-plans.db"),
		Now:    clock.Now,
	})
	if err != nil {
		return derrors.Wrap(err, derrors.CodeUnavailable, "ingestapi.Serve", "initialize durable query session store")
	}

	server := &Server{
		registryPath:     registryPath,
		chatPath:         chatPath,
		traceService:     traceServiceName,
		inferenceTimeout: inferenceTimeout,
		chatDefaults:     defaults,
		chatPersonas:     personas,
		chatModels:       models,
		manager:          manager,
		queryPlanner:     dspygo.NewPlanner(os.Getenv("LOCALAI_BASE_URL"), os.Getenv("LOCALAI_CHAT_MODEL")),
		meiliClient:      searchClient,
		localAIClient:    localAIClient,
		sessionStore:     sessionStore,
		clock:            clock.Now,
		logger:           logger,
		httpClient:       &http.Client{},
		plannedQueries:   make(map[string]plannedQuery),
		sessionPlans:     make(map[string][]plannedQuery),
		planTTL:          time.Duration(getenvInt("QUERY_PLAN_TTL_SECONDS", 600)) * time.Second,
	}

	mux := http.NewServeMux()
	mux.Handle("/healthz", observability.TraceHTTP(server.traceService, "ingest_api.healthz", http.HandlerFunc(server.handleHealth)))
	mux.Handle("/diagnostics", observability.TraceHTTP(server.traceService, "ingest_api.diagnostics", http.HandlerFunc(server.handleDiagnostics)))
	mux.Handle("/projects", observability.TraceHTTP(server.traceService, "ingest_api.projects.list", http.HandlerFunc(server.handleProjects)))
	mux.Handle("/projects/reload", observability.TraceHTTP(server.traceService, "ingest_api.projects.reload", http.HandlerFunc(server.handleReload)))
	mux.Handle("/jobs/ingest", observability.TraceHTTP(server.traceService, "ingest_api.jobs.ingest", http.HandlerFunc(server.handleIngest)))
	mux.Handle("/jobs/cancel", observability.TraceHTTP(server.traceService, "ingest_api.jobs.cancel", http.HandlerFunc(server.handleCancelIngest)))
	mux.Handle("/jobs/drop", observability.TraceHTTP(server.traceService, "ingest_api.jobs.drop", http.HandlerFunc(server.handleDropIndexes)))
	mux.Handle("/jobs/", observability.TraceHTTP(server.traceService, "ingest_api.jobs.get", http.HandlerFunc(server.handleGetJob)))
	mux.Handle("/query/search", observability.TraceHTTP(server.traceService, "ingest_api.query.search", http.HandlerFunc(server.handleQuerySearch)))
	mux.Handle("/query/plan", observability.TraceHTTP(server.traceService, "ingest_api.query.plan", http.HandlerFunc(server.handleQueryPlan)))
	mux.Handle("/query/ask", observability.TraceHTTP(server.traceService, "ingest_api.query.ask", http.HandlerFunc(server.handleQueryAsk)))

	httpServer := &http.Server{
		Addr:              ":8090",
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      inferenceTimeout + 30*time.Second,
		IdleTimeout:       inferenceTimeout + 60*time.Second,
	}

	return listenAndServe(ctx, httpServer, logger, func() {
		if sessionStore != nil {
			_ = sessionStore.Close()
		}
		if otelProvider != nil {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := observability.ShutdownOTEL(shutdownCtx, otelProvider); err != nil {
				logger.Warnf("failed to shutdown openinference tracer provider: %v", err)
			}
		}
	})
}

func detectProjectsPath() string {
	candidates := []string{"/app/configs/bundles", "../configs/bundles", "configs/bundles"}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}

	return "../configs/bundles"
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok"}); err != nil {
		s.logger.Warnf("failed to write health response: %v", err)
	}
}

const (
	diagStatusError   = "error"
	diagNameInference = "localai_inference"
	diagNameMCP       = "mcp_server"
)

// diagCheck is the result of a single diagnostic probe.
type diagCheck struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	Message   string `json:"message"`
	LatencyMs int64  `json:"latency_ms"`
}

func (s *Server) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	checks := []diagCheck{
		s.diagSelf(),
		s.diagQueryPlanner(),
		s.diagMeilisearch(ctx),
		s.diagMeilisearchIndexes(ctx),
		s.diagEmbed(ctx),
		s.diagInference(ctx),
		s.diagMCPServer(ctx),
	}

	allOK := true
	for _, c := range checks {
		if c.Status != "ok" && c.Status != "info" {
			allOK = false
			break
		}
	}

	status := http.StatusOK
	if !allOK {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, map[string]any{
		"overall": map[string]bool{"ok": allOK},
		"checks":  checks,
	})
}

func (s *Server) diagSelf() diagCheck {
	return diagCheck{Name: "ingest_api", Status: "ok", Message: "self-check passed.", LatencyMs: 0}
}

func (s *Server) diagQueryPlanner() diagCheck {
	mode := "rule-based fallback"
	if s.queryPlanner.IsLLMBacked() {
		mode = "LLM-backed (ChainOfThought)"
	}
	return diagCheck{Name: "query_planner", Status: "info", Message: mode + ".", LatencyMs: 0}
}

func (s *Server) diagMeilisearch(ctx context.Context) diagCheck {
	start := time.Now()
	lat := func() int64 { return time.Since(start).Milliseconds() }
	if s.meiliClient == nil {
		return diagCheck{Name: "meilisearch", Status: diagStatusError, Message: "client not configured", LatencyMs: lat()}
	}
	if err := s.meiliClient.Health(ctx); err != nil {
		return diagCheck{Name: "meilisearch", Status: diagStatusError, Message: err.Error(), LatencyMs: lat()}
	}
	return diagCheck{Name: "meilisearch", Status: "ok", Message: "available.", LatencyMs: lat()}
}

func (s *Server) diagMeilisearchIndexes(ctx context.Context) diagCheck {
	start := time.Now()
	lat := time.Since(start).Milliseconds()
	if s.meiliClient == nil {
		return diagCheck{Name: "meilisearch_indexes", Status: diagStatusError, Message: "client not configured", LatencyMs: lat}
	}
	uids, err := s.meiliClient.ListIndexUIDs(ctx, 100)
	if err != nil {
		return diagCheck{Name: "meilisearch_indexes", Status: diagStatusError, Message: err.Error(), LatencyMs: lat}
	}
	existing := map[string]struct{}{}
	for _, uid := range uids {
		existing[uid] = struct{}{}
	}
	projects := s.manager.ListProjects()
	var missing []string
	for _, p := range projects {
		if p.IndexName == "" {
			continue
		}
		if _, ok := existing[p.IndexName]; !ok {
			missing = append(missing, p.IndexName)
		}
	}
	if len(missing) > 0 {
		return diagCheck{Name: "meilisearch_indexes", Status: "warn", Message: fmt.Sprintf("missing indexes: %s", strings.Join(missing, ", ")), LatencyMs: lat}
	}
	return diagCheck{Name: "meilisearch_indexes", Status: "ok", Message: fmt.Sprintf("%d project index(es) found.", len(projects)), LatencyMs: lat}
}

func (s *Server) diagEmbed(ctx context.Context) diagCheck {
	embedURL := strings.TrimRight(strings.TrimSpace(os.Getenv("EMBED_URL")), "/")
	embedModel := strings.TrimSpace(os.Getenv("EMBED_MODEL"))
	if embedURL == "" {
		return diagCheck{Name: "localai_embed", Status: diagStatusError, Message: "EMBED_URL not set.", LatencyMs: 0}
	}
	if embedModel == "" {
		embedModel = "bge-base-en-v1.5"
	}
	start := time.Now()
	if err := s.localAIClient.PingEmbed(ctx, embedURL, embedModel); err != nil {
		return diagCheck{Name: "localai_embed", Status: diagStatusError, Message: err.Error(), LatencyMs: time.Since(start).Milliseconds()}
	}
	return diagCheck{Name: "localai_embed", Status: "ok", Message: "embeddings endpoint reachable.", LatencyMs: time.Since(start).Milliseconds()}
}

func (s *Server) diagInference(ctx context.Context) diagCheck {
	baseURL, apiKey, model, err := resolveInferenceEnv()
	if err != nil {
		return diagCheck{Name: diagNameInference, Status: diagStatusError, Message: err.Error(), LatencyMs: 0}
	}
	_, maxTokens := resolveInferenceOptions(nil, nil, model, s.chatModels)
	payloadMap := map[string]any{
		"model":      model,
		"max_tokens": maxTokens,
		"messages": []map[string]string{
			{"role": "user", "content": "Reply with just the word ok."},
		},
	}
	start := time.Now()
	infClient := localai.NewWithConfig(baseURL, apiKey, s.localAIClient.HTTPClient())
	status, body, err := infClient.ChatCompletions(ctx, payloadMap)
	lat := time.Since(start).Milliseconds()
	if err != nil {
		return diagCheck{Name: diagNameInference, Status: diagStatusError, Message: err.Error(), LatencyMs: lat}
	}
	var result struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				Reasoning string `json:"reasoning"`
			} `json:"message"`
		} `json:"choices"`
	}
	if status >= 400 || json.Unmarshal(body, &result) != nil || len(result.Choices) == 0 {
		return diagCheck{Name: diagNameInference, Status: diagStatusError, Message: fmt.Sprintf("invalid inference response (status %d).", status), LatencyMs: lat}
	}
	content := strings.TrimSpace(result.Choices[0].Message.Content)
	reasoning := strings.TrimSpace(result.Choices[0].Message.Reasoning)
	if content == "" {
		if reasoning != "" {
			return diagCheck{Name: diagNameInference, Status: "info", Message: "reasoning-only response received.", LatencyMs: lat}
		}
		return diagCheck{Name: diagNameInference, Status: diagStatusError, Message: "empty content in response.", LatencyMs: lat}
	}
	return diagCheck{Name: diagNameInference, Status: "ok", Message: fmt.Sprintf("model responded: %q", content), LatencyMs: lat}
}

func (s *Server) diagMCPServer(ctx context.Context) diagCheck {
	if ctx == nil {
		return diagCheck{Name: diagNameMCP, Status: diagStatusError, Message: "context is required", LatencyMs: 0}
	}
	if _, ok := ctx.Deadline(); !ok {
		return diagCheck{Name: diagNameMCP, Status: diagStatusError, Message: "context deadline is required", LatencyMs: 0}
	}
	mcpPort := getenvInt("MCP_PORT", 3000)
	url := fmt.Sprintf("http://localhost:%d/health", mcpPort)
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return diagCheck{Name: diagNameMCP, Status: diagStatusError, Message: fmt.Sprintf("build request: %v", err), LatencyMs: 0}
	}
	client := s.httpClient
	if client == nil {
		client = &http.Client{}
	}
	resp, err := httpfailsafe.Do(ctx, client, req, "mcp")
	lat := time.Since(start).Milliseconds()
	if err != nil {
		return diagCheck{Name: diagNameMCP, Status: diagStatusError, Message: err.Error(), LatencyMs: lat}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return diagCheck{Name: diagNameMCP, Status: diagStatusError, Message: fmt.Sprintf("unexpected status %d.", resp.StatusCode), LatencyMs: lat}
	}
	return diagCheck{Name: diagNameMCP, Status: "ok", Message: "healthy.", LatencyMs: lat}
}

func (s *Server) handleProjects(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.manager.ListProjects())
}

func (s *Server) handleReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	projectsCfg, err := registry.LoadMany(s.registryPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to load projects config: %v", err), http.StatusInternalServerError)
		return
	}

	defaults, personas, models, _, err := loadChatSettings(s.chatPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to load chat settings: %v", err), http.StatusInternalServerError)
		return
	}

	s.manager.Reload(projectsCfg)
	s.chatDefaults = defaults
	s.chatPersonas = personas
	s.chatModels = models
	writeJSON(w, http.StatusOK, map[string]string{"status": "reloaded"})
}

func (s *Server) handleIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ingestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	mode := jobs.Mode(strings.ToLower(strings.TrimSpace(req.Mode)))
	if mode == "" {
		mode = jobs.ModeIncremental
	}

	if !jobs.IsValidMode(mode) {
		http.Error(w, "mode must be one of full|incremental|watch", http.StatusBadRequest)
		return
	}

	job, err := s.manager.Enqueue(r.Context(), jobs.EnqueueInput{
		ProjectID:  strings.TrimSpace(req.ProjectID),
		Mode:       mode,
		TTLMinutes: req.TTLMinutes,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) handleCancelIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req cancelIngestRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
	}

	projectID := strings.TrimSpace(req.ProjectID)
	projects := s.manager.ListProjects()
	targets := make([]registry.Project, 0, len(projects))

	if projectID != "" {
		found := false
		for _, p := range projects {
			if p.ID == projectID {
				targets = append(targets, p)
				found = true
				break
			}
		}
		if !found {
			http.Error(w, fmt.Sprintf("project %q not found or disabled", projectID), http.StatusBadRequest)
			return
		}
	} else {
		targets = projects
	}

	type item struct {
		ProjectID string `json:"project_id"`
		IndexName string `json:"index_name"`
		Status    string `json:"status"`
		Error     string `json:"error,omitempty"`
	}

	results := make([]item, 0, len(targets))
	failed := 0
	for _, p := range targets {
		if strings.TrimSpace(p.IndexName) == "" {
			results = append(results, item{ProjectID: p.ID, IndexName: p.IndexName, Status: "skipped", Error: "index_name is empty"})
			continue
		}
		if err := s.meiliClient.CancelIndexTasks(r.Context(), p.IndexName); err != nil {
			failed++
			results = append(results, item{ProjectID: p.ID, IndexName: p.IndexName, Status: "failed", Error: err.Error()})
			continue
		}
		results = append(results, item{ProjectID: p.ID, IndexName: p.IndexName, Status: "canceled"})
	}

	statusCode := http.StatusOK
	if failed > 0 {
		statusCode = http.StatusBadGateway
	}

	writeJSON(w, statusCode, map[string]any{
		"project_id": projectID,
		"total":      len(targets),
		"failed":     failed,
		"results":    results,
	})
}

func (s *Server) handleDropIndexes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req cancelIngestRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
	}

	projectID := strings.TrimSpace(req.ProjectID)
	projects := s.manager.ListProjects()
	targets := make([]registry.Project, 0, len(projects))

	if projectID != "" {
		found := false
		for _, p := range projects {
			if p.ID == projectID {
				targets = append(targets, p)
				found = true
				break
			}
		}
		if !found {
			http.Error(w, fmt.Sprintf("project %q not found or disabled", projectID), http.StatusBadRequest)
			return
		}
	} else {
		targets = projects
	}

	// Respond immediately — the HTTP server's write timeout is too short to wait for
	// DeleteAllDocuments, which blocks while Meilisearch queues the delete task behind
	// any currently-processing embed tasks. The actual drop runs in the background.
	writeJSON(w, http.StatusAccepted, map[string]any{
		"status":     "drop_initiated",
		"project_id": projectID,
		"total":      len(targets),
	})

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		for _, p := range targets {
			if strings.TrimSpace(p.IndexName) == "" {
				continue
			}
			// Cancel any stuck embed tasks, then wait briefly for the cancellation to take
			// effect before issuing the delete so it is not queued behind them.
			if err := s.meiliClient.CancelIndexTasks(ctx, p.IndexName); err != nil {
				s.logger.Warnf("ingest-drop: cancel tasks for index %q: %v", p.IndexName, err)
			}
			time.Sleep(2 * time.Second)
			if err := s.meiliClient.DeleteAllDocuments(ctx, p.IndexName); err != nil {
				s.logger.Warnf("ingest-drop: failed to delete documents for index %q: %v", p.IndexName, err)
				continue
			}
			s.logger.Infof("ingest-drop: dropped index %q", p.IndexName)
		}
	}()
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/jobs/")
	if id == "" {
		http.Error(w, "job id is required", http.StatusBadRequest)
		return
	}

	job, ok := s.manager.GetJob(id)
	if !ok {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}

	writeJSON(w, http.StatusOK, job)
}

func (s *Server) handleQuerySearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.inferenceTimeout)
	defer cancel()

	var req querySearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	query := strings.TrimSpace(req.Query)
	if query == "" {
		http.Error(w, "query is required", http.StatusBadRequest)
		return
	}

	topK, semanticRatio := normalizeQueryParams(req.TopK, req.SemanticRatio)

	routedIndexes, err := s.routeIndexes(query, req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	chatCfg := s.resolveChatConfig(routedIndexes)

	merged := make([]map[string]any, 0, topK*len(routedIndexes))
	for _, indexName := range routedIndexes {
		hits, searchErr := s.searchIndex(ctx, indexName, query, topK, semanticRatio)
		if searchErr != nil {
			http.Error(w, searchErr.Error(), http.StatusBadGateway)
			return
		}

		for _, hit := range hits {
			hit["_index_name"] = indexName
			merged = append(merged, hit)
		}
	}

	if len(merged) > topK {
		merged = merged[:topK]
	}

	writeJSON(w, http.StatusOK, querySearchResponse{
		Query:         query,
		RoutedIndexes: routedIndexes,
		Chat:          chatCfg,
		Hits:          merged,
	})
}

func (s *Server) handleQueryPlan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.inferenceTimeout)
	defer cancel()

	var req queryPlanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	question := strings.TrimSpace(req.Question)
	sessionID := strings.TrimSpace(req.SessionID)
	if question == "" {
		question = strings.TrimSpace(req.Query)
	}
	if question == "" {
		http.Error(w, "question is required", http.StatusBadRequest)
		return
	}

	topK, semanticRatio := normalizeQueryParams(req.TopK, req.SemanticRatio)

	plan := s.queryPlanner.Build(ctx, question)
	if len(plan.Queries) == 0 {
		http.Error(w, "unable to build query plan", http.StatusBadRequest)
		return
	}
	reuseSource := "none"
	prior, source, ok, err := s.findSimilarSessionPlan(sessionID, question)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if ok {
		plan.Queries = mergePlanQueries(plan.Queries, prior.QueryPlan)
		reuseSource = source
	}

	routedIndexes, err := s.routeIndexes(plan.Primary, querySearchRequest{
		Query:     question,
		ProjectID: req.ProjectID,
		IndexName: req.IndexName,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	merged, err := s.gatherDeduplicatedHits(ctx, routedIndexes, plan.Queries, topK, semanticRatio)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if len(merged) > topK {
		merged = merged[:topK]
	}

	planID := s.storePlannedQuery(plannedQuery{
		SessionID:        sessionID,
		Question:         question,
		RoutedIndexes:    append([]string(nil), routedIndexes...),
		QueryPlan:        append([]string(nil), plan.Queries...),
		CandidateSources: copyHits(merged),
		TopK:             topK,
		SemanticRatio:    semanticRatio,
		CreatedAt:        s.nowTime(),
	})

	writeJSON(w, http.StatusOK, queryPlanResponse{
		Question:         question,
		PlanID:           planID,
		ReuseSource:      reuseSource,
		RoutedIndexes:    routedIndexes,
		AvailableIndexes: s.describeAvailableIndexes(),
		QueryPlan:        plan.Queries,
		CandidateSources: merged,
	})
}

func (s *Server) handleQueryAsk(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.inferenceTimeout)
	defer cancel()

	var req queryAskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	question := strings.TrimSpace(req.Question)
	sessionID := strings.TrimSpace(req.SessionID)
	if question == "" {
		question = strings.TrimSpace(req.Query)
	}
	if question == "" {
		http.Error(w, "question is required", http.StatusBadRequest)
		return
	}
	planID := strings.TrimSpace(req.PlanID)

	topK, semanticRatio := normalizeQueryParams(req.TopK, req.SemanticRatio)

	var (
		routedIndexes []string
		queries       []string
		merged        []map[string]any
		err           error
	)

	if planID != "" {
		planned, ok := s.peekPlannedQuery(planID)
		if !ok {
			http.Error(w, "plan_id not found or expired", http.StatusBadRequest)
			return
		}
		if !semanticallyMatchesQuestion(question, planned.Question) {
			http.Error(w, "question does not match confirmed plan_id", http.StatusBadRequest)
			return
		}
		if sessionID != "" && planned.SessionID != "" && sessionID != planned.SessionID {
			http.Error(w, "session_id does not match confirmed plan_id", http.StatusBadRequest)
			return
		}
		if !s.consumePlannedQuery(planID) {
			http.Error(w, "plan_id not found or expired", http.StatusBadRequest)
			return
		}
		question = planned.Question
		if sessionID == "" {
			sessionID = planned.SessionID
		}
		topK = planned.TopK
		semanticRatio = planned.SemanticRatio
		routedIndexes = append([]string(nil), planned.RoutedIndexes...)
		queries = append([]string(nil), planned.QueryPlan...)
		merged = copyHits(planned.CandidateSources)
	} else {
		plan := s.queryPlanner.Build(ctx, question)
		if len(plan.Queries) == 0 {
			http.Error(w, "unable to build query plan", http.StatusBadRequest)
			return
		}

		routedIndexes, err = s.routeIndexes(plan.Primary, querySearchRequest{
			Query:     question,
			ProjectID: req.ProjectID,
			IndexName: req.IndexName,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		queries = append([]string(nil), plan.Queries...)
		merged, err = s.gatherDeduplicatedHits(ctx, routedIndexes, queries, topK, semanticRatio)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		if len(merged) > topK {
			merged = merged[:topK]
		}
	}

	chatCfg := s.resolveChatConfig(routedIndexes)
	resolvedTemperature := req.Temperature
	if resolvedTemperature == nil {
		resolvedTemperature = chatCfg.Temperature
	}
	resolvedMaxTokens := req.MaxTokens
	if resolvedMaxTokens == nil {
		resolvedMaxTokens = chatCfg.MaxTokens
	}

	baseURL, apiKey, model, err := resolveInferenceEnv()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if strings.TrimSpace(chatCfg.Model) != "" {
		model = strings.TrimSpace(chatCfg.Model)
	}

	temperature, maxTokens := resolveInferenceOptions(resolvedTemperature, resolvedMaxTokens, model, s.chatModels)

	answer, inferErr := s.inferAnswer(
		ctx,
		question,
		merged,
		model,
		baseURL,
		apiKey,
		temperature,
		maxTokens,
	)
	if inferErr != nil {
		http.Error(w, inferErr.Error(), http.StatusBadGateway)
		return
	}
	if err := s.recordSuccessfulSessionPlan(sessionID, plannedQuery{
		SessionID:        sessionID,
		Question:         question,
		RoutedIndexes:    append([]string(nil), routedIndexes...),
		QueryPlan:        append([]string(nil), queries...),
		CandidateSources: copyHits(merged),
		TopK:             topK,
		SemanticRatio:    semanticRatio,
		CreatedAt:        s.nowTime(),
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, queryAskResponse{
		Question:      question,
		PlanID:        planID,
		RoutedIndexes: routedIndexes,
		QueryPlan:     queries,
		Answer:        answer,
		Sources:       merged,
		Model:         model,
	})
}

func copyHits(hits []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(hits))
	for _, hit := range hits {
		copied := make(map[string]any, len(hit))
		for k, v := range hit {
			copied[k] = v
		}
		out = append(out, copied)
	}
	return out
}

func (s *Server) describeAvailableIndexes() []availableIndex {
	projects := s.manager.ListProjects()
	out := make([]availableIndex, 0, len(projects))
	for _, project := range projects {
		desc := strings.TrimSpace(project.Chat.Prompts.SearchDescription)
		if desc == "" {
			desc = fmt.Sprintf("Project %s documentation index", strings.TrimSpace(project.ID))
		}
		out = append(out, availableIndex{
			ProjectID:   strings.TrimSpace(project.ID),
			IndexName:   strings.TrimSpace(project.IndexName),
			Description: desc,
			Topics:      topicHints(project),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ProjectID < out[j].ProjectID
	})
	return out
}

func topicHints(project registry.Project) []string {
	seen := make(map[string]struct{}, len(project.Rules)+2)
	add := func(value string) {
		v := strings.TrimSpace(strings.ToLower(value))
		if v == "" {
			return
		}
		if _, exists := seen[v]; exists {
			return
		}
		seen[v] = struct{}{}
	}

	for _, rule := range project.Rules {
		add(rule.DocType)
		prefix := strings.Trim(rule.MatchPathPrefix, "/")
		if prefix != "" {
			add(prefix)
		}
	}
	add(project.Default.DocType)

	topics := make([]string, 0, len(seen))
	for topic := range seen {
		topics = append(topics, topic)
	}
	sort.Strings(topics)
	if len(topics) > 8 {
		topics = topics[:8]
	}
	return topics
}

func mergePlanQueries(primary, prior []string) []string {
	merged := make([]string, 0, len(primary)+len(prior))
	seen := map[string]struct{}{}
	appendUnique := func(items []string) {
		for _, q := range items {
			query := strings.TrimSpace(q)
			if query == "" {
				continue
			}
			key := strings.ToLower(query)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			merged = append(merged, query)
			if len(merged) >= 6 {
				return
			}
		}
	}
	appendUnique(primary)
	appendUnique(prior)
	return merged
}

func semanticallyMatchesQuestion(userQuestion, plannedQuestion string) bool {
	left := tokenizeQuestion(userQuestion)
	right := tokenizeQuestion(plannedQuestion)
	if len(left) == 0 || len(right) == 0 {
		return strings.EqualFold(strings.TrimSpace(userQuestion), strings.TrimSpace(plannedQuestion))
	}
	leftSet := make(map[string]struct{}, len(left))
	for _, token := range left {
		leftSet[token] = struct{}{}
	}
	intersect := 0
	union := len(leftSet)
	seenRight := map[string]struct{}{}
	for _, token := range right {
		if _, seen := seenRight[token]; seen {
			continue
		}
		seenRight[token] = struct{}{}
		if _, ok := leftSet[token]; ok {
			intersect++
		} else {
			union++
		}
	}
	if union == 0 {
		return false
	}
	score := float64(intersect) / float64(union)
	return score >= 0.45
}

func tokenizeQuestion(value string) []string {
	raw := strings.FieldsFunc(strings.ToLower(strings.TrimSpace(value)), func(r rune) bool {
		return !(r == '_' || r == '-' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
	})
	out := make([]string, 0, len(raw))
	for _, token := range raw {
		if len(token) < 3 {
			continue
		}
		out = append(out, token)
	}
	return out
}

func (s *Server) findSimilarSessionPlan(sessionID, question string) (plannedQuery, string, bool, error) {
	if s.sessionStore != nil {
		stored, ok, err := s.sessionStore.FindSimilar(sessionID, question, semanticallyMatchesQuestion)
		if err != nil {
			return plannedQuery{}, "none", false, derrors.Wrap(err, derrors.CodeUnavailable, "ingestapi.findSimilarSessionPlan", "similar lookup")
		}
		if ok {
			return plannedQuery{
				SessionID:     stored.SessionID,
				Question:      stored.Question,
				RoutedIndexes: stored.RoutedIndexes,
				QueryPlan:     stored.QueryPlan,
				TopK:          stored.TopK,
				SemanticRatio: stored.SemanticRatio,
				CreatedAt:     stored.CreatedAt,
			}, "sqlite", true, nil
		}
	}

	if strings.TrimSpace(sessionID) == "" {
		return plannedQuery{}, "none", false, nil
	}
	s.planMu.Lock()
	defer s.planMu.Unlock()

	now := s.nowTime()
	s.pruneExpiredPlansLocked(now)
	plans := s.sessionPlans[sessionID]
	for i := len(plans) - 1; i >= 0; i-- {
		candidate := plans[i]
		if semanticallyMatchesQuestion(question, candidate.Question) {
			return candidate, "memory", true, nil
		}
	}
	return plannedQuery{}, "none", false, nil
}

func (s *Server) recordSuccessfulSessionPlan(sessionID string, plan plannedQuery) error {
	if strings.TrimSpace(sessionID) == "" {
		return nil
	}

	if s.sessionStore != nil {
		if err := s.sessionStore.RecordSuccessful(sessionstore.PlannedQuery{
			SessionID:     plan.SessionID,
			Question:      plan.Question,
			RoutedIndexes: plan.RoutedIndexes,
			QueryPlan:     plan.QueryPlan,
			TopK:          plan.TopK,
			SemanticRatio: plan.SemanticRatio,
			CreatedAt:     plan.CreatedAt,
		}); err != nil {
			return derrors.Wrap(err, derrors.CodeUnavailable, "ingestapi.recordSuccessfulSessionPlan", "persist successful plan")
		}
	}

	s.planMu.Lock()
	defer s.planMu.Unlock()

	now := s.nowTime()
	s.pruneExpiredPlansLocked(now)
	plans := append(s.sessionPlans[sessionID], plan)
	if len(plans) > 10 {
		plans = plans[len(plans)-10:]
	}
	s.sessionPlans[sessionID] = plans
	return nil
}

func (s *Server) storePlannedQuery(plan plannedQuery) string {
	s.planMu.Lock()
	defer s.planMu.Unlock()

	now := s.nowTime()
	s.pruneExpiredPlansLocked(now)

	if plan.CreatedAt.IsZero() {
		plan.CreatedAt = now
	}
	planID := newPlanID(now)
	s.plannedQueries[planID] = plan
	return planID
}

func (s *Server) peekPlannedQuery(planID string) (plannedQuery, bool) {
	s.planMu.Lock()
	defer s.planMu.Unlock()

	now := s.nowTime()
	s.pruneExpiredPlansLocked(now)

	plan, ok := s.plannedQueries[planID]
	if !ok {
		return plannedQuery{}, false
	}
	return plan, true
}

func (s *Server) consumePlannedQuery(planID string) bool {
	s.planMu.Lock()
	defer s.planMu.Unlock()

	now := s.nowTime()
	s.pruneExpiredPlansLocked(now)

	if _, ok := s.plannedQueries[planID]; !ok {
		return false
	}
	delete(s.plannedQueries, planID)
	return true
}

func (s *Server) pruneExpiredPlansLocked(now time.Time) {
	if s.planTTL <= 0 {
		s.planTTL = 10 * time.Minute
	}
	for id, plan := range s.plannedQueries {
		if now.Sub(plan.CreatedAt) > s.planTTL {
			delete(s.plannedQueries, id)
		}
	}
	for sessionID, plans := range s.sessionPlans {
		kept := plans[:0]
		for _, plan := range plans {
			if now.Sub(plan.CreatedAt) <= s.planTTL {
				kept = append(kept, plan)
			}
		}
		if len(kept) == 0 {
			delete(s.sessionPlans, sessionID)
			continue
		}
		s.sessionPlans[sessionID] = kept
	}
}

func newPlanID(now time.Time) string {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Sprintf("plan_%d", now.UnixNano())
	}
	return fmt.Sprintf("plan_%d_%s", now.UnixNano(), hex.EncodeToString(raw))
}

func (s *Server) gatherDeduplicatedHits(ctx context.Context, routedIndexes, queries []string, topK int, semanticRatio float64) ([]map[string]any, error) {
	merged := make([]map[string]any, 0, topK*len(routedIndexes)*len(queries))
	seen := make(map[string]struct{}, topK*4)
	for _, indexName := range routedIndexes {
		for _, q := range queries {
			hits, err := s.searchIndex(ctx, indexName, q, topK, semanticRatio)
			if err != nil {
				return nil, fmt.Errorf("search index %q: %w", indexName, err)
			}
			for _, hit := range hits {
				hit["_index_name"] = indexName
				hit["_query"] = q
				key := fmt.Sprintf("%v|%v|%v", hit["path"], hit["section_heading"], hit["snippet"])
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				merged = append(merged, hit)
			}
		}
	}
	return merged, nil
}

func resolveInferenceEnv() (baseURL, apiKey, model string, err error) {
	// Prefer LOCALAI_BASE_URL / LOCALAI_CHAT_MODEL (set by docker compose defaults);
	// fall back to generic INFERENCE_OPENAI_* for external providers.
	baseURL = strings.TrimSpace(os.Getenv("LOCALAI_BASE_URL"))
	if baseURL == "" {
		baseURL = strings.TrimSpace(os.Getenv("INFERENCE_OPENAI_BASE_URL"))
	}
	if baseURL == "" {
		return "", "", "", fmt.Errorf("LOCALAI_BASE_URL (or INFERENCE_OPENAI_BASE_URL) is required")
	}
	apiKey = strings.TrimSpace(os.Getenv("INFERENCE_OPENAI_API_KEY"))
	model = strings.TrimSpace(os.Getenv("LOCALAI_CHAT_MODEL"))
	if model == "" {
		model = strings.TrimSpace(os.Getenv("INFERENCE_OPENAI_MODEL"))
	}
	if model == "" {
		return "", "", "", fmt.Errorf("LOCALAI_CHAT_MODEL (or INFERENCE_OPENAI_MODEL) is required")
	}
	return baseURL, apiKey, model, nil
}

func resolveInferenceOptions(
	temperature *float64,
	maxTokens *int,
	model string,
	modelSettings map[string]chatModelSettings,
) (float64, int) {
	modelDefaults := resolveModelDefaults(model, modelSettings)
	t := 0.2
	if modelDefaults.Temperature != nil {
		t = *modelDefaults.Temperature
	}
	if temperature != nil {
		t = *temperature
	}
	m := 512
	if modelDefaults.MaxTokens != nil && *modelDefaults.MaxTokens > 0 {
		m = *modelDefaults.MaxTokens
	}
	if maxTokens != nil && *maxTokens > 0 {
		m = *maxTokens
	}
	return t, m
}

func resolveModelDefaults(model string, modelSettings map[string]chatModelSettings) chatModelSettings {
	if len(modelSettings) == 0 {
		return chatModelSettings{}
	}
	key := strings.TrimSpace(model)
	if key == "" {
		return chatModelSettings{}
	}
	if settings, ok := modelSettings[key]; ok {
		return settings
	}
	return chatModelSettings{}
}

func normalizeQueryParams(topK int, semanticRatio float64) (int, float64) {
	if topK <= 0 {
		topK = 5
	}
	if topK > 50 {
		topK = 50
	}
	if semanticRatio <= 0 || semanticRatio > 1 {
		semanticRatio = 0.5
	}
	return topK, semanticRatio
}

func (s *Server) resolveChatConfig(routedIndexes []string) queryChatConfig {
	if len(routedIndexes) == 0 {
		return queryChatConfig{}
	}

	primaryIndex := routedIndexes[0]
	for _, project := range s.manager.ListProjects() {
		if project.IndexName != primaryIndex {
			continue
		}

		resolved := s.chatDefaults
		personaStyle := ""
		persona := strings.TrimSpace(project.Chat.Persona)
		if persona != "" {
			profile, ok := s.chatPersonas[persona]
			if !ok {
				s.logger.Warnf("chat persona %q not found for project %q", persona, project.ID)
			} else {
				personaStyle = strings.TrimSpace(profile.Style)
				if strings.TrimSpace(profile.Model) != "" {
					resolved.Model = strings.TrimSpace(profile.Model)
				}
				if profile.Temperature != nil {
					resolved.Temperature = profile.Temperature
				}
				if profile.MaxTokens != nil {
					resolved.MaxTokens = profile.MaxTokens
				}
			}
		}

		projectConfig := queryChatConfig{
			Workspace: strings.TrimSpace(project.Chat.Workspace),
			Source:    strings.TrimSpace(project.Chat.Source),
			BaseURL:   strings.TrimSpace(project.Chat.BaseURL),
			APIKey:    strings.TrimSpace(project.Chat.APIKey),
			Model:     strings.TrimSpace(project.Chat.Model),
			Prompts: queryChatPrompts{
				System:              strings.TrimSpace(project.Chat.Prompts.System),
				SearchDescription:   strings.TrimSpace(project.Chat.Prompts.SearchDescription),
				SearchQParam:        strings.TrimSpace(project.Chat.Prompts.SearchQParam),
				SearchFilterParam:   strings.TrimSpace(project.Chat.Prompts.SearchFilterParam),
				SearchIndexUIDParam: strings.TrimSpace(project.Chat.Prompts.SearchIndexUIDParam),
			},
		}

		resolved = mergeChatConfig(resolved, projectConfig)
		resolved.Prompts.System = composeSystemPrompt(personaStyle, resolved.Prompts.System)
		return resolved
	}

	return queryChatConfig{}
}

func detectChatSettingsPath() string {
	return observability.DetectOpenInferenceConfigPath()
}

func loadChatSettings(path string) (queryChatConfig, map[string]chatPersonaSettings, map[string]chatModelSettings, observability.OpenInferenceConfig, error) {
	if strings.TrimSpace(path) == "" {
		return queryChatConfig{}, nil, nil, observability.OpenInferenceConfig{}, fmt.Errorf("chat settings file path is required")
	}

	raw, err := os.ReadFile(filepath.Clean(path)) //nolint:gosec // operator-configured path
	if err != nil {
		return queryChatConfig{}, nil, nil, observability.OpenInferenceConfig{}, fmt.Errorf("read chat settings file: %w", err)
	}

	var settings chatSettings
	if err := yaml.Unmarshal(raw, &settings); err != nil {
		return queryChatConfig{}, nil, nil, observability.OpenInferenceConfig{}, fmt.Errorf("parse chat settings file: %w", err)
	}

	personas := make(map[string]chatPersonaSettings, len(settings.Personas))
	for key, value := range settings.Personas {
		name := strings.TrimSpace(key)
		if name == "" {
			continue
		}
		personas[name] = chatPersonaSettings{
			Style:       strings.TrimSpace(value.Style),
			Model:       strings.TrimSpace(value.Model),
			Temperature: value.Temperature,
			MaxTokens:   value.MaxTokens,
		}
	}

	models := make(map[string]chatModelSettings, len(settings.Models))
	for key, value := range settings.Models {
		name := strings.TrimSpace(key)
		if name == "" {
			continue
		}
		models[name] = chatModelSettings{
			Temperature: value.Temperature,
			MaxTokens:   value.MaxTokens,
		}
	}

	return trimChatConfig(settings.Defaults), personas, models, settings.OpenInference, nil
}

func composeSystemPrompt(personaStyle, systemInstructions string) string {
	style := strings.TrimSpace(personaStyle)
	instructions := strings.TrimSpace(systemInstructions)

	if style == "" {
		return instructions
	}
	if instructions == "" {
		return style
	}

	return fmt.Sprintf("Persona style:\n%s\n\nSystem instructions:\n%s", style, instructions)
}

func trimChatConfig(in queryChatConfig) queryChatConfig {
	return queryChatConfig{
		Workspace:   strings.TrimSpace(in.Workspace),
		Source:      strings.TrimSpace(in.Source),
		BaseURL:     strings.TrimSpace(in.BaseURL),
		APIKey:      strings.TrimSpace(in.APIKey),
		Model:       strings.TrimSpace(in.Model),
		Temperature: in.Temperature,
		MaxTokens:   in.MaxTokens,
		Prompts: queryChatPrompts{
			System:              strings.TrimSpace(in.Prompts.System),
			SearchDescription:   strings.TrimSpace(in.Prompts.SearchDescription),
			SearchQParam:        strings.TrimSpace(in.Prompts.SearchQParam),
			SearchFilterParam:   strings.TrimSpace(in.Prompts.SearchFilterParam),
			SearchIndexUIDParam: strings.TrimSpace(in.Prompts.SearchIndexUIDParam),
		},
	}
}

func mergeChatConfig(base, override queryChatConfig) queryChatConfig {
	merged := base

	if strings.TrimSpace(override.Workspace) != "" {
		merged.Workspace = strings.TrimSpace(override.Workspace)
	}
	if strings.TrimSpace(override.Source) != "" {
		merged.Source = strings.TrimSpace(override.Source)
	}
	if strings.TrimSpace(override.BaseURL) != "" {
		merged.BaseURL = strings.TrimSpace(override.BaseURL)
	}
	if strings.TrimSpace(override.APIKey) != "" {
		merged.APIKey = strings.TrimSpace(override.APIKey)
	}
	if strings.TrimSpace(override.Model) != "" {
		merged.Model = strings.TrimSpace(override.Model)
	}
	if override.Temperature != nil {
		merged.Temperature = override.Temperature
	}
	if override.MaxTokens != nil {
		merged.MaxTokens = override.MaxTokens
	}

	if strings.TrimSpace(override.Prompts.System) != "" {
		merged.Prompts.System = strings.TrimSpace(override.Prompts.System)
	}
	if strings.TrimSpace(override.Prompts.SearchDescription) != "" {
		merged.Prompts.SearchDescription = strings.TrimSpace(override.Prompts.SearchDescription)
	}
	if strings.TrimSpace(override.Prompts.SearchQParam) != "" {
		merged.Prompts.SearchQParam = strings.TrimSpace(override.Prompts.SearchQParam)
	}
	if strings.TrimSpace(override.Prompts.SearchFilterParam) != "" {
		merged.Prompts.SearchFilterParam = strings.TrimSpace(override.Prompts.SearchFilterParam)
	}
	if strings.TrimSpace(override.Prompts.SearchIndexUIDParam) != "" {
		merged.Prompts.SearchIndexUIDParam = strings.TrimSpace(override.Prompts.SearchIndexUIDParam)
	}

	return merged
}

type indexCandidate struct {
	index string
	score int
}

func scoreProjectAgainstQuery(project registry.Project, queryLower string, tokens []string) int {
	haystack := strings.ToLower(strings.Join([]string{
		project.ID,
		project.IndexName,
		project.ScanPath(),
	}, " "))

	score := 0
	if strings.Contains(queryLower, strings.ToLower(project.ID)) {
		score += 3
	}
	if strings.Contains(queryLower, strings.ToLower(project.IndexName)) {
		score += 2
	}
	for _, token := range tokens {
		if len(token) < 3 {
			continue
		}
		if strings.Contains(haystack, token) {
			score++
		}
	}
	return score
}

func pickTopCandidates(candidates []indexCandidate, bestScore int) []string {
	routed := make([]string, 0, 2)
	for _, c := range candidates {
		if c.score == bestScore {
			routed = append(routed, c.index)
			if len(routed) == 2 {
				break
			}
		}
	}
	return routed
}

func (s *Server) routeIndexes(query string, req querySearchRequest) ([]string, error) {
	projects := s.manager.ListProjects()
	if len(projects) == 0 {
		return nil, fmt.Errorf("no enabled projects available for routing")
	}

	if indexName := strings.TrimSpace(req.IndexName); indexName != "" {
		return []string{indexName}, nil
	}

	if projectID := strings.TrimSpace(req.ProjectID); projectID != "" {
		for _, project := range projects {
			if project.ID == projectID {
				return []string{project.IndexName}, nil
			}
		}
		return nil, fmt.Errorf("project %q not found or disabled", projectID)
	}

	return s.routeByQueryScore(projects, query)
}

func (s *Server) routeByQueryScore(projects []registry.Project, query string) ([]string, error) {
	queryLower := strings.ToLower(query)
	tokens := strings.FieldsFunc(queryLower, func(r rune) bool {
		return !(r == '_' || r == '-' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
	})

	bestScore := 0
	candidates := make([]indexCandidate, 0, len(projects))
	for _, project := range projects {
		score := scoreProjectAgainstQuery(project, queryLower, tokens)
		if score > bestScore {
			bestScore = score
		}
		candidates = append(candidates, indexCandidate{index: project.IndexName, score: score})
	}

	if bestScore <= 0 {
		if defaultIndex := strings.TrimSpace(os.Getenv("MEILI_INDEX")); defaultIndex != "" {
			return []string{defaultIndex}, nil
		}
		return []string{projects[0].IndexName}, nil
	}

	routed := pickTopCandidates(candidates, bestScore)
	if len(routed) == 0 {
		return nil, fmt.Errorf("failed to route query to an index")
	}
	return routed, nil
}

func (s *Server) searchIndex(ctx context.Context, indexName, query string, topK int, semanticRatio float64) ([]map[string]any, error) {
	payload := meili.SearchPayload{
		"q":     query,
		"limit": topK,
		"hybrid": map[string]any{
			"semanticRatio": semanticRatio,
			"embedder":      "default",
		},
	}

	hits, err := s.meiliClient.Search(ctx, indexName, payload)
	if err != nil {
		return nil, fmt.Errorf("search index %q: %w", indexName, err)
	}

	return hits, nil
}

func buildContextParts(hits []map[string]any) []string {
	contextParts := make([]string, 0, len(hits))
	for _, hit := range hits {
		heading := strings.TrimSpace(fmt.Sprintf("%v", hit["section_heading"]))
		if heading == "" || heading == "<nil>" {
			heading = strings.TrimSpace(fmt.Sprintf("%v", hit["title"]))
		}
		path := strings.TrimSpace(fmt.Sprintf("%v", hit["path"]))
		snippet := strings.TrimSpace(fmt.Sprintf("%v", hit["snippet"]))
		if snippet == "" || snippet == "<nil>" {
			snippet = strings.TrimSpace(fmt.Sprintf("%v", hit["content"]))
		}
		if len(snippet) > 1200 {
			snippet = snippet[:1200]
		}
		contextParts = append(contextParts, fmt.Sprintf("[%s] %s\n%s", path, heading, snippet))
	}
	return contextParts
}

func (s *Server) inferAnswer(
	ctx context.Context,
	question string,
	hits []map[string]any,
	model string,
	baseURL string,
	apiKey string,
	temperature float64,
	maxTokens int,
) (string, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return "", fmt.Errorf("inference base_url is required")
	}

	contextParts := buildContextParts(hits)

	systemPrompt := "Answer using only the provided context. If context is insufficient, say so clearly."

	messages := []map[string]any{
		{
			"role":    "system",
			"content": systemPrompt,
		},
		{
			"role": "user",
			"content": fmt.Sprintf(
				"Context:\n%s\n\nQuestion: %s",
				strings.Join(contextParts, "\n\n---\n\n"),
				question,
			),
		},
	}

	payload := localai.ChatRequest{
		"model":       model,
		"messages":    messages,
		"stream":      false,
		"temperature": temperature,
		"max_tokens":  maxTokens,
	}
	// context_size is a model-load-time setting for llama.cpp (n_ctx).
	// Forwarding it at the request level with a value larger than what the model
	// was loaded with causes the backend to return an empty response silently.
	// We intentionally omit it here and rely on the model config's context_size.

	// Construct a per-request client with the caller-supplied base URL and API key,
	// reusing the underlying HTTP transport from the injected client.
	c := localai.NewWithConfig(baseURL, apiKey, s.localAIClient.HTTPClient())

	statusCode, body, err := c.ChatCompletions(ctx, payload)
	if err != nil {
		return "", fmt.Errorf("execute inference request: %w", err)
	}

	if statusCode >= 400 {
		return "", fmt.Errorf("inference request failed: status %d: %s", statusCode, strings.TrimSpace(string(body)))
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				Reasoning string `json:"reasoning"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("parse inference response: %w", err)
	}

	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("inference response did not include choices")
	}

	content := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if content == "" {
		content = strings.TrimSpace(parsed.Choices[0].Message.Reasoning)
	}
	if content == "" {
		return "", fmt.Errorf("inference response was empty")
	}

	return content, nil
}

func writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		encodeLogger.Warnf("failed to encode JSON response: %v", err)
	}
}

func getenv(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func getenvInt(key string, fallback int) int {
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

func (s *Server) nowTime() time.Time {
	if s == nil || s.clock == nil {
		panic("ingestapi: clock is required")
	}
	return s.clock()
}
