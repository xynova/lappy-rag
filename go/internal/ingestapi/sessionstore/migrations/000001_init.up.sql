CREATE TABLE IF NOT EXISTS session_plan_examples (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id TEXT NOT NULL,
  question TEXT NOT NULL,
  query_plan_json TEXT NOT NULL,
  routed_indexes_json TEXT NOT NULL,
  top_k INTEGER NOT NULL,
  semantic_ratio REAL NOT NULL,
  created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_session_plan_examples_session_created
  ON session_plan_examples(session_id, created_at DESC);
