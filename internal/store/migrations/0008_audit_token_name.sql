-- 审计会永久保留 token_id，因此令牌行号也必须永久不复用。
CREATE TABLE tokens_v8 (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL UNIQUE,
  token_hash TEXT NOT NULL UNIQUE,
  all_hosts INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  last_used_at INTEGER,
  manage_hosts INTEGER NOT NULL DEFAULT 0,
  source TEXT NOT NULL DEFAULT 'manual',
  expires_at INTEGER,
  resource TEXT,
  client_id TEXT
);

INSERT INTO tokens_v8(
  id, name, token_hash, all_hosts, created_at, last_used_at,
  manage_hosts, source, expires_at, resource, client_id
)
SELECT
  id, name, token_hash, all_hosts, created_at, last_used_at,
  manage_hosts, source, expires_at, resource, client_id
FROM tokens;

-- 保留所有仍可观测到的历史 ID；旧表若已使用 AUTOINCREMENT，也不能降低其序列。
WITH token_id_ceiling(value) AS (
  SELECT MAX(value)
  FROM (
    SELECT COALESCE(MAX(id), 0) AS value FROM tokens_v8
    UNION ALL SELECT COALESCE(MAX(token_id), 0) FROM audit
    UNION ALL SELECT COALESCE(MAX(token_id), 0) FROM sessions
    UNION ALL SELECT COALESCE(MAX(token_id), 0) FROM jobs
    UNION ALL SELECT COALESCE(MAX(token_id), 0) FROM memories
    UNION ALL SELECT COALESCE(MAX(access_token_id), 0) FROM oauth_refresh_tokens
    UNION ALL SELECT COALESCE(seq, 0) FROM sqlite_sequence WHERE name = 'tokens'
  )
)
UPDATE sqlite_sequence
SET seq = (SELECT value FROM token_id_ceiling)
WHERE name = 'tokens_v8';

WITH token_id_ceiling(value) AS (
  SELECT MAX(value)
  FROM (
    SELECT COALESCE(MAX(id), 0) AS value FROM tokens_v8
    UNION ALL SELECT COALESCE(MAX(token_id), 0) FROM audit
    UNION ALL SELECT COALESCE(MAX(token_id), 0) FROM sessions
    UNION ALL SELECT COALESCE(MAX(token_id), 0) FROM jobs
    UNION ALL SELECT COALESCE(MAX(token_id), 0) FROM memories
    UNION ALL SELECT COALESCE(MAX(access_token_id), 0) FROM oauth_refresh_tokens
    UNION ALL SELECT COALESCE(seq, 0) FROM sqlite_sequence WHERE name = 'tokens'
  )
)
INSERT INTO sqlite_sequence(name, seq)
SELECT 'tokens_v8', value
FROM token_id_ceiling
WHERE NOT EXISTS (SELECT 1 FROM sqlite_sequence WHERE name = 'tokens_v8');

DROP TABLE tokens;
ALTER TABLE tokens_v8 RENAME TO tokens;

-- 令牌创建时间只有秒精度；跳过创建当秒，宁可保持未知，也不把旧主体归属给复用 ID 的新主体。
UPDATE audit
SET token_name = (
  SELECT name
  FROM tokens
  WHERE tokens.id = audit.token_id
)
WHERE token_name IS NULL
  AND token_id IS NOT NULL
  AND EXISTS (
    SELECT 1
    FROM tokens
    WHERE tokens.id = audit.token_id
      AND audit.ts >= (tokens.created_at + 1) * 1000
  );
