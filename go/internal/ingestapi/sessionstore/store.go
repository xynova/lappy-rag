package sessionstore

import (
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	derrors "github.com/xynova/lappy-rag/internal/errors"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// PlannedQuery is a persisted successful query plan.
type PlannedQuery struct {
	SessionID        string
	Question         string
	RoutedIndexes    []string
	QueryPlan        []string
	CandidateSources []map[string]any
	TopK             int
	SemanticRatio    float64
	CreatedAt        time.Time
}

// Config holds session store construction inputs.
type Config struct {
	DBPath string
	Now    func() time.Time
}

// Store persists query session plans in SQLite.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

// Open creates a Store and applies pending migrations.
func Open(cfg Config) (*Store, error) {
	if cfg.Now == nil {
		return nil, derrors.New(derrors.CodeInvalid, "sessionstore.Open", "clock is required")
	}
	dbPath := strings.TrimSpace(cfg.DBPath)
	if dbPath == "" {
		return nil, derrors.New(derrors.CodeInvalid, "sessionstore.Open", "db path is required")
	}
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, derrors.Wrap(err, derrors.CodeUnavailable, "sessionstore.Open", "create db directory")
	}

	dsn := dbPath + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeUnavailable, "sessionstore.Open", "open db")
	}
	db.SetMaxOpenConns(1)

	driver, err := sqlite.WithInstance(db, &sqlite.Config{})
	if err != nil {
		_ = db.Close()
		return nil, derrors.Wrap(err, derrors.CodeInternal, "sessionstore.Open", "migrate driver")
	}
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		_ = db.Close()
		return nil, derrors.Wrap(err, derrors.CodeInternal, "sessionstore.Open", "migration source")
	}
	m, err := migrate.NewWithInstance("iofs", src, "sqlite", driver)
	if err != nil {
		_ = db.Close()
		return nil, derrors.Wrap(err, derrors.CodeInternal, "sessionstore.Open", "migrate instance")
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		_ = db.Close()
		return nil, derrors.Wrap(err, derrors.CodeInternal, "sessionstore.Open", "migrate up")
	}

	return &Store{db: db, now: cfg.Now}, nil
}

// Close closes the database handle.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// RecordSuccessful inserts a successful plan row.
func (s *Store) RecordSuccessful(plan PlannedQuery) error {
	if s == nil || s.db == nil {
		return nil
	}
	sessionID := strings.TrimSpace(plan.SessionID)
	if sessionID == "" {
		return nil
	}
	question := strings.TrimSpace(plan.Question)
	if question == "" {
		return derrors.New(derrors.CodeInvalid, "sessionstore.RecordSuccessful", "question is required")
	}

	queryPlanRaw, err := json.Marshal(plan.QueryPlan)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeInternal, "sessionstore.RecordSuccessful", "marshal query plan")
	}
	routedRaw, err := json.Marshal(plan.RoutedIndexes)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeInternal, "sessionstore.RecordSuccessful", "marshal routed indexes")
	}

	createdAt := plan.CreatedAt
	if createdAt.IsZero() {
		createdAt = s.now().UTC()
	}

	_, err = s.db.Exec(
		`INSERT INTO session_plan_examples(
			session_id, question, query_plan_json, routed_indexes_json, top_k, semantic_ratio, created_at
		) VALUES(?, ?, ?, ?, ?, ?, ?)`,
		sessionID,
		question,
		string(queryPlanRaw),
		string(routedRaw),
		plan.TopK,
		plan.SemanticRatio,
		createdAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeUnavailable, "sessionstore.RecordSuccessful", "insert row")
	}
	return nil
}

// FindSimilar returns the newest semantically matching plan for the session.
func (s *Store) FindSimilar(sessionID, question string, matches func(string, string) bool) (PlannedQuery, bool, error) {
	if s == nil || s.db == nil {
		return PlannedQuery{}, false, nil
	}
	sessionID = strings.TrimSpace(sessionID)
	question = strings.TrimSpace(question)
	if sessionID == "" || question == "" {
		return PlannedQuery{}, false, nil
	}
	if matches == nil {
		return PlannedQuery{}, false, derrors.New(derrors.CodeInvalid, "sessionstore.FindSimilar", "matcher is required")
	}

	rows, err := s.db.Query(
		`SELECT session_id, question, query_plan_json, routed_indexes_json, top_k, semantic_ratio, created_at
		 FROM session_plan_examples
		 WHERE session_id = ?
		 ORDER BY created_at DESC
		 LIMIT 50`,
		sessionID,
	)
	if err != nil {
		return PlannedQuery{}, false, derrors.Wrap(err, derrors.CodeUnavailable, "sessionstore.FindSimilar", "query")
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var (
			row             PlannedQuery
			queryPlanJSON   string
			routedIndexJSON string
			createdRaw      string
		)
		if err := rows.Scan(&row.SessionID, &row.Question, &queryPlanJSON, &routedIndexJSON, &row.TopK, &row.SemanticRatio, &createdRaw); err != nil {
			return PlannedQuery{}, false, derrors.Wrap(err, derrors.CodeInternal, "sessionstore.FindSimilar", "scan")
		}
		if err := json.Unmarshal([]byte(queryPlanJSON), &row.QueryPlan); err != nil {
			continue
		}
		if err := json.Unmarshal([]byte(routedIndexJSON), &row.RoutedIndexes); err != nil {
			continue
		}
		if created, err := time.Parse(time.RFC3339Nano, createdRaw); err == nil {
			row.CreatedAt = created
		}
		if !matches(question, row.Question) {
			continue
		}
		return row, true, nil
	}
	if err := rows.Err(); err != nil {
		return PlannedQuery{}, false, derrors.Wrap(err, derrors.CodeUnavailable, "sessionstore.FindSimilar", "iterate")
	}
	return PlannedQuery{}, false, nil
}

// MatcherError formats matcher failures for tests.
func MatcherError(msg string) error {
	return fmt.Errorf("%s", msg)
}
