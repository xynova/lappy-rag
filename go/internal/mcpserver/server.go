package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/xynova/lappy-rag/internal/clients/ingest"
	"github.com/xynova/lappy-rag/internal/clients/localai"
	"github.com/xynova/lappy-rag/internal/clients/meili"
	derrors "github.com/xynova/lappy-rag/internal/errors"
	"github.com/xynova/lappy-rag/internal/observability"
)

type app struct {
	meiliIndex           string
	traceServiceName     string
	localAIChatModel     string
	proxyDefaultMaxToken int
	proxyTimeout         time.Duration
	enabledTools         map[string]struct{}
	disabledTools        map[string]struct{}
	meiliClient          *meili.Client
	ingestClient         *ingest.Client
	localAIClient        *localai.Client
	proxyClient          *localai.Client
	logger               *observability.Logger
}

// Run starts the MCP HTTP server (blocking).
func Run() error {
	return Serve(context.Background())
}

// Serve runs the MCP server until shutdown.
func Serve(ctx context.Context) error {
	mcpPort := getenvInt("MCP_PORT", 3000)
	traceCfgPath := observability.DetectOpenInferenceConfigPath()
	traceCfg, err := observability.LoadOpenInferenceConfigFromFile(traceCfgPath, "mcp-server")
	if err != nil {
		return derrors.Wrap(err, derrors.CodeUnavailable, "mcpserver.Serve", "load openinference config")
	}
	traceServiceName := observability.ServiceNameFromConfig(&traceCfg)
	logger := observability.NewLogger("mcp-server")

	if traceCfg.Enabled {
		provider, err := observability.InitOTEL(traceServiceName, traceCfg.Endpoint)
		if err != nil {
			return derrors.Wrap(err, derrors.CodeUnavailable, "mcpserver.Serve", "initialize openinference tracer provider")
		}
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := observability.ShutdownOTEL(shutdownCtx, provider); err != nil {
				logger.Warnf("failed to shutdown openinference tracer provider: %v", err)
			}
		}()
	}

	meiliClient, err := meili.New()
	if err != nil {
		return derrors.Wrap(err, derrors.CodeUnavailable, "mcpserver.Serve", "initialize meilisearch client")
	}

	ingestClient, err := ingest.New()
	if err != nil {
		return derrors.Wrap(err, derrors.CodeUnavailable, "mcpserver.Serve", "initialize ingest client")
	}

	proxyTimeout := time.Duration(getenvInt("LOCALAI_PROXY_TIMEOUT_SECONDS", 180)) * time.Second

	localAIClient, err := localai.New()
	if err != nil {
		return derrors.Wrap(err, derrors.CodeUnavailable, "mcpserver.Serve", "initialize localai client")
	}

	proxyClient, err := localai.New()
	if err != nil {
		return derrors.Wrap(err, derrors.CodeUnavailable, "mcpserver.Serve", "initialize localai proxy client")
	}

	a := &app{
		meiliIndex:           strings.TrimSpace(getenv("MEILI_INDEX", "")),
		traceServiceName:     traceServiceName,
		localAIChatModel:     strings.TrimSpace(getenv("LOCALAI_CHAT_MODEL", "gemma-4-12b")),
		proxyTimeout:         proxyTimeout,
		proxyDefaultMaxToken: getenvInt("LOCALAI_PROXY_DEFAULT_MAX_TOKENS", 512),
		enabledTools:         parseCSVEnvSet("MCP_ENABLED_TOOLS"),
		disabledTools:        parseCSVEnvSet("MCP_DISABLED_TOOLS"),
		meiliClient:          meiliClient,
		ingestClient:         ingestClient,
		localAIClient:        localAIClient,
		proxyClient:          proxyClient,
		logger:               logger,
	}

	if a.meiliIndex == "" {
		return derrors.New(derrors.CodeInvalid, "mcpserver.Serve", "MEILI_INDEX is required")
	}

	mcpServer := server.NewMCPServer(
		"lappy-rag",
		"1.0.0",
		server.WithToolCapabilities(false),
		server.WithRecovery(),
		server.WithInstructions(
			"Use these tools ONLY for external Markdown documentation such as ADRs, runbooks, onboarding guides, wiki pages, and architecture notes. Do NOT use these tools for source code questions — use VS Code workspace context, #codebase, or file/symbol references for code instead.",
		),
	)

	a.registerTools(mcpServer)

	sseServer := server.NewSSEServer(
		mcpServer,
		server.WithSSEEndpoint("/sse"),
		server.WithMessageEndpoint("/message"),
	)

	mux := http.NewServeMux()
	mux.Handle("/health", observability.TraceHTTP(a.traceServiceName, "mcp.health", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})))
	mux.Handle("/sse", observability.TraceHTTP(a.traceServiceName, "mcp.sse", sseServer.SSEHandler()))
	mux.Handle("/message", observability.TraceHTTP(a.traceServiceName, "mcp.message", sseServer.MessageHandler()))
	mux.Handle("/localai-proxy/v1/chat/completions", observability.TraceHTTP(a.traceServiceName, "mcp.localai.proxy.chat_completions", http.HandlerFunc(a.handleLocalAIProxyChatCompletions)))

	httpServer := &http.Server{
		Addr:              fmt.Sprintf(":%d", mcpPort),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      proxyTimeout + 30*time.Second,
		IdleTimeout:       proxyTimeout + 60*time.Second,
	}

	return listenAndServe(ctx, httpServer, logger, nil)
}

func (a *app) registerTools(mcpServer *server.MCPServer) {
	if a.isToolEnabled("search_docs") {
		semanticSearchTool := mcp.NewTool(
			"search_docs",
			mcp.WithDescription("Semantic-first search over indexed Markdown documentation. Best for conceptual or paraphrased questions where exact keywords may differ. Do NOT use for source code — use workspace context for code questions."),
			mcp.WithString("query", mcp.Required(), mcp.Description("Query")),
			mcp.WithNumber("top_k", mcp.Description("Top K")),
		)
		mcpServer.AddTool(semanticSearchTool, a.handleSemanticSearchDocs)
	}

	if a.isToolEnabled("ask_docs") {
		askDocsTool := mcp.NewTool(
			"ask_docs",
			mcp.WithDescription("Q&A over indexed Markdown documentation. Internally resolves the best source/index and returns answer plus routing metadata."),
			mcp.WithString("question", mcp.Required(), mcp.Description("Question")),
			mcp.WithNumber("top_k", mcp.Description("Top K")),
			mcp.WithString("session_id", mcp.Description("Optional session id used to reuse successful prior plans for similar queries.")),
			mcp.WithString("plan_id", mcp.Description("Plan token from a prior confirmation_required response. Required when confirm_intent=true and confirmed=true.")),
			mcp.WithBoolean("confirm_intent", mcp.Description("If true, returns an intent confirmation prompt before running retrieval unless confirmed=true. Defaults to true.")),
			mcp.WithBoolean("confirmed", mcp.Description("Set true to confirm intent and execute retrieval when confirm_intent=true.")),
		)
		mcpServer.AddTool(askDocsTool, a.handleAskDocs)
	}

	if a.isToolEnabled("get_document") {
		getDocumentTool := mcp.NewTool(
			"get_document",
			mcp.WithDescription("Retrieve all chunks belonging to a specific documentation file. Accepts the relative file path (e.g. 'sops/references/index.md') or the doc_id hash returned by search results."),
			mcp.WithString("path_or_id", mcp.Required(), mcp.Description("Path Or Id")),
		)
		mcpServer.AddTool(getDocumentTool, a.handleGetDocument)
	}

	if a.isToolEnabled("get_section") {
		getSectionTool := mcp.NewTool(
			"get_section",
			mcp.WithDescription("Retrieve a specific section from a document by its heading text. Useful for jumping directly to a known section of a known document."),
			mcp.WithString("doc_id", mcp.Required(), mcp.Description("Doc Id")),
			mcp.WithString("section_heading", mcp.Required(), mcp.Description("Section Heading")),
		)
		mcpServer.AddTool(getSectionTool, a.handleGetSection)
	}

	if a.isToolEnabled("list_sources") {
		listSourcesTool := mcp.NewTool(
			"list_sources",
			mcp.WithDescription("List all documentation source folders currently indexed. Use to discover what documentation is available before searching."),
		)
		mcpServer.AddTool(listSourcesTool, a.handleListSources)
	}

	if a.isToolEnabled("trigger_ingest") {
		triggerIngestTool := mcp.NewTool(
			"trigger_ingest",
			mcp.WithDescription("Trigger ingest-api /jobs/ingest and optionally wait for completion by polling /jobs/{id}."),
			mcp.WithString("project_id", mcp.Required(), mcp.Description("Project Id")),
			mcp.WithString("mode", mcp.Description("Mode: full|incremental|watch")),
			mcp.WithBoolean("wait", mcp.Description("Wait for terminal status (default true).")),
			mcp.WithNumber("timeout_seconds", mcp.Description("Polling timeout in seconds (default 900).")),
			mcp.WithNumber("poll_interval_seconds", mcp.Description("Poll interval in seconds (default 2).")),
		)
		mcpServer.AddTool(triggerIngestTool, a.handleTriggerIngest)
	}
}

func (a *app) isToolEnabled(name string) bool {
	if _, blocked := a.disabledTools[name]; blocked {
		return false
	}
	if len(a.enabledTools) == 0 {
		return true
	}
	_, allowed := a.enabledTools[name]
	return allowed
}

func (a *app) handleSemanticSearchDocs(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx, span := observability.WithOpenTelemetryTrace(ctx, a.traceServiceName, "mcp.tool.search_docs")
	var handlerErr error
	defer observability.EndSpanWithStatus(span, &handlerErr)

	query, err := request.RequireString("query")
	if err != nil {
		handlerErr = err
		return mcp.NewToolResultError(err.Error()), nil
	}
	topK := request.GetInt("top_k", 5)

	hits, err := a.ingestClient.Search(ctx, query, topK)
	if err != nil {
		handlerErr = err
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultStructuredOnly(formatHits(hits)), nil
}

func (a *app) handleAskDocs(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx, span := observability.WithOpenTelemetryTrace(ctx, a.traceServiceName, "mcp.tool.ask_docs")
	var handlerErr error
	defer observability.EndSpanWithStatus(span, &handlerErr)

	question, err := request.RequireString("question")
	if err != nil {
		handlerErr = err
		return mcp.NewToolResultError(err.Error()), nil
	}
	question = strings.TrimSpace(question)
	if question == "" {
		handlerErr = derrors.New(derrors.CodeInvalid, "mcpserver", "question is required")
		return mcp.NewToolResultError("question is required"), nil
	}

	confirmIntent := request.GetBool("confirm_intent", true)
	confirmed := request.GetBool("confirmed", false)
	sessionID := strings.TrimSpace(request.GetString("session_id", ""))
	planID := strings.TrimSpace(request.GetString("plan_id", ""))
	topK := request.GetInt("top_k", 5)
	if topK <= 0 {
		topK = 5
	}

	if confirmIntent && !confirmed {
		planResp, planErr := a.ingestClient.Plan(ctx, question, topK, sessionID)
		if planErr != nil {
			handlerErr = planErr
			return mcp.NewToolResultError(planErr.Error()), nil
		}

		return mcp.NewToolResultStructuredOnly(map[string]any{
			"status":            "confirmation_required",
			"intent_hypothesis": fmt.Sprintf("You want a docs answer for: %s", planResp.Question),
			"proposed_plan": map[string]any{
				"plan_id":           planResp.PlanID,
				"reuse_source":      planResp.ReuseSource,
				"question":          planResp.Question,
				"available_indexes": planResp.AvailableIndexes,
				"routed_indexes":    planResp.RoutedIndexes,
				"query_plan":        planResp.QueryPlan,
				"candidate_sources": planResp.CandidateSources,
			},
			"confirmation_prompt":   "Does this match what you have in mind?",
			"requires_confirmation": true,
			"next_call": map[string]any{
				"question":       planResp.Question,
				"session_id":     sessionID,
				"plan_id":        planResp.PlanID,
				"top_k":          topK,
				"confirm_intent": true,
				"confirmed":      true,
			},
		}), nil
	}

	if confirmIntent && confirmed && planID == "" {
		handlerErr = derrors.New(derrors.CodeInvalid, "mcpserver", "plan_id is required when confirm_intent=true and confirmed=true")
		return mcp.NewToolResultError("plan_id is required when confirm_intent=true and confirmed=true"), nil
	}

	askResp, err := a.ingestClient.AskDetailedWithPlan(ctx, question, topK, planID, sessionID)
	if err != nil {
		handlerErr = err
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultStructuredOnly(map[string]any{
		"question":       askResp.Question,
		"plan_id":        askResp.PlanID,
		"answer":         askResp.Answer,
		"routed_indexes": askResp.RoutedIndexes,
		"query_plan":     askResp.QueryPlan,
		"sources":        askResp.Sources,
		"model":          askResp.Model,
	}), nil
}

func (a *app) handleGetDocument(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx, span := observability.WithOpenTelemetryTrace(ctx, a.traceServiceName, "mcp.tool.get_document")
	var handlerErr error
	defer observability.EndSpanWithStatus(span, &handlerErr)

	pathOrID, err := request.RequireString("path_or_id")
	if err != nil {
		handlerErr = err
		return mcp.NewToolResultError(err.Error()), nil
	}
	pathOrID = strings.TrimSpace(pathOrID)
	if pathOrID == "" {
		handlerErr = derrors.New(derrors.CodeInvalid, "mcpserver", "path_or_id is required")
		return mcp.NewToolResultError("path_or_id is required"), nil
	}

	payload := meili.SearchPayload{
		"q":      "",
		"filter": []string{fmt.Sprintf("path = %q", pathOrID)},
		"limit":  50,
	}

	if !strings.Contains(pathOrID, "/") && !strings.HasSuffix(strings.ToLower(pathOrID), ".md") {
		payload = meili.SearchPayload{
			"q":     pathOrID,
			"limit": 50,
		}
	}

	hits, err := a.meiliClient.Search(ctx, a.meiliIndex, payload)
	if err != nil {
		handlerErr = err
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultStructuredOnly(formatHits(hits)), nil
}

func (a *app) handleGetSection(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx, span := observability.WithOpenTelemetryTrace(ctx, a.traceServiceName, "mcp.tool.get_section")
	var handlerErr error
	defer observability.EndSpanWithStatus(span, &handlerErr)

	docID, err := request.RequireString("doc_id")
	if err != nil {
		handlerErr = err
		return mcp.NewToolResultError(err.Error()), nil
	}
	docID = strings.TrimSpace(docID)
	sectionHeading, err := request.RequireString("section_heading")
	if err != nil {
		handlerErr = err
		return mcp.NewToolResultError(err.Error()), nil
	}
	sectionHeading = strings.TrimSpace(sectionHeading)

	payload := meili.SearchPayload{
		"q":     strings.TrimSpace(docID + " " + sectionHeading),
		"limit": 5,
	}

	hits, err := a.meiliClient.Search(ctx, a.meiliIndex, payload)
	if err != nil {
		handlerErr = err
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultStructuredOnly(formatHits(hits)), nil
}

func (a *app) handleListSources(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx, span := observability.WithOpenTelemetryTrace(ctx, a.traceServiceName, "mcp.tool.list_sources")
	var handlerErr error
	defer observability.EndSpanWithStatus(span, &handlerErr)

	payload := meili.SearchPayload{
		"q":      "",
		"facets": []string{"source_name"},
		"limit":  0,
	}

	dist, err := a.meiliClient.SearchFacets(ctx, a.meiliIndex, payload)
	if err != nil {
		handlerErr = err
		return mcp.NewToolResultError(err.Error()), nil
	}

	sourceName := dist["source_name"]
	out := make([]string, 0, len(sourceName))
	for k := range sourceName {
		out = append(out, k)
	}
	sort.Strings(out)

	return mcp.NewToolResultStructuredOnly(out), nil
}

type triggerIngestParams struct {
	projectID      string
	mode           string
	wait           bool
	timeoutSeconds int
	pollSeconds    int
}

func parseTriggerParams(request mcp.CallToolRequest) (triggerIngestParams, error) {
	projectID, err := request.RequireString("project_id")
	if err != nil {
		return triggerIngestParams{}, err
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return triggerIngestParams{}, derrors.New(derrors.CodeInvalid, "mcpserver", "project_id is required")
	}

	mode := strings.ToLower(strings.TrimSpace(request.GetString("mode", "full")))
	if mode == "" {
		mode = "full"
	}
	if mode != "full" && mode != "incremental" && mode != "watch" {
		return triggerIngestParams{}, derrors.New(derrors.CodeInvalid, "mcpserver", "mode must be one of full|incremental|watch")
	}

	timeoutSeconds := request.GetInt("timeout_seconds", 900)
	if timeoutSeconds <= 0 {
		timeoutSeconds = 900
	}
	pollSeconds := request.GetInt("poll_interval_seconds", 2)
	if pollSeconds <= 0 {
		pollSeconds = 2
	}

	return triggerIngestParams{
		projectID:      projectID,
		mode:           mode,
		wait:           request.GetBool("wait", true),
		timeoutSeconds: timeoutSeconds,
		pollSeconds:    pollSeconds,
	}, nil
}

func (a *app) handleTriggerIngest(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx, span := observability.WithOpenTelemetryTrace(ctx, a.traceServiceName, "mcp.tool.trigger_ingest")
	var handlerErr error
	defer observability.EndSpanWithStatus(span, &handlerErr)

	params, err := parseTriggerParams(request)
	if err != nil {
		handlerErr = err
		return mcp.NewToolResultError(err.Error()), nil
	}

	if params.wait && params.mode == "watch" {
		handlerErr = derrors.New(derrors.CodeInvalid, "mcpserver", "wait=true is not supported for mode=watch; use wait=false")
		return mcp.NewToolResultError("wait=true is not supported for mode=watch; use wait=false"), nil
	}

	job, err := a.ingestClient.Enqueue(ctx, params.projectID, params.mode)
	if err != nil {
		handlerErr = err
		return mcp.NewToolResultError(err.Error()), nil
	}

	if !params.wait {
		return mcp.NewToolResultStructuredOnly(map[string]any{
			"queued": true,
			"job":    job,
		}), nil
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(params.timeoutSeconds)*time.Second)
	defer cancel()

	finalJob, err := a.ingestClient.PollJob(timeoutCtx, job.ID, time.Duration(params.pollSeconds)*time.Second)
	if err != nil {
		handlerErr = err
		return mcp.NewToolResultError(err.Error()), nil
	}

	if strings.EqualFold(finalJob.Status, "failed") {
		errMsg := strings.TrimSpace(finalJob.Error)
		if errMsg == "" {
			errMsg = "ingest job failed"
		}
		handlerErr = errors.New(errMsg)
		return mcp.NewToolResultError(errMsg), nil
	}

	return mcp.NewToolResultStructuredOnly(map[string]any{
		"queued": true,
		"job":    finalJob,
	}), nil
}

func formatHits(hits []map[string]any) []map[string]any {
	keys := []string{"title", "section_heading", "snippet", "path", "source_name", "chunk_index"}
	out := make([]map[string]any, 0, len(hits))
	for _, hit := range hits {
		trimmed := make(map[string]any, len(keys))
		for _, key := range keys {
			trimmed[key] = hit[key]
		}
		out = append(out, trimmed)
	}
	sort.Slice(out, func(i, j int) bool {
		left := chunkIndex(out[i])
		right := chunkIndex(out[j])
		return left < right
	})
	return out
}

func parseProxyBody(r *http.Request) (map[string]any, bool, error) {
	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, false, derrors.Wrap(err, derrors.CodeInvalid, "mcpserver.parseProxyBody", "read request body")
	}
	payload := map[string]any{}
	if len(rawBody) > 0 {
		if err := json.Unmarshal(rawBody, &payload); err != nil {
			return nil, false, derrors.Wrap(err, derrors.CodeInvalid, "mcpserver.parseProxyBody", "parse request body")
		}
	}
	stream, ok := payload["stream"].(bool)
	if !ok {
		stream = false
	}
	return payload, stream, nil
}

func chunkIndex(hit map[string]any) float64 {
	switch v := hit["chunk_index"].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case json.Number:
		f, _ := v.Float64()
		return f
	default:
		return 0
	}
}

func sendSSECompletion(w http.ResponseWriter, content, model string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	chunk := func(delta map[string]any, finishReason any) {
		emitSSE(w, map[string]any{
			"id": "localai-proxy", "object": "chat.completion.chunk", "created": 0, "model": model,
			"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finishReason}},
		})
	}

	chunk(map[string]any{"role": "assistant", "content": ""}, nil)
	if content != "" {
		chunk(map[string]any{"content": content}, nil)
	}
	chunk(map[string]any{"content": ""}, "stop")
	if _, err := w.Write([]byte("data: [DONE]\n\n")); err != nil {
		observability.NewLogger("mcp-server").Warnf("failed to write stream completion event: %v", err)
	}
}

func (a *app) handleLocalAIProxyChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	payload, stream, err := parseProxyBody(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if _, ok := payload["model"]; !ok {
		payload["model"] = a.localAIChatModel
	}
	if _, ok := payload["max_tokens"]; !ok {
		payload["max_tokens"] = a.proxyDefaultMaxToken
	}
	payload["stream"] = false

	ctx, cancel := context.WithTimeout(r.Context(), a.proxyTimeout)
	defer cancel()

	statusCode, body, err := a.forwardLocalAIRequest(ctx, payload)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	if !stream || statusCode >= 400 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		if _, err := w.Write(body); err != nil {
			a.logger.Warnf("failed to write proxy response: %v", err)
		}
		return
	}

	content, err := extractOpenAIChatContent(body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	model := a.localAIChatModel
	if rawModel, ok := payload["model"].(string); ok && strings.TrimSpace(rawModel) != "" {
		model = rawModel
	}

	sendSSECompletion(w, content, model)
}

func emitSSE(w io.Writer, payload map[string]any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	if _, err := w.Write([]byte("data: ")); err != nil {
		return
	}
	if _, err := w.Write(raw); err != nil {
		return
	}
	if _, err := w.Write([]byte("\n\n")); err != nil {
		return
	}
}

func extractOpenAIChatContent(body []byte) (string, error) {
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", derrors.Wrap(err, derrors.CodeUnavailable, "mcpserver.extractOpenAIChatContent", "parse localai response")
	}
	if len(parsed.Choices) == 0 {
		return "", derrors.New(derrors.CodeUnavailable, "mcpserver.extractOpenAIChatContent", "localai response did not include choices")
	}
	return strings.TrimSpace(parsed.Choices[0].Message.Content), nil
}

func (a *app) forwardLocalAIRequest(ctx context.Context, payload map[string]any) (int, []byte, error) {
	statusCode, body, err := a.proxyClient.ChatCompletions(ctx, payload)
	if err != nil {
		return 0, nil, derrors.Wrap(err, derrors.CodeUnavailable, "mcpserver.forwardLocalAIRequest", "execute proxy request")
	}

	return statusCode, body, nil
}

func writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		observability.NewLogger("mcp-server").Warnf("failed to write response: %v", err)
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

func parseCSVEnvSet(key string) map[string]struct{} {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return map[string]struct{}{}
	}

	set := map[string]struct{}{}
	parts := strings.Split(value, ",")
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		set[name] = struct{}{}
	}

	return set
}
