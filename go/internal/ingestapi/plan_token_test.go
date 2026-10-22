package ingestapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xynova/lappy-rag/internal/clients/localai"
	"github.com/xynova/lappy-rag/internal/clients/meili"
	"github.com/xynova/lappy-rag/internal/dspygo"
	"github.com/xynova/lappy-rag/internal/jobs"
	"github.com/xynova/lappy-rag/internal/profiles"
	"github.com/xynova/lappy-rag/internal/registry"
)

type fakeJobsSearchClient struct{}

func (f *fakeJobsSearchClient) EnableChatCompletions(ctx context.Context) error {
	return nil
}

func (f *fakeJobsSearchClient) EnsureIndex(ctx context.Context, indexName string) error {
	return nil
}

func (f *fakeJobsSearchClient) CancelIndexTasks(ctx context.Context, indexName string) error {
	return nil
}

func (f *fakeJobsSearchClient) ApplySettings(ctx context.Context, indexName string) error {
	return nil
}

func (f *fakeJobsSearchClient) ApplyEmbedders(ctx context.Context, indexName string) error {
	return nil
}

func (f *fakeJobsSearchClient) DeleteAllDocuments(ctx context.Context, indexName string) error {
	return nil
}

func (f *fakeJobsSearchClient) AddDocuments(ctx context.Context, indexName string, docs []meili.Document) (int, error) {
	return len(docs), nil
}

func (f *fakeJobsSearchClient) DeleteDocumentsByPath(ctx context.Context, indexName, projectID, relativePath string) error {
	return nil
}

func newTestServerForPlanAskHTTP(t *testing.T) *Server {
	t.Helper()

	meiliServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/search") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"hits": []map[string]any{{
				"path":            "docs/mesh.md",
				"section_heading": "Debugging",
				"snippet":         "Use logs and health checks.",
			}},
		})
	}))
	t.Cleanup(meiliServer.Close)

	localAIServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path != "/v1/chat/completions" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil || len(body) == 0 {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{"content": "Mesh apps are debugged with source-guided checks."},
			}},
		})
	}))
	t.Cleanup(localAIServer.Close)

	t.Setenv("MEILI_HOST", meiliServer.URL)
	t.Setenv("LOCALAI_BASE_URL", localAIServer.URL+"/v1")
	t.Setenv("LOCALAI_CHAT_MODEL", "test-model")

	meiliClient, err := meili.New()
	if err != nil {
		t.Fatalf("create meili client: %v", err)
	}

	localAIClient := localai.NewWithConfig("http://unused/v1", "", &http.Client{Timeout: 5 * time.Second})

	projectsCfg := &registry.Config{Projects: []registry.Project{{
		ID:        "mesh-docs",
		IndexName: "mesh-index",
		Enabled:   true,
		Default: profiles.DefaultRule{
			DocType:       "docs",
			ChunkStrategy: profiles.StrategyMarkdown,
		},
	}}}

	manager := jobs.NewManager(
		1,
		1,
		false,
		projectsCfg,
		&fakeJobsSearchClient{},
		jobs.NewFileStateStore(t.TempDir()),
		jobs.NewRealClock(),
	)

	frozen := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	return &Server{
		manager:          manager,
		inferenceTimeout: 30 * time.Second,
		queryPlanner:     dspygo.NewPlanner("", ""),
		meiliClient:      meiliClient,
		localAIClient:    localAIClient,
		plannedQueries:   make(map[string]plannedQuery),
		sessionPlans:     make(map[string][]plannedQuery),
		planTTL:          10 * time.Minute,
		clock:            func() time.Time { return frozen },
	}
}

func newTestServerForPlans() *Server {
	frozen := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	return &Server{
		plannedQueries: make(map[string]plannedQuery),
		sessionPlans:   make(map[string][]plannedQuery),
		planTTL:        10 * time.Minute,
		clock:          func() time.Time { return frozen },
	}
}

func TestStorePlannedQueryUsesInjectedClock(t *testing.T) {
	s := newTestServerForPlans()
	frozen := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	planID := s.storePlannedQuery(plannedQuery{
		SessionID: "session-clock",
		Question:  "clock test",
	})
	plan, ok := s.peekPlannedQuery(planID)
	if !ok {
		t.Fatalf("expected stored plan")
	}
	if !plan.CreatedAt.Equal(frozen) {
		t.Fatalf("expected CreatedAt %v, got %v", frozen, plan.CreatedAt)
	}
}

func TestConsumePlannedQuerySingleUse(t *testing.T) {
	s := newTestServerForPlans()
	planID := s.storePlannedQuery(plannedQuery{
		SessionID: "session-1",
		Question:  "how to debug mesh apps",
		CreatedAt: time.Now(),
	})

	if _, ok := s.peekPlannedQuery(planID); !ok {
		t.Fatalf("expected stored plan to be present")
	}

	if consumed := s.consumePlannedQuery(planID); !consumed {
		t.Fatalf("expected first consume to succeed")
	}

	if consumed := s.consumePlannedQuery(planID); consumed {
		t.Fatalf("expected second consume to fail")
	}

	if _, ok := s.peekPlannedQuery(planID); ok {
		t.Fatalf("expected plan to be removed after consume")
	}
}

func TestHandleQueryAskPlanIDMismatchDoesNotConsume(t *testing.T) {
	s := newTestServerForPlans()
	planID := s.storePlannedQuery(plannedQuery{
		SessionID: "session-1",
		Question:  "mesh application troubleshooting",
		CreatedAt: time.Now(),
	})

	body := []byte(`{"question":"unrelated payroll policy","plan_id":"` + planID + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/query/ask", bytes.NewReader(body))
	w := httptest.NewRecorder()

	s.handleQueryAsk(w, req)
	res := w.Result()

	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, res.StatusCode)
	}
	if !strings.Contains(w.Body.String(), "question does not match confirmed plan_id") {
		t.Fatalf("expected mismatch error, got %q", w.Body.String())
	}

	if _, ok := s.peekPlannedQuery(planID); !ok {
		t.Fatalf("expected plan to remain available after mismatch")
	}
}

func TestConsumePlannedQueryConcurrentSingleWinner(t *testing.T) {
	s := newTestServerForPlans()
	planID := s.storePlannedQuery(plannedQuery{
		SessionID: "session-2",
		Question:  "debug mesh services",
		CreatedAt: time.Now(),
	})

	const workers = 32
	var wg sync.WaitGroup
	var successCount int32

	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			if s.consumePlannedQuery(planID) {
				atomic.AddInt32(&successCount, 1)
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&successCount); got != 1 {
		t.Fatalf("expected exactly one successful consume, got %d", got)
	}

	if _, ok := s.peekPlannedQuery(planID); ok {
		t.Fatalf("expected plan to be absent after concurrent consume")
	}
}

func TestQueryPlanThenAskConsumesPlanID(t *testing.T) {
	s := newTestServerForPlanAskHTTP(t)

	planBody := []byte(`{"question":"how to debug mesh apps","session_id":"s-123","top_k":3}`)
	planReq := httptest.NewRequest(http.MethodPost, "/query/plan", bytes.NewReader(planBody))
	planW := httptest.NewRecorder()

	s.handleQueryPlan(planW, planReq)
	if planW.Result().StatusCode != http.StatusOK {
		t.Fatalf("expected plan status %d, got %d with body %q", http.StatusOK, planW.Result().StatusCode, planW.Body.String())
	}

	var planResp queryPlanResponse
	if err := json.Unmarshal(planW.Body.Bytes(), &planResp); err != nil {
		t.Fatalf("decode plan response: %v", err)
	}
	if strings.TrimSpace(planResp.PlanID) == "" {
		t.Fatalf("expected non-empty plan_id")
	}

	askBody := []byte(`{"question":"how to debug mesh apps","session_id":"s-123","plan_id":"` + planResp.PlanID + `"}`)
	askReq := httptest.NewRequest(http.MethodPost, "/query/ask", bytes.NewReader(askBody))
	askW := httptest.NewRecorder()

	s.handleQueryAsk(askW, askReq)
	if askW.Result().StatusCode != http.StatusOK {
		t.Fatalf("expected ask status %d, got %d with body %q", http.StatusOK, askW.Result().StatusCode, askW.Body.String())
	}

	var askResp queryAskResponse
	if err := json.Unmarshal(askW.Body.Bytes(), &askResp); err != nil {
		t.Fatalf("decode ask response: %v", err)
	}
	if strings.TrimSpace(askResp.Answer) == "" {
		t.Fatalf("expected non-empty answer")
	}

	askReq2 := httptest.NewRequest(http.MethodPost, "/query/ask", bytes.NewReader(askBody))
	askW2 := httptest.NewRecorder()

	s.handleQueryAsk(askW2, askReq2)
	if askW2.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("expected second ask status %d, got %d with body %q", http.StatusBadRequest, askW2.Result().StatusCode, askW2.Body.String())
	}
	if !strings.Contains(askW2.Body.String(), "plan_id not found or expired") {
		t.Fatalf("expected consumed plan_id error, got %q", askW2.Body.String())
	}
}
