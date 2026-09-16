ALTER TABLE tokens ADD COLUMN disabled_tools_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE oauth_authorization_codes ADD COLUMN disabled_tools_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE oauth_refresh_tokens ADD COLUMN disabled_tools_json TEXT NOT NULL DEFAULT '[]';
