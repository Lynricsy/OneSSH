package store

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type OAuthClient struct {
	ClientID     string
	ClientName   string
	ClientURI    string
	RedirectURIs []string
	CreatedAt    int64
}

type OAuthAuthorizationCode struct {
	CodeHash      string
	ClientID      string
	RedirectURI   string
	Resource      string
	CodeChallenge string
	Scope         string
	AllHosts      bool
	ManageHosts   bool
	HostIDs       []int64
	ExpiresAt     int64
	CreatedAt     int64
	UsedAt        sql.NullInt64
	GrantID       sql.NullString
}

type OAuthAuthorizationCodeExchange struct {
	CodeHash         string
	ClientID         string
	RedirectURI      string
	Resource         string
	CodeChallenge    string
	GrantID          string
	AccessTokenName  string
	AccessTokenHash  string
	RefreshTokenHash string
	Now              int64
	AccessExpiresAt  int64
	RefreshExpiresAt int64
}
type OAuthRefreshToken struct {
	TokenHash     string
	GrantID       string
	AccessTokenID int64
	ClientID      string
	Resource      string
	Scope         string
	AllHosts      bool
	ManageHosts   bool
	HostIDs       []int64
	ExpiresAt     int64
	CreatedAt     int64
	UsedAt        sql.NullInt64
	RevokedAt     sql.NullInt64
}

var (
	ErrOAuthAuthorizationCodeReuse = errors.New("OAuth authorization code reuse detected")
	ErrOAuthRefreshReuse           = errors.New("OAuth refresh token reuse detected")
	ErrOAuthGrantRevoked           = errors.New("OAuth grant revoked")
)

func (s *Store) CreateOAuthClient(ctx context.Context, client OAuthClient) (OAuthClient, error) {
	rawRedirects, err := json.Marshal(client.RedirectURIs)
	if err != nil {
		return OAuthClient{}, err
	}
	if client.CreatedAt == 0 {
		client.CreatedAt = time.Now().Unix()
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO oauth_clients(client_id,client_name,client_uri,redirect_uris_json,created_at) VALUES(?,?,?,?,?)`, client.ClientID, client.ClientName, nullableString(client.ClientURI), string(rawRedirects), client.CreatedAt)
	return client, err
}

func (s *Store) GetOAuthClient(ctx context.Context, clientID string) (OAuthClient, error) {
	var client OAuthClient
	var clientURI sql.NullString
	var rawRedirects string
	err := s.DB.QueryRowContext(ctx, `SELECT client_id,client_name,client_uri,redirect_uris_json,created_at FROM oauth_clients WHERE client_id=?`, clientID).Scan(&client.ClientID, &client.ClientName, &clientURI, &rawRedirects, &client.CreatedAt)
	if err != nil {
		return OAuthClient{}, err
	}
	client.ClientURI = clientURI.String
	if err = json.Unmarshal([]byte(rawRedirects), &client.RedirectURIs); err != nil {
		return OAuthClient{}, err
	}
	return client, nil
}

func (s *Store) CreateOAuthAuthorizationCode(ctx context.Context, code OAuthAuthorizationCode) error {
	rawHostIDs, err := json.Marshal(code.HostIDs)
	if err != nil {
		return err
	}
	if code.CreatedAt == 0 {
		code.CreatedAt = time.Now().Unix()
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO oauth_authorization_codes(code_hash,client_id,redirect_uri,resource,code_challenge,scope,all_hosts,manage_hosts,host_ids_json,expires_at,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, code.CodeHash, code.ClientID, code.RedirectURI, code.Resource, code.CodeChallenge, code.Scope, boolInt(code.AllHosts), boolInt(code.ManageHosts), string(rawHostIDs), code.ExpiresAt, code.CreatedAt)
	return err
}

func (s *Store) ExchangeOAuthAuthorizationCode(ctx context.Context, in OAuthAuthorizationCodeExchange) (OAuthAuthorizationCode, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return OAuthAuthorizationCode{}, err
	}
	defer tx.Rollback()

	var code OAuthAuthorizationCode
	var allHosts, manageHosts int
	var rawHostIDs string
	err = tx.QueryRowContext(ctx, `SELECT code_hash,client_id,redirect_uri,resource,code_challenge,scope,all_hosts,manage_hosts,host_ids_json,expires_at,created_at,used_at,grant_id FROM oauth_authorization_codes WHERE code_hash=?`, in.CodeHash).Scan(&code.CodeHash, &code.ClientID, &code.RedirectURI, &code.Resource, &code.CodeChallenge, &code.Scope, &allHosts, &manageHosts, &rawHostIDs, &code.ExpiresAt, &code.CreatedAt, &code.UsedAt, &code.GrantID)
	if err != nil {
		return OAuthAuthorizationCode{}, err
	}
	code.AllHosts = allHosts != 0
	code.ManageHosts = manageHosts != 0
	if err = json.Unmarshal([]byte(rawHostIDs), &code.HostIDs); err != nil {
		return OAuthAuthorizationCode{}, err
	}
	valid := subtle.ConstantTimeCompare([]byte(code.CodeChallenge), []byte(in.CodeChallenge)) == 1 &&
		code.ClientID == in.ClientID &&
		code.RedirectURI == in.RedirectURI &&
		code.Resource == in.Resource &&
		code.ExpiresAt > in.Now
	if !valid {
		return OAuthAuthorizationCode{}, sql.ErrNoRows
	}
	if code.UsedAt.Valid {
		if !code.GrantID.Valid {
			return OAuthAuthorizationCode{}, errors.New("OAuth authorization code tombstone is missing its grant ID")
		}
		if err = revokeOAuthGrantTx(ctx, tx, code.GrantID.String, in.Now); err != nil {
			return OAuthAuthorizationCode{}, err
		}
		if err = tx.Commit(); err != nil {
			return OAuthAuthorizationCode{}, err
		}
		return OAuthAuthorizationCode{}, ErrOAuthAuthorizationCodeReuse
	}

	result, err := tx.ExecContext(ctx, `INSERT INTO tokens(name,token_hash,all_hosts,manage_hosts,created_at,source,expires_at,resource,client_id) VALUES(?,?,?,?,?,'oauth',?,?,?)`, in.AccessTokenName, in.AccessTokenHash, boolInt(code.AllHosts), boolInt(code.ManageHosts), in.Now, in.AccessExpiresAt, code.Resource, code.ClientID)
	if err != nil {
		return OAuthAuthorizationCode{}, err
	}
	accessTokenID, err := result.LastInsertId()
	if err != nil {
		return OAuthAuthorizationCode{}, err
	}
	for _, hostID := range code.HostIDs {
		if _, err = tx.ExecContext(ctx, `INSERT INTO token_hosts(token_id,host_id) VALUES(?,?)`, accessTokenID, hostID); err != nil {
			return OAuthAuthorizationCode{}, err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO oauth_refresh_tokens(token_hash,grant_id,access_token_id,client_id,resource,scope,all_hosts,manage_hosts,host_ids_json,expires_at,created_at,used_at,revoked_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,NULL,NULL)`, in.RefreshTokenHash, in.GrantID, accessTokenID, code.ClientID, code.Resource, code.Scope, boolInt(code.AllHosts), boolInt(code.ManageHosts), rawHostIDs, in.RefreshExpiresAt, in.Now); err != nil {
		return OAuthAuthorizationCode{}, err
	}
	result, err = tx.ExecContext(ctx, `UPDATE oauth_authorization_codes SET used_at=?,grant_id=? WHERE code_hash=? AND used_at IS NULL`, in.Now, in.GrantID, in.CodeHash)
	if err != nil {
		return OAuthAuthorizationCode{}, err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return OAuthAuthorizationCode{}, err
	}
	if updated != 1 {
		return OAuthAuthorizationCode{}, ErrOAuthAuthorizationCodeReuse
	}
	if err = tx.Commit(); err != nil {
		return OAuthAuthorizationCode{}, err
	}
	code.UsedAt = sql.NullInt64{Int64: in.Now, Valid: true}
	code.GrantID = sql.NullString{String: in.GrantID, Valid: true}
	return code, nil
}
func (s *Store) CreateOAuthRefreshToken(ctx context.Context, token OAuthRefreshToken) error {
	rawHostIDs, err := json.Marshal(token.HostIDs)
	if err != nil {
		return err
	}
	if token.GrantID == "" {
		return errors.New("OAuth grant ID 不能为空")
	}
	if token.CreatedAt == 0 {
		token.CreatedAt = time.Now().Unix()
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var revoked int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM oauth_refresh_tokens WHERE grant_id=? AND revoked_at IS NOT NULL`, token.GrantID).Scan(&revoked); err != nil {
		return err
	}
	if revoked != 0 {
		return ErrOAuthGrantRevoked
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO oauth_refresh_tokens(token_hash,grant_id,access_token_id,client_id,resource,scope,all_hosts,manage_hosts,host_ids_json,expires_at,created_at,used_at,revoked_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, token.TokenHash, token.GrantID, token.AccessTokenID, token.ClientID, token.Resource, token.Scope, boolInt(token.AllHosts), boolInt(token.ManageHosts), string(rawHostIDs), token.ExpiresAt, token.CreatedAt, nullInt(token.UsedAt), nullInt(token.RevokedAt))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) UseOAuthRefreshToken(ctx context.Context, tokenHash, clientID, resource string, now int64) (OAuthRefreshToken, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return OAuthRefreshToken{}, err
	}
	defer tx.Rollback()

	var token OAuthRefreshToken
	var allHosts, manageHosts int
	var rawHostIDs string
	err = tx.QueryRowContext(ctx, `SELECT token_hash,grant_id,access_token_id,client_id,resource,scope,all_hosts,manage_hosts,host_ids_json,expires_at,created_at,used_at,revoked_at FROM oauth_refresh_tokens WHERE token_hash=?`, tokenHash).Scan(&token.TokenHash, &token.GrantID, &token.AccessTokenID, &token.ClientID, &token.Resource, &token.Scope, &allHosts, &manageHosts, &rawHostIDs, &token.ExpiresAt, &token.CreatedAt, &token.UsedAt, &token.RevokedAt)
	if err != nil {
		return OAuthRefreshToken{}, err
	}
	token.AllHosts = allHosts != 0
	token.ManageHosts = manageHosts != 0
	if err = json.Unmarshal([]byte(rawHostIDs), &token.HostIDs); err != nil {
		return OAuthRefreshToken{}, err
	}
	if token.ClientID != clientID || token.Resource != resource || token.ExpiresAt <= now || token.RevokedAt.Valid {
		return OAuthRefreshToken{}, sql.ErrNoRows
	}
	if token.UsedAt.Valid {
		if err = revokeOAuthGrantTx(ctx, tx, token.GrantID, now); err != nil {
			return OAuthRefreshToken{}, err
		}
		if err = tx.Commit(); err != nil {
			return OAuthRefreshToken{}, err
		}
		return OAuthRefreshToken{}, ErrOAuthRefreshReuse
	}
	result, err := tx.ExecContext(ctx, `UPDATE oauth_refresh_tokens SET used_at=? WHERE token_hash=? AND used_at IS NULL AND revoked_at IS NULL`, now, tokenHash)
	if err != nil {
		return OAuthRefreshToken{}, err
	}
	used, err := result.RowsAffected()
	if err != nil {
		return OAuthRefreshToken{}, err
	}
	if used != 1 {
		return OAuthRefreshToken{}, ErrOAuthRefreshReuse
	}
	token.UsedAt = sql.NullInt64{Int64: now, Valid: true}
	if err = tx.Commit(); err != nil {
		return OAuthRefreshToken{}, err
	}
	return token, nil
}

func revokeOAuthGrantTx(ctx context.Context, tx *sql.Tx, grantID string, now int64) error {
	if _, err := tx.ExecContext(ctx, `UPDATE tokens SET expires_at=? WHERE id IN (SELECT access_token_id FROM oauth_refresh_tokens WHERE grant_id=?)`, now, grantID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE oauth_refresh_tokens SET revoked_at=? WHERE grant_id=? AND revoked_at IS NULL`, now, grantID)
	return err
}

func (s *Store) ValidateHostIDs(ctx context.Context, hostIDs []int64) ([]int64, error) {
	seen := make(map[int64]struct{}, len(hostIDs))
	unique := make([]int64, 0, len(hostIDs))
	for _, hostID := range hostIDs {
		if hostID <= 0 {
			return nil, errors.New("主机 ID 无效")
		}
		if _, ok := seen[hostID]; ok {
			continue
		}
		var exists int
		if err := s.DB.QueryRowContext(ctx, `SELECT 1 FROM hosts WHERE id=?`, hostID).Scan(&exists); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, errors.New("所选主机不存在")
			}
			return nil, err
		}
		seen[hostID] = struct{}{}
		unique = append(unique, hostID)
	}
	return unique, nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
