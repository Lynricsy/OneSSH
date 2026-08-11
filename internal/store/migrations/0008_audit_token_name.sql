ALTER TABLE audit ADD COLUMN token_name TEXT;

UPDATE audit
SET token_name = (
  SELECT name FROM tokens WHERE tokens.id = audit.token_id
)
WHERE token_id IS NOT NULL;
