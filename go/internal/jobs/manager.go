package jobs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/xynova/lappy-rag/internal/clients/meili"
	"github.com/xynova/lappy-rag/internal/profiles"
	"github.com/xynova/lappy-rag/internal/registry"
)

// Status represents the lifecycle state of an ingest job.
type Status string

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
)

// Mode controls whether a run re-indexes everything or only changed files.
type Mode string

const (
	ModeFull        Mode = "full"
	ModeIncremental Mode = "incremental"
	ModeWatch       Mode = "watch"
)

// ValidModes is the set of accepted Mode values.
var ValidModes = []Mode{ModeFull, ModeIncremental, ModeWatch}

// IsValidMode reports whether m is a recognised ingest mode.
func IsValidMode(m Mode) bool {
	for _, v := range ValidModes {
		if v == m {
			return true
		}
	}
	return false
}

type EnqueueInput struct {
	ProjectID  string
	Mode       Mode
	TTLMinutes int
}

type Job struct {
	ID             string `json:"id"`
	ProjectID      string `json:"project_id"`
	IndexName      string `json:"index_name"`
	Mode           Mode   `json:"mode"`
	TTLMinutes     int    `json:"ttl_minutes,omitempty"`
	Status         Status `json:"status"`
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

// IndexAdminClient configures Meilisearch indexes for ingest runs.
type IndexAdminClient interface {
	EnableChatCompletions(ctx context.Context) error
	EnsureIndex(ctx context.Context, indexName string) error
	CancelIndexTasks(ctx context.Context, indexName string) error
	ApplySettings(ctx context.Context, indexName string) error
	ApplyEmbedders(ctx context.Context, indexName string) error
}

// DocumentWriter mutates indexed documents during ingest runs.
type DocumentWriter interface {
	DeleteAllDocuments(ctx context.Context, indexName string) error
	AddDocuments(ctx context.Context, indexName string, docs []meili.Document) (int, error)
	DeleteDocumentsByPath(ctx context.Context, indexName, projectID, relativePath string) error
}

// SearchClient defines indexing operations used by ingest runs.
type SearchClient interface {
	IndexAdminClient
	DocumentWriter
}

// StateStore persists per-project ingest state.
type StateStore interface {
	LoadProjectState(projectID string) (*projectState, error)
	SaveProjectState(state *projectState) error
}

// Clock provides current time for deterministic tests.
type Clock interface {
	Now() time.Time
}

// NewRealClock returns the production clock implementation.
func NewRealClock() Clock {
	return realClock{}
}

type realClock struct{}

func (realClock) Now() time.Time {
	return time.Now().UTC()
}

// NewFileStateStore returns a filesystem-backed state store.
func NewFileStateStore(stateDir string) StateStore {
	trimmed := strings.TrimSpace(stateDir)
	if trimmed == "" {
		trimmed = "state"
	}

	return &fileStateStore{stateDir: trimmed}
}

type fileStateStore struct {
	stateDir string
}

type Manager struct {
	mu                      sync.RWMutex
	jobs                    map[string]*Job
	projects                *registry.Config
	maxConcurrentJobs       int
	maxConcurrentPerProject int
	activeGlobal            int
	activeByProject         map[string]int
	searchClient            SearchClient
	stateStore              StateStore
	clock                   Clock
	allowSymlinks           bool
}

func NewManager(maxGlobal, maxPerProject int, allowSymlinks bool, projectsCfg *registry.Config, searchClient SearchClient, stateStore StateStore, clock Clock) *Manager {
	if projectsCfg == nil {
		panic("projectsCfg cannot be nil")
	}
	if searchClient == nil {
		panic("searchClient cannot be nil")
	}
	if stateStore == nil {
		panic("stateStore cannot be nil")
	}
	if clock == nil {
		panic("clock cannot be nil")
	}

	return &Manager{
		jobs:                    make(map[string]*Job),
		projects:                projectsCfg,
		maxConcurrentJobs:       maxGlobal,
		maxConcurrentPerProject: maxPerProject,
		activeByProject:         make(map[string]int),
		searchClient:            searchClient,
		stateStore:              stateStore,
		clock:                   clock,
		allowSymlinks:           allowSymlinks,
	}
}

func (m *Manager) Reload(projectsCfg *registry.Config) {
	if projectsCfg == nil {
		panic("projectsCfg cannot be nil")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.projects = projectsCfg
}

func (m *Manager) ListProjects() []registry.Project {
	m.mu.RLock()
	defer m.mu.RUnlock()

	projects := make([]registry.Project, 0, len(m.projects.Projects))
	for _, project := range m.projects.Projects {
		if project.Enabled {
			projects = append(projects, project)
		}
	}

	return projects
}

func (m *Manager) Enqueue(ctx context.Context, input EnqueueInput) (*Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	project, err := m.projects.Get(input.ProjectID)
	if err != nil {
		return nil, err
	}

	if m.activeGlobal >= m.maxConcurrentJobs {
		return nil, fmt.Errorf("global concurrency limit reached")
	}

	if m.activeByProject[input.ProjectID] >= m.maxConcurrentPerProject {
		for _, j := range m.jobs {
			if j.ProjectID == input.ProjectID && (j.Status == StatusQueued || j.Status == StatusRunning) {
				return cloneJob(j), nil
			}
		}
		return nil, fmt.Errorf("project %q already has maximum active jobs", input.ProjectID)
	}

	now := m.clock.Now()
	id := fmt.Sprintf("job-%d", now.UnixNano())
	job := &Job{
		ID:         id,
		ProjectID:  input.ProjectID,
		IndexName:  project.IndexName,
		Mode:       input.Mode,
		TTLMinutes: input.TTLMinutes,
		Status:     StatusQueued,
		QueuedAt:   now.Format(time.RFC3339),
	}

	m.jobs[id] = job
	m.activeGlobal++
	m.activeByProject[input.ProjectID]++

	go m.run(context.WithoutCancel(ctx), id, project)

	return cloneJob(job), nil
}

func (m *Manager) GetJob(id string) (*Job, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	job, ok := m.jobs[id]
	if !ok {
		return nil, false
	}

	return cloneJob(job), true
}

func (m *Manager) run(ctx context.Context, jobID string, project registry.Project) {
	mode := m.setRunning(jobID)

	started := m.clock.Now()
	stats, err := m.runOnce(ctx, project, mode)
	completed := m.clock.Now()

	m.mu.Lock()
	defer m.mu.Unlock()

	job := m.jobs[jobID]
	job.StartedAt = started.Format(time.RFC3339)
	job.CompletedAt = completed.Format(time.RFC3339)
	job.DurationMillis = completed.Sub(started).Milliseconds()

	if err != nil {
		job.Status = StatusFailed
		job.Error = err.Error()
	} else {
		job.Status = StatusCompleted
		job.FilesScanned = stats.FilesScanned
		job.FilesChanged = stats.FilesChanged
		job.FilesSkipped = stats.FilesSkipped
		job.FilesDeleted = stats.FilesDeleted
		job.ChunksPlanned = stats.ChunksPlanned
		job.ChunksIndexed = stats.ChunksIndexed
	}

	m.activeGlobal--
	m.activeByProject[job.ProjectID]--

	select {
	case <-ctx.Done():
		return
	default:
	}
}

func (m *Manager) setRunning(jobID string) Mode {
	m.mu.Lock()
	defer m.mu.Unlock()

	job, ok := m.jobs[jobID]
	if !ok {
		return ModeIncremental
	}

	job.Status = StatusRunning
	return job.Mode
}

func (m *Manager) runOnce(ctx context.Context, project registry.Project, mode Mode) (*RunStats, error) {
	return RunOnceWithDeps(ctx, project, mode, m.allowSymlinks, m.searchClient, m.stateStore, m.clock)
}

type RunStats struct {
	ProjectID     string `json:"project_id"`
	IndexName     string `json:"index_name"`
	Mode          Mode   `json:"mode"`
	FilesScanned  int    `json:"files_scanned"`
	FilesChanged  int    `json:"files_changed"`
	FilesSkipped  int    `json:"files_skipped"`
	FilesDeleted  int    `json:"files_deleted"`
	ChunksPlanned int    `json:"chunks_planned"`
	ChunksIndexed int    `json:"chunks_indexed"`
}

// RunOnce runs a full or incremental ingest for the given project, constructing
// all dependencies from environment variables. Prefer RunOnceWithDeps when
// dependencies should be injected (e.g. in tests).
func RunOnce(ctx context.Context, project registry.Project, mode Mode) (*RunStats, error) {
	searchClient, err := meili.New()
	if err != nil {
		return nil, fmt.Errorf("initialize meilisearch client: %w", err)
	}

	allowSymlinks := strings.EqualFold(strings.TrimSpace(os.Getenv("ALLOW_FOLLOW_SYMLINKS")), "true")
	stateStore := NewFileStateStore(strings.TrimSpace(os.Getenv("STATE_DIR")))
	clock := NewRealClock()

	return RunOnceWithDeps(ctx, project, mode, allowSymlinks, searchClient, stateStore, clock)
}

// RunOnceWithDeps runs a full or incremental ingest for the given project with
// all dependencies provided by the caller.
func RunOnceWithDeps(ctx context.Context, project registry.Project, mode Mode, allowSymlinks bool, searchClient SearchClient, stateStore StateStore, clock Clock) (*RunStats, error) {
	projectPath := project.ScanPath()

	stats := &RunStats{
		ProjectID: project.ID,
		IndexName: project.IndexName,
		Mode:      mode,
	}

	state, err := stateStore.LoadProjectState(project.ID)
	if err != nil {
		return nil, fmt.Errorf("load project state for %q: %w", project.ID, err)
	}

	nextState := &projectState{
		ProjectID: project.ID,
		Files:     make(map[string]string),
	}

	if err := configureIndex(ctx, searchClient, project, mode); err != nil {
		return nil, err
	}

	docs, err := scanProjectDocuments(ctx, searchClient, project, projectPath, mode, allowSymlinks, state, nextState, stats, clock)
	if err != nil {
		return nil, fmt.Errorf("scan project content: %w", err)
	}

	if err := reconcileDeletedDocuments(ctx, searchClient, project, mode, state, nextState, stats); err != nil {
		return nil, err
	}

	if len(docs) > 0 {
		written, writeErr := searchClient.AddDocuments(ctx, project.IndexName, docs)
		if writeErr != nil {
			return nil, fmt.Errorf("add final document batch to %q: %w", project.IndexName, writeErr)
		}
		stats.ChunksIndexed += written
	}

	if err := stateStore.SaveProjectState(nextState); err != nil {
		return nil, fmt.Errorf("save project state for %q: %w", project.ID, err)
	}

	return stats, nil
}

func configureIndex(ctx context.Context, searchClient SearchClient, project registry.Project, mode Mode) error {
	if err := searchClient.EnableChatCompletions(ctx); err != nil {
		return fmt.Errorf("enable meilisearch chat completions: %w", err)
	}

	if err := searchClient.EnsureIndex(ctx, project.IndexName); err != nil {
		return fmt.Errorf("ensure index %q: %w", project.IndexName, err)
	}

	// Cancel any stuck background tasks (e.g. a failing re-embed loop) before
	// applying settings — otherwise the new settings task sits enqueued behind
	// them and times out waiting to start.
	if err := searchClient.CancelIndexTasks(ctx, project.IndexName); err != nil {
		return fmt.Errorf("cancel pending tasks for index %q: %w", project.IndexName, err)
	}

	if err := searchClient.ApplySettings(ctx, project.IndexName); err != nil {
		return fmt.Errorf("apply index settings for %q: %w", project.IndexName, err)
	}

	if err := searchClient.ApplyEmbedders(ctx, project.IndexName); err != nil {
		return fmt.Errorf("apply embedders for %q: %w", project.IndexName, err)
	}

	if mode == ModeFull {
		if err := searchClient.DeleteAllDocuments(ctx, project.IndexName); err != nil {
			return fmt.Errorf("delete all documents for %q: %w", project.IndexName, err)
		}
	}

	return nil
}

func shouldSkipDirEntry(d fs.DirEntry, path string, allowedExtensions map[string]struct{}, allowSymlinks, followSymlinks bool) bool {
	if d.IsDir() {
		return true
	}
	if d.Type()&fs.ModeSymlink != 0 && !(allowSymlinks && followSymlinks) {
		return true
	}
	_, allowed := allowedExtensions[strings.ToLower(filepath.Ext(path))]
	return !allowed
}

func processScannedEntry(
	ctx context.Context,
	searchClient SearchClient,
	project registry.Project,
	path, projectPath string,
	mode Mode,
	raw []byte,
	state, nextState *projectState,
	stats *RunStats,
	clock Clock,
	docs *[]meili.Document,
) error {
	plan, err := buildDocumentPlan(path, projectPath, project, raw)
	if err != nil {
		return err
	}
	if plan == nil {
		return nil
	}
	stats.FilesScanned++
	nextState.Files[plan.NormalizedPath] = plan.ContentHash
	if mode != ModeFull {
		if prev, found := state.Files[plan.NormalizedPath]; found && prev == plan.ContentHash {
			stats.FilesSkipped++
			return nil
		}
		if err := searchClient.DeleteDocumentsByPath(ctx, project.IndexName, project.ID, plan.NormalizedPath); err != nil {
			return fmt.Errorf("delete existing documents for %q at %q: %w", project.IndexName, plan.NormalizedPath, err)
		}
	}
	stats.FilesChanged++
	return appendPlannedChunks(ctx, searchClient, project, plan, stats, clock, docs)
}

func scanProjectDocuments(
	ctx context.Context,
	searchClient SearchClient,
	project registry.Project,
	projectPath string,
	mode Mode,
	allowSymlinks bool,
	state *projectState,
	nextState *projectState,
	stats *RunStats,
	clock Clock,
) ([]meili.Document, error) {
	docs := make([]meili.Document, 0, 1024)
	allowedExtensions := make(map[string]struct{}, 4)
	for _, ext := range project.AllowedFileExtensions() {
		allowedExtensions[ext] = struct{}{}
	}

	err := filepath.WalkDir(projectPath, func(path string, d fs.DirEntry, walkErr error) error { //nolint:gosec // G122: symlink traversal is gated by allowSymlinks && project.FollowSymlinks
		if walkErr != nil {
			return walkErr
		}
		if shouldSkipDirEntry(d, path, allowedExtensions, allowSymlinks, project.FollowSymlinks) {
			return nil
		}
		raw, readErr := os.ReadFile(path) //nolint:gosec // G304: path is within operator-configured project scan root
		if readErr != nil {
			return fmt.Errorf("read markdown file %s: %w", path, readErr)
		}
		return processScannedEntry(ctx, searchClient, project, path, projectPath, mode, raw, state, nextState, stats, clock, &docs)
	})
	if err != nil {
		return nil, err
	}

	return docs, nil
}

type documentPlan struct {
	NormalizedPath string
	ContentHash    string
	DocType        string
	Strategy       profiles.ChunkStrategy
	Chunks         []string
	Title          string
}

func buildDocumentPlan(path, projectPath string, project registry.Project, raw []byte) (*documentPlan, error) {
	relativePath, relErr := filepath.Rel(projectPath, path)
	if relErr != nil {
		return nil, relErr
	}

	content := strings.TrimSpace(string(raw))
	if content == "" {
		return nil, nil
	}

	docType, strategy := project.Match(relativePath)
	if docType == "" || strategy == profiles.ChunkStrategy("") {
		return nil, fmt.Errorf("invalid profile resolution for %s", relativePath)
	}

	return &documentPlan{
		NormalizedPath: strings.ReplaceAll(relativePath, "\\", "/"),
		ContentHash:    stableHash(content),
		DocType:        docType,
		Strategy:       strategy,
		Chunks:         chunkByStrategy(content, strategy),
		Title:          deriveTitle(relativePath, content),
	}, nil
}

func appendPlannedChunks(
	ctx context.Context,
	searchClient SearchClient,
	project registry.Project,
	plan *documentPlan,
	stats *RunStats,
	clock Clock,
	docs *[]meili.Document,
) error {
	for idx, chunk := range plan.Chunks {
		text := strings.TrimSpace(chunk)
		if text == "" {
			continue
		}

		chunkID := stableID(project.ID, plan.NormalizedPath, idx)
		embedText := strings.TrimSpace(
			fmt.Sprintf("Title: %s\nPath: %s\nType: %s\n\n%s", plan.Title, plan.NormalizedPath, plan.DocType, text),
		)
		embedText = clampEmbedText(embedText, maxEmbedWords, maxEmbedChars)
		*docs = append(*docs, meili.Document{
			ID:             chunkID,
			ProjectID:      project.ID,
			DocID:          stableID(project.ID, plan.NormalizedPath, -1),
			Title:          plan.Title,
			SectionHeading: plan.Title,
			EmbedText:      embedText,
			Content:        text,
			Snippet:        buildSnippet(text, 40),
			Path:           plan.NormalizedPath,
			SourceName:     topLevel(plan.NormalizedPath),
			DocType:        plan.DocType,
			ChunkStrategy:  string(plan.Strategy),
			ChunkIndex:     idx,
			UpdatedAt:      clock.Now().Format(time.RFC3339),
		})
		stats.ChunksPlanned++

		if len(*docs) < 128 {
			continue
		}

		written, writeErr := searchClient.AddDocuments(ctx, project.IndexName, *docs)
		if writeErr != nil {
			return fmt.Errorf("add document batch to %q: %w", project.IndexName, writeErr)
		}
		stats.ChunksIndexed += written
		*docs = (*docs)[:0]
	}

	return nil
}

func reconcileDeletedDocuments(
	ctx context.Context,
	searchClient SearchClient,
	project registry.Project,
	mode Mode,
	state *projectState,
	nextState *projectState,
	stats *RunStats,
) error {
	if mode == ModeFull {
		return nil
	}

	for oldPath := range state.Files {
		if _, exists := nextState.Files[oldPath]; exists {
			continue
		}

		if err := searchClient.DeleteDocumentsByPath(ctx, project.IndexName, project.ID, oldPath); err != nil {
			return fmt.Errorf("delete removed file documents for %q at %q: %w", project.IndexName, oldPath, err)
		}
		stats.FilesDeleted++
	}

	return nil
}

// maxMarkdownChunkChars is the character cap for prose markdown sections.
// At ~4 chars/token for English, 1200 chars ≈ 300 tokens (target: 256–384 tokens).
const maxMarkdownChunkChars = 1200

// maxCodeChunkChars is the character cap for code-containing sections.
// Code tokenises more densely (~3 chars/token), so 768 chars ≈ 256 tokens.
const maxCodeChunkChars = 768

// markdownOverlapChars is the overlap carried into the next prose chunk (~15% of maxMarkdownChunkChars).
const markdownOverlapChars = 180

// codeOverlapChars is the overlap carried into the next code chunk (~15% of maxCodeChunkChars).
const codeOverlapChars = 115

// maxEmbedWords caps embed payload size to keep BGE input under 512 tokens.
// BGE uses WordPiece: technical content averages ~3.2 sub-tokens per whitespace word.
// 140 words × 3.2 = ~448 tokens, leaving ~64 token headroom for [CLS]/[SEP] and variance.
const maxEmbedWords = 140

// maxEmbedChars adds a hard upper bound even for no-whitespace content (URLs, hashes, code).
// At ~1.57 chars/token for technical text, 600 chars → ~382 tokens — safely below 512.
const maxEmbedChars = 600

var (
	reH2 = regexp.MustCompile(`(?m)^##\s+`)
	reH3 = regexp.MustCompile(`(?m)^###\s+`)
)

func chunkByStrategy(content string, strategy profiles.ChunkStrategy) []string {
	normalized := strings.TrimSpace(content)
	if normalized == "" {
		return nil
	}

	if strategy == profiles.StrategyMarkdown || strategy == profiles.StrategyCodeMarkdown {
		maxChars := maxMarkdownChunkChars
		overlapChars := markdownOverlapChars
		if strategy == profiles.StrategyCodeMarkdown {
			maxChars = maxCodeChunkChars
			overlapChars = codeOverlapChars
		}

		// Step 1: split on H2 boundaries.
		h2parts := reH2.Split(normalized, -1)
		chunks := make([]string, 0, len(h2parts))
		for _, h2 := range h2parts {
			h2 = strings.TrimSpace(h2)
			if h2 == "" {
				continue
			}
			if len(h2) <= maxChars {
				chunks = append(chunks, h2)
				continue
			}
			// Step 2: H2 section is too large — split on H3 boundaries.
			h3parts := reH3.Split(h2, -1)
			for _, h3 := range h3parts {
				h3 = strings.TrimSpace(h3)
				if h3 == "" {
					continue
				}
				if len(h3) <= maxChars {
					chunks = append(chunks, h3)
					continue
				}
				// Step 3: H3 section is still too large — word-boundary split with overlap.
				chunks = append(chunks, splitWithOverlap(h3, maxChars, overlapChars)...)
			}
		}
		if len(chunks) > 0 {
			return chunks
		}
	}

	return []string{normalized}
}

// splitWithOverlap splits text at word boundaries, seeding each successive chunk with
// the trailing overlapChars words of the previous chunk.
func splitWithOverlap(text string, maxChars, overlapChars int) []string {
	if maxChars <= 0 || len(text) <= maxChars {
		return []string{text}
	}

	words := strings.Fields(text)
	var chunks []string
	var buf strings.Builder

	for _, w := range words {
		// A single token can exceed maxChars (for example minified text, URLs, hashes,
		// or long code literals). Split it hard with overlap so we never emit oversize
		// chunks that exceed the embed model token limit.
		if len(w) > maxChars {
			if buf.Len() > 0 {
				chunks = append(chunks, buf.String())
				buf.Reset()
			}
			chunks = append(chunks, splitLongTokenWithOverlap(w, maxChars, overlapChars)...)
			continue
		}

		// +1 for the space separator.
		if buf.Len() > 0 && buf.Len()+1+len(w) > maxChars {
			chunks = append(chunks, buf.String())
			overlap := overlapWordsForChars(strings.Fields(buf.String()), overlapChars)
			buf.Reset()
			for _, ow := range overlap {
				if buf.Len() > 0 {
					buf.WriteByte(' ')
				}
				buf.WriteString(ow)
			}
		}
		if buf.Len() > 0 {
			buf.WriteByte(' ')
		}
		buf.WriteString(w)
	}
	if buf.Len() > 0 {
		chunks = append(chunks, buf.String())
	}

	if len(chunks) == 0 {
		return []string{text}
	}
	return chunks
}

func clampEmbedText(text string, maxWords, maxChars int) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}

	if maxWords > 0 {
		words := strings.Fields(trimmed)
		if len(words) > maxWords {
			trimmed = strings.Join(words[:maxWords], " ")
		}
	}

	if maxChars > 0 && len(trimmed) > maxChars {
		trimmed = trimmed[:maxChars]
	}

	return strings.TrimSpace(trimmed)
}

// splitLongTokenWithOverlap hard-splits a no-whitespace token into maxChars windows.
func splitLongTokenWithOverlap(token string, maxChars, overlapChars int) []string {
	if maxChars <= 0 || len(token) <= maxChars {
		return []string{token}
	}

	if overlapChars < 0 {
		overlapChars = 0
	}
	if overlapChars >= maxChars {
		overlapChars = maxChars / 4
	}

	parts := make([]string, 0)
	start := 0
	for start < len(token) {
		end := start + maxChars
		if end >= len(token) {
			parts = append(parts, token[start:])
			break
		}
		parts = append(parts, token[start:end])
		step := maxChars - overlapChars
		if step <= 0 {
			step = maxChars
		}
		start += step
	}

	return parts
}

// overlapWordsForChars returns the tail words of a slice that fit within maxChars total.
func overlapWordsForChars(words []string, maxChars int) []string {
	total := 0
	start := len(words)
	for start > 0 {
		w := words[start-1]
		// +1 for the space that will precede it.
		if total+len(w)+1 > maxChars {
			break
		}
		total += len(w) + 1
		start--
	}
	return words[start:]
}

func deriveTitle(relativePath, content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}

	base := filepath.Base(relativePath)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func buildSnippet(content string, words int) string {
	items := strings.Fields(content)
	if len(items) <= words {
		return strings.Join(items, " ")
	}
	return strings.Join(items[:words], " ")
}

func topLevel(relativePath string) string {
	if relativePath == "" {
		return ""
	}
	parts := strings.Split(relativePath, "/")
	return parts[0]
}

func stableID(projectID, path string, chunkIndex int) string {
	seed := fmt.Sprintf("%s|%s|%d", projectID, path, chunkIndex)
	hash := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(hash[:])
}

func stableHash(content string) string {
	hash := sha256.Sum256([]byte(content))
	return hex.EncodeToString(hash[:])
}

type projectState struct {
	ProjectID string            `json:"project_id"`
	Files     map[string]string `json:"files"`
}

func (s *fileStateStore) LoadProjectState(projectID string) (*projectState, error) {
	path := s.stateFilePath(projectID)
	raw, err := os.ReadFile(path) //nolint:gosec // G304: path is operator-configured state directory
	if err != nil {
		if os.IsNotExist(err) {
			return &projectState{ProjectID: projectID, Files: make(map[string]string)}, nil
		}
		return nil, fmt.Errorf("read state file for %q: %w", projectID, err)
	}

	var state projectState
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, fmt.Errorf("parse state file for %q: %w", projectID, err)
	}

	if state.Files == nil {
		state.Files = make(map[string]string)
	}

	if state.ProjectID == "" {
		state.ProjectID = projectID
	}

	return &state, nil
}

func (s *fileStateStore) SaveProjectState(state *projectState) error {
	path := s.stateFilePath(state.ProjectID)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}

	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state for %q: %w", state.ProjectID, err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("write temp state for %q: %w", state.ProjectID, err)
	}

	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("swap state file for %q: %w", state.ProjectID, err)
	}

	return nil
}

func (s *fileStateStore) stateFilePath(projectID string) string {
	return filepath.Join(s.stateDir, fmt.Sprintf("%s.json", projectID))
}

func cloneJob(source *Job) *Job {
	copy := *source
	return &copy
}
