CREATE TABLE IF NOT EXISTS keys (
  id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE,
  private_key_enc BLOB NOT NULL, public_key TEXT NOT NULL, created_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS hosts (
  id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, addr TEXT NOT NULL,
  port INTEGER NOT NULL DEFAULT 22, username TEXT NOT NULL,
  auth_type TEXT NOT NULL CHECK(auth_type IN ('key','password')),
  key_id INTEGER REFERENCES keys(id), password_enc BLOB, hostkey_fp TEXT,
  monitor_enabled INTEGER NOT NULL DEFAULT 1, created_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS tokens (
  id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, token_hash TEXT NOT NULL UNIQUE,
  all_hosts INTEGER NOT NULL DEFAULT 1, created_at INTEGER NOT NULL, last_used_at INTEGER);
CREATE TABLE IF NOT EXISTS token_hosts (
  token_id INTEGER NOT NULL, host_id INTEGER NOT NULL, PRIMARY KEY(token_id, host_id));
CREATE TABLE IF NOT EXISTS sessions (
  id INTEGER PRIMARY KEY, token_id INTEGER NOT NULL, host_id INTEGER NOT NULL,
  label TEXT NOT NULL, cwd TEXT NOT NULL, env_json TEXT NOT NULL DEFAULT '{}',
  updated_at INTEGER NOT NULL, UNIQUE(token_id, host_id, label));
CREATE TABLE IF NOT EXISTS jobs (
  id TEXT PRIMARY KEY, host_id INTEGER NOT NULL, token_id INTEGER,
  command TEXT NOT NULL, cwd TEXT NOT NULL, pid INTEGER,
  used_setsid INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL CHECK(status IN ('running','exited','lost','killed')),
  exit_code INTEGER, started_at INTEGER NOT NULL, finished_at INTEGER);
CREATE TABLE IF NOT EXISTS audit (
  id INTEGER PRIMARY KEY, ts INTEGER NOT NULL, token_id INTEGER, tool TEXT NOT NULL,
  host TEXT, params_json TEXT NOT NULL, ok INTEGER NOT NULL, exit_code INTEGER,
  duration_ms INTEGER, bytes_out INTEGER);
CREATE INDEX IF NOT EXISTS audit_ts ON audit(ts);
CREATE TABLE IF NOT EXISTS metrics (
  host_id INTEGER NOT NULL, ts INTEGER NOT NULL, cpu_pct REAL,
  mem_used_kb INTEGER, mem_total_kb INTEGER, load1 REAL, disks_json TEXT,
  PRIMARY KEY(host_id, ts));
