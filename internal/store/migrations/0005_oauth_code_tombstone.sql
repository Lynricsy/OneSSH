ALTER TABLE oauth_authorization_codes ADD COLUMN used_at INTEGER;
ALTER TABLE oauth_authorization_codes ADD COLUMN grant_id TEXT;
CREATE INDEX oauth_authorization_codes_grant_id ON oauth_authorization_codes(grant_id);
