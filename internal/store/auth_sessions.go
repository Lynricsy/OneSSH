package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"time"
)

func TokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
func (s *Store) CreateToken(ctx context.Context, name, hash string, all bool, hostIDs []int64) (Token, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Token{}, err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	res, err := tx.ExecContext(ctx, `INSERT INTO tokens(name,token_hash,all_hosts,created_at) VALUES(?,?,?,?)`, name, hash, boolInt(all), now)
	if err != nil {
		return Token{}, err
	}
	id, _ := res.LastInsertId()
	for _, hid := range hostIDs {
		if _, err = tx.ExecContext(ctx, `INSERT INTO token_hosts(token_id,host_id) VALUES(?,?)`, id, hid); err != nil {
			return Token{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return Token{}, err
	}
	return Token{ID: id, Name: name, AllHosts: all, CreatedAt: now}, nil
}
func (s *Store) FindToken(ctx context.Context, hash string) (Token, []Host, error) {
	var t Token
	var all int
	err := s.DB.QueryRowContext(ctx, `SELECT id,name,all_hosts,created_at,last_used_at FROM tokens WHERE token_hash=?`, hash).Scan(&t.ID, &t.Name, &all, &t.CreatedAt, &t.LastUsedAt)
	if err != nil {
		return t, nil, err
	}
	t.AllHosts = all != 0
	_, _ = s.DB.ExecContext(ctx, `UPDATE tokens SET last_used_at=? WHERE id=?`, time.Now().Unix(), t.ID)
	q := `SELECT h.id,h.name,h.addr,h.port,h.username,h.auth_type,h.key_id,h.password_enc,h.hostkey_fp,h.monitor_enabled,h.created_at FROM hosts h`
	args := []any{}
	if !t.AllHosts {
		q += ` JOIN token_hosts th ON th.host_id=h.id WHERE th.token_id=?`
		args = append(args, t.ID)
	}
	q += ` ORDER BY h.name`
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return t, nil, err
	}
	defer rows.Close()
	var hosts []Host
	for rows.Next() {
		h, e := scanHost(rows)
		if e != nil {
			return t, nil, e
		}
		hosts = append(hosts, h)
	}
	return t, hosts, rows.Err()
}
func (s *Store) ListTokens(ctx context.Context) ([]Token, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,name,all_hosts,created_at,last_used_at FROM tokens ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Token
	for rows.Next() {
		var t Token
		var a int
		if err := rows.Scan(&t.ID, &t.Name, &a, &t.CreatedAt, &t.LastUsedAt); err != nil {
			return nil, err
		}
		t.AllHosts = a != 0
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
	var out []int64
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
