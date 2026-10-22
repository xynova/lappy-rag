package ingestjob

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/xynova/lappy-rag/internal/cli/guide"
	"github.com/xynova/lappy-rag/internal/clients/meili"
	derrors "github.com/xynova/lappy-rag/internal/errors"
	"github.com/xynova/lappy-rag/internal/jobs"
	"github.com/xynova/lappy-rag/internal/observability"
	"github.com/xynova/lappy-rag/internal/registry"
)

// RunConfig holds inputs for a one-shot ingest run.
type RunConfig struct {
	ProjectID    string
	Mode         string
	ProjectsPath string
}

// Run executes the ingest job using the provided context as the parent budget.
func (c RunConfig) Run(ctx context.Context) error {
	projectID := strings.TrimSpace(c.ProjectID)
	if projectID == "" {
		return derrors.New(derrors.CodeInvalid, "ingestjob.Run", "project id is required")
	}
	mode := strings.TrimSpace(c.Mode)
	if mode == "" {
		return derrors.New(derrors.CodeInvalid, "ingestjob.Run", "mode is required")
	}
	projectsPath := strings.TrimSpace(c.ProjectsPath)
	if projectsPath == "" {
		projectsPath = DetectProjectsPath()
	}

	traceCfgPath := observability.DetectOpenInferenceConfigPath()
	traceCfg, err := observability.LoadOpenInferenceConfigFromFile(traceCfgPath, "ingest-job")
	if err != nil {
		return derrors.Wrap(err, derrors.CodeUnavailable, "ingestjob.Run", "load openinference config")
	}
	traceServiceName := observability.ServiceNameFromConfig(&traceCfg)
	if traceCfg.Enabled {
		provider, err := observability.InitOTEL(traceServiceName, traceCfg.Endpoint)
		if err != nil {
			return derrors.Wrap(err, derrors.CodeUnavailable, "ingestjob.Run", "initialize openinference tracer provider")
		}
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := observability.ShutdownOTEL(shutdownCtx, provider); err != nil {
				observability.NewLogger("ingest-job").Warnf("failed to shutdown openinference tracer provider: %v", err)
			}
		}()
	}

	projectsCfg, err := registry.LoadMany(projectsPath)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeUnavailable, "ingestjob.Run", "load projects config")
	}

	project, err := projectsCfg.Get(projectID)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeNotFound, "ingestjob.Run", fmt.Sprintf("resolve project %q", projectID))
	}

	searchClient, err := meili.New()
	if err != nil {
		return derrors.Wrap(err, derrors.CodeUnavailable, "ingestjob.Run", "initialize meilisearch client")
	}

	allowSymlinks := strings.EqualFold(strings.TrimSpace(os.Getenv("ALLOW_FOLLOW_SYMLINKS")), "true")
	stateStore := jobs.NewFileStateStore(strings.TrimSpace(os.Getenv("STATE_DIR")))
	clock := jobs.NewRealClock()

	jobCtx, cancel := context.WithTimeout(ctx, 6*time.Hour)
	defer cancel()

	traceCtx, rootSpan := guide.StartCommandTrace(jobCtx, traceCfg, "ingest_job.run")
	var runErr error
	defer observability.EndSpanWithStatus(rootSpan, &runErr)

	stats, err := jobs.RunOnceWithDeps(traceCtx, project, jobs.Mode(mode), allowSymlinks, searchClient, searchClient, stateStore, clock)
	if err != nil {
		runErr = err
		return derrors.Wrap(err, derrors.CodeUnavailable, "ingestjob.Run", fmt.Sprintf("run ingest for project %q in mode %q", projectID, mode))
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(stats); err != nil {
		return derrors.Wrap(err, derrors.CodeUnavailable, "ingestjob.Run", fmt.Sprintf("encode stats output for project %q", projectID))
	}

	return nil
}

// DetectProjectsPath returns the default bundle directory for ingest jobs.
func DetectProjectsPath() string {
	candidates := []string{"/app/configs/bundles", "../configs/bundles", "configs/bundles"}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	return "../configs/bundles"
}
