ALTER TABLE tokens ADD COLUMN source TEXT NOT NULL DEFAULT 'manual';
ALTER TABLE tokens ADD COLUMN expires_at INTEGER;
ALTER TABLE tokens ADD COLUMN resource TEXT;
ALTER TABLE tokens ADD COLUMN client_id TEXT;

CREATE TABLE oauth_clients (
  client_id TEXT PRIMARY KEY,
  client_name TEXT NOT NULL,
  client_uri TEXT,
  redirect_uris_json TEXT NOT NULL,
  created_at INTEGER NOT NULL
);

CREATE TABLE oauth_authorization_codes (
  code_hash TEXT PRIMARY KEY,
  client_id TEXT NOT NULL,
  redirect_uri TEXT NOT NULL,
  resource TEXT NOT NULL,
  code_challenge TEXT NOT NULL,
  scope TEXT NOT NULL,
  all_hosts INTEGER NOT NULL,
  manage_hosts INTEGER NOT NULL,
  host_ids_json TEXT NOT NULL,
  expires_at INTEGER NOT NULL,
  created_at INTEGER NOT NULL,
  FOREIGN KEY(client_id) REFERENCES oauth_clients(client_id) ON DELETE CASCADE
);
CREATE TABLE oauth_refresh_tokens (
  token_hash TEXT PRIMARY KEY,
  grant_id TEXT NOT NULL,
  access_token_id INTEGER NOT NULL,
  client_id TEXT NOT NULL,
  resource TEXT NOT NULL,
  scope TEXT NOT NULL,
  all_hosts INTEGER NOT NULL,
  manage_hosts INTEGER NOT NULL,
  host_ids_json TEXT NOT NULL,
  expires_at INTEGER NOT NULL,
  created_at INTEGER NOT NULL,
  used_at INTEGER,
  revoked_at INTEGER,
  FOREIGN KEY(client_id) REFERENCES oauth_clients(client_id) ON DELETE CASCADE
);
CREATE INDEX oauth_refresh_tokens_grant_id ON oauth_refresh_tokens(grant_id);
CREATE INDEX oauth_refresh_tokens_expires_at ON oauth_refresh_tokens(expires_at);


CREATE INDEX oauth_authorization_codes_expires_at ON oauth_authorization_codes(expires_at);
