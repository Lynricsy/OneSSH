CREATE TABLE IF NOT EXISTS command_runs (
  seq INTEGER PRIMARY KEY AUTOINCREMENT,
  id TEXT NOT NULL UNIQUE,
  token_id INTEGER,
  token_name TEXT,
  tool TEXT NOT NULL,
  host_id INTEGER,
  host TEXT NOT NULL,
  command TEXT NOT NULL,
  cwd TEXT NOT NULL,
  session TEXT,
  job_id TEXT UNIQUE,
  status TEXT NOT NULL CHECK(status IN ('running','succeeded','failed','timeout','cancelled','lost')),
  exit_code INTEGER,
  stdout_preview TEXT NOT NULL DEFAULT '',
  stderr_preview TEXT NOT NULL DEFAULT '',
  stdout_bytes INTEGER NOT NULL DEFAULT 0,
  stderr_bytes INTEGER NOT NULL DEFAULT 0,
  output_available INTEGER NOT NULL DEFAULT 0,
  output_expired INTEGER NOT NULL DEFAULT 0,
  output_error TEXT,
  error_text TEXT,
  started_at INTEGER NOT NULL,
  finished_at INTEGER
);

CREATE INDEX IF NOT EXISTS idx_command_runs_started ON command_runs(started_at DESC);
CREATE INDEX IF NOT EXISTS idx_command_runs_host ON command_runs(host);
CREATE INDEX IF NOT EXISTS idx_command_runs_tool ON command_runs(tool);
CREATE INDEX IF NOT EXISTS idx_command_runs_status ON command_runs(status);
