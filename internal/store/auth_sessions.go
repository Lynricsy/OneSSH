package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

func TokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

type TokenCreate struct {
	Name        string
	Hash        string
	AllHosts    bool
	ManageHosts bool
	HostIDs     []int64
	Source      string
	ExpiresAt   int64
	Resource    string
	ClientID    string
}

func (s *Store) CreateToken(ctx context.Context, in TokenCreate) (Token, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Token{}, err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	source := in.Source
	if source == "" {
		source = "manual"
	}
	var expiresAt any
	if in.ExpiresAt > 0 {
		expiresAt = in.ExpiresAt
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO tokens(name,token_hash,all_hosts,manage_hosts,created_at,source,expires_at,resource,client_id) VALUES(?,?,?,?,?,?,?,?,?)`, in.Name, in.Hash, boolInt(in.AllHosts), boolInt(in.ManageHosts), now, source, expiresAt, nullableString(in.Resource), nullableString(in.ClientID))
	if err != nil {
		return Token{}, err
	}
	id, _ := res.LastInsertId()
	for _, hid := range in.HostIDs {
		if _, err = tx.ExecContext(ctx, `INSERT INTO token_hosts(token_id,host_id) VALUES(?,?)`, id, hid); err != nil {
			return Token{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return Token{}, err
	}
	token := Token{ID: id, Name: in.Name, AllHosts: in.AllHosts, ManageHosts: in.ManageHosts, Source: source, CreatedAt: now}
	token.ExpiresAt = sql.NullInt64{Int64: in.ExpiresAt, Valid: in.ExpiresAt > 0}
	token.Resource = sql.NullString{String: in.Resource, Valid: in.Resource != ""}
	token.ClientID = sql.NullString{String: in.ClientID, Valid: in.ClientID != ""}
	return token, nil
}
func (s *Store) FindToken(ctx context.Context, hash string) (Token, []Host, error) {
	return s.findToken(ctx, hash, "", false)
}

func (s *Store) FindTokenForResource(ctx context.Context, hash, resource string) (Token, []Host, error) {
	return s.findToken(ctx, hash, resource, true)
}

func (s *Store) findToken(ctx context.Context, hash, resource string, requireResource bool) (Token, []Host, error) {
	var t Token
	var all, manage int
	q := `SELECT id,name,all_hosts,manage_hosts,source,created_at,last_used_at,expires_at,resource,client_id FROM tokens WHERE token_hash=? AND (expires_at IS NULL OR expires_at>?)`
	args := []any{hash, time.Now().Unix()}
	if requireResource {
		q += ` AND (source='manual' OR resource=?)`
		args = append(args, resource)
	}
	err := s.DB.QueryRowContext(ctx, q, args...).Scan(&t.ID, &t.Name, &all, &manage, &t.Source, &t.CreatedAt, &t.LastUsedAt, &t.ExpiresAt, &t.Resource, &t.ClientID)
	if err != nil {
		return t, nil, err
	}
	t.AllHosts = all != 0
	t.ManageHosts = manage != 0
	_, _ = s.DB.ExecContext(ctx, `UPDATE tokens SET last_used_at=? WHERE id=?`, time.Now().Unix(), t.ID)
	hostQuery := `SELECT h.id,h.name,h.addr,h.port,h.username,h.auth_type,h.key_id,h.password_enc,h.hostkey_fp,h.proxy_jump_host,h.monitor_enabled,h.created_at FROM hosts h`
	hostArgs := []any{}
	if !t.AllHosts {
		hostQuery += ` JOIN token_hosts th ON th.host_id=h.id WHERE th.token_id=?`
		hostArgs = append(hostArgs, t.ID)
	}
	hostQuery += ` ORDER BY h.name`
	rows, err := s.DB.QueryContext(ctx, hostQuery, hostArgs...)
	if err != nil {
		return t, nil, err
	}
	defer rows.Close()
	var hosts []Host
	for rows.Next() {
		h, scanErr := scanHost(rows)
		if scanErr != nil {
			return t, nil, scanErr
		}
		hosts = append(hosts, h)
	}
	return t, hosts, rows.Err()
}
func (s *Store) ListTokens(ctx context.Context) ([]Token, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,name,all_hosts,manage_hosts,source,created_at,last_used_at,expires_at,resource,client_id FROM tokens ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Token, 0)
	for rows.Next() {
		var t Token
		var all, manage int
		if err := rows.Scan(&t.ID, &t.Name, &all, &manage, &t.Source, &t.CreatedAt, &t.LastUsedAt, &t.ExpiresAt, &t.Resource, &t.ClientID); err != nil {
			return nil, err
		}
		t.AllHosts = all != 0
		t.ManageHosts = manage != 0
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s *Store) DeleteToken(ctx context.Context, id int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var grantID sql.NullString
	if err = tx.QueryRowContext(ctx, `SELECT grant_id FROM oauth_refresh_tokens WHERE access_token_id=? LIMIT 1`, id).Scan(&grantID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if grantID.Valid {
		if err = revokeOAuthGrantTx(ctx, tx, grantID.String, time.Now().Unix()); err != nil {
			return err
		}
	}
	for _, q := range []string{`DELETE FROM token_hosts WHERE token_id=?`, `DELETE FROM tokens WHERE id=?`} {
		if _, err = tx.ExecContext(ctx, q, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) TokenHostIDs(ctx context.Context, id int64) ([]int64, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT host_id FROM token_hosts WHERE token_id=?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]int64, 0)
	for rows.Next() {
		var x int64
		if err := rows.Scan(&x); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) GetSession(ctx context.Context, tokenID, hostID int64, label string) (Session, error) {
	var x Session
	var raw string
	err := s.DB.QueryRowContext(ctx, `SELECT id,token_id,host_id,label,cwd,env_json,updated_at FROM sessions WHERE token_id=? AND host_id=? AND label=?`, tokenID, hostID, label).Scan(&x.ID, &x.TokenID, &x.HostID, &x.Label, &x.Cwd, &raw, &x.UpdatedAt)
	if err == sql.ErrNoRows {
		x = Session{TokenID: tokenID, HostID: hostID, Label: label, Cwd: "~", Env: map[string]string{}}
		return x, nil
	}
	if err != nil {
		return x, err
	}
	err = json.Unmarshal([]byte(raw), &x.Env)
	return x, err
}
func (s *Store) SaveSession(ctx context.Context, x Session) error {
	raw, err := json.Marshal(x.Env)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO sessions(token_id,host_id,label,cwd,env_json,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(token_id,host_id,label) DO UPDATE SET cwd=excluded.cwd,env_json=excluded.env_json,updated_at=excluded.updated_at`, x.TokenID, x.HostID, x.Label, x.Cwd, string(raw), time.Now().Unix())
	return err
}
