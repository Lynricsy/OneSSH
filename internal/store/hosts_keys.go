package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

func (s *Store) CreateKey(ctx context.Context, name string, enc []byte, public string) (Key, error) {
	now := time.Now().Unix()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO keys(name,private_key_enc,public_key,created_at) VALUES(?,?,?,?)`, name, enc, public, now)
	if err != nil {
		return Key{}, err
	}
	id, _ := res.LastInsertId()
	return Key{ID: id, Name: name, PrivateKeyEnc: enc, PublicKey: public, CreatedAt: now}, nil
}
func (s *Store) ListKeys(ctx context.Context) ([]Key, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,name,public_key,created_at FROM keys ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Key, 0)
	for rows.Next() {
		var x Key
		if err := rows.Scan(&x.ID, &x.Name, &x.PublicKey, &x.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
func (s *Store) GetKey(ctx context.Context, id int64) (Key, error) {
	var x Key
	err := s.DB.QueryRowContext(ctx, `SELECT id,name,private_key_enc,public_key,created_at FROM keys WHERE id=?`, id).Scan(&x.ID, &x.Name, &x.PrivateKeyEnc, &x.PublicKey, &x.CreatedAt)
	return x, err
}
func (s *Store) DeleteKey(ctx context.Context, id int64) error {
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM hosts WHERE key_id=?`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("密钥正被 %d 个主机引用", n)
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM keys WHERE id=?`, id)
	return err
}

func scanHost(row interface{ Scan(...any) error }) (Host, error) {
	var h Host
	var enabled int
	err := row.Scan(&h.ID, &h.Name, &h.Addr, &h.Port, &h.Username, &h.AuthType, &h.KeyID, &h.PasswordEnc, &h.HostKeyFP, &enabled, &h.CreatedAt)
	h.MonitorEnabled = enabled != 0
	return h, err
}
func (s *Store) CreateHost(ctx context.Context, h Host) (Host, error) {
	now := time.Now().Unix()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO hosts(name,addr,port,username,auth_type,key_id,password_enc,monitor_enabled,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, h.Name, h.Addr, h.Port, h.Username, h.AuthType, nullInt(h.KeyID), h.PasswordEnc, boolInt(h.MonitorEnabled), now)
	if err != nil {
		return Host{}, err
	}
	h.ID, _ = res.LastInsertId()
	h.CreatedAt = now
	return h, nil
}
func (s *Store) UpdateHost(ctx context.Context, h Host) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE hosts SET name=?,addr=?,port=?,username=?,auth_type=?,key_id=?,password_enc=?,monitor_enabled=? WHERE id=?`, h.Name, h.Addr, h.Port, h.Username, h.AuthType, nullInt(h.KeyID), h.PasswordEnc, boolInt(h.MonitorEnabled), h.ID)
	return err
}
func (s *Store) DeleteHost(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM hosts WHERE id=?`, id)
	return err
}
func (s *Store) ListHosts(ctx context.Context) ([]Host, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,name,addr,port,username,auth_type,key_id,password_enc,hostkey_fp,monitor_enabled,created_at FROM hosts ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Host, 0)
	for rows.Next() {
		x, err := scanHost(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
func (s *Store) GetHostByName(ctx context.Context, name string) (Host, error) {
	return scanHost(s.DB.QueryRowContext(ctx, `SELECT id,name,addr,port,username,auth_type,key_id,password_enc,hostkey_fp,monitor_enabled,created_at FROM hosts WHERE name=?`, name))
}
func (s *Store) GetHost(ctx context.Context, id int64) (Host, error) {
	return scanHost(s.DB.QueryRowContext(ctx, `SELECT id,name,addr,port,username,auth_type,key_id,password_enc,hostkey_fp,monitor_enabled,created_at FROM hosts WHERE id=?`, id))
}
func (s *Store) UpdateHostFingerprint(ctx context.Context, id int64, fp *string) error {
	var v any
	if fp != nil {
		v = *fp
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE hosts SET hostkey_fp=? WHERE id=?`, v, id)
	return err
}
func (s *Store) MonitoredHosts(ctx context.Context) ([]Host, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,name,addr,port,username,auth_type,key_id,password_enc,hostkey_fp,monitor_enabled,created_at FROM hosts WHERE monitor_enabled=1 ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Host, 0)
	for rows.Next() {
		h, e := scanHost(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
func nullInt(v sql.NullInt64) any {
	if v.Valid {
		return v.Int64
	}
	return nil
}
