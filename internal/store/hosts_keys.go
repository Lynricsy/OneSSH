package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var (
	ErrHostHasRunningJobs = errors.New("主机存在运行中的后台任务")
	ErrHostIsJumpHost     = errors.New("主机被其他主机用作跳板")
	ErrJumpHostArchived   = errors.New("跳板主机已归档，请先取消归档跳板主机")
)

// hostColumns 是 scanHost 期望的列顺序，所有读取主机的查询共用。
const hostColumns = `id,name,addr,port,username,auth_type,key_id,password_enc,hostkey_fp,jump_host_id,monitor_enabled,created_at,tags,archived_at`

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
	var tagsRaw sql.NullString
	err := row.Scan(&h.ID, &h.Name, &h.Addr, &h.Port, &h.Username, &h.AuthType, &h.KeyID, &h.PasswordEnc, &h.HostKeyFP, &h.JumpHostID, &enabled, &h.CreatedAt, &tagsRaw, &h.ArchivedAt)
	h.MonitorEnabled = enabled != 0
	h.Tags = parseHostTags(tagsRaw)
	return h, err
}
func (s *Store) CreateHost(ctx context.Context, h Host) (Host, error) {
	now := time.Now().Unix()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO hosts(name,addr,port,username,auth_type,key_id,password_enc,jump_host_id,monitor_enabled,created_at,tags) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, h.Name, h.Addr, h.Port, h.Username, h.AuthType, nullInt(h.KeyID), h.PasswordEnc, nullInt(h.JumpHostID), boolInt(h.MonitorEnabled), now, hostTagsJSON(h.Tags))
	if err != nil {
		return Host{}, err
	}
	h.ID, _ = res.LastInsertId()
	h.CreatedAt = now
	return h, nil
}
func (s *Store) UpdateHost(ctx context.Context, h Host) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE hosts SET name=?,addr=?,port=?,username=?,auth_type=?,key_id=?,password_enc=?,jump_host_id=?,monitor_enabled=?,tags=? WHERE id=?`, h.Name, h.Addr, h.Port, h.Username, h.AuthType, nullInt(h.KeyID), h.PasswordEnc, nullInt(h.JumpHostID), boolInt(h.MonitorEnabled), hostTagsJSON(h.Tags), h.ID)
	return err
}
func (s *Store) DeleteHost(ctx context.Context, id int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var running int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM jobs WHERE host_id=? AND status='running'`, id).Scan(&running); err != nil {
		return err
	}
	if running != 0 {
		return ErrHostHasRunningJobs
	}
	var dependents int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM hosts WHERE jump_host_id=?`, id).Scan(&dependents); err != nil {
		return err
	}
	if dependents != 0 {
		return ErrHostIsJumpHost
	}
	now := time.Now().Unix()
	restrictedGrant := `SELECT DISTINCT grant_id FROM oauth_refresh_tokens WHERE all_hosts=0 AND EXISTS (SELECT 1 FROM json_each(oauth_refresh_tokens.host_ids_json) WHERE json_each.value=?)`
	if _, err = tx.ExecContext(ctx, `UPDATE tokens SET expires_at=? WHERE id IN (SELECT access_token_id FROM oauth_refresh_tokens WHERE grant_id IN (`+restrictedGrant+`))`, now, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE oauth_refresh_tokens SET revoked_at=? WHERE grant_id IN (`+restrictedGrant+`) AND revoked_at IS NULL`, now, id); err != nil {
		return err
	}
	for _, query := range []string{
		`DELETE FROM token_hosts WHERE host_id=?`,
		`DELETE FROM sessions WHERE host_id=?`,
		`DELETE FROM jobs WHERE host_id=?`,
		`DELETE FROM metrics WHERE host_id=?`,
		`DELETE FROM memories WHERE host_id=?`,
	} {
		if _, err = tx.ExecContext(ctx, query, id); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM hosts WHERE id=?`, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}
func (s *Store) ListHosts(ctx context.Context) ([]Host, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+hostColumns+` FROM hosts ORDER BY name`)
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
	return scanHost(s.DB.QueryRowContext(ctx, `SELECT `+hostColumns+` FROM hosts WHERE name=?`, name))
}
func (s *Store) GetHost(ctx context.Context, id int64) (Host, error) {
	return scanHost(s.DB.QueryRowContext(ctx, `SELECT `+hostColumns+` FROM hosts WHERE id=?`, id))
}
func (s *Store) JumpDependentNames(ctx context.Context, id int64) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT name FROM hosts WHERE jump_host_id=? ORDER BY name`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names := make([]string, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}
func (s *Store) UpdateHostFingerprint(ctx context.Context, id int64, fp *string) error {
	var v any
	if fp != nil {
		v = *fp
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE hosts SET hostkey_fp=? WHERE id=?`, v, id)
	return err
}

// ArchiveHost 归档主机，重复归档保留最初的归档时刻。仍被未归档主机用作跳板时拒绝：
// 归档意味着停用，而依赖它的活跃主机每次连接都要经过它。
func (s *Store) ArchiveHost(ctx context.Context, id, at int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var dependents int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM hosts WHERE jump_host_id=? AND archived_at IS NULL`, id).Scan(&dependents); err != nil {
		return err
	}
	if dependents != 0 {
		return ErrHostIsJumpHost
	}
	if err = execOne(ctx, tx, `UPDATE hosts SET archived_at=COALESCE(archived_at,?) WHERE id=?`, at, id); err != nil {
		return err
	}
	return tx.Commit()
}

// UnarchiveHost 取消归档。跳板主机仍处于归档状态时拒绝，与 ArchiveHost 的约束对称。
// 归档期间不探测任务，确认时间早已过期；取消归档时为运行中任务重新起算失联宽限期，
// 否则主机恰好离线时下一轮维护就会把它们直接判为失联。
func (s *Store) UnarchiveHost(ctx context.Context, id, now int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var jumpArchived int
	err = tx.QueryRowContext(ctx, `SELECT count(*) FROM hosts h JOIN hosts j ON j.id=h.jump_host_id WHERE h.id=? AND j.archived_at IS NOT NULL`, id).Scan(&jumpArchived)
	if err != nil {
		return err
	}
	if jumpArchived != 0 {
		return ErrJumpHostArchived
	}
	if err = execOne(ctx, tx, `UPDATE hosts SET archived_at=NULL WHERE id=?`, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE jobs SET last_checked_at=? WHERE host_id=? AND status='running'`, now, id); err != nil {
		return err
	}
	return tx.Commit()
}

// execOne 执行恰好影响一行的写入，没有命中时返回 sql.ErrNoRows。
func execOne(ctx context.Context, tx *sql.Tx, query string, args ...any) error {
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}
func (s *Store) MonitoredHosts(ctx context.Context) ([]Host, error) {
	// 归档主机即使开着监控开关也不再轮询
	rows, err := s.DB.QueryContext(ctx, `SELECT `+hostColumns+` FROM hosts WHERE monitor_enabled=1 AND archived_at IS NULL ORDER BY name`)
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

// hostTagsJSON 把标签序列化为 JSON 数组字符串，空标签也存 '[]'。
func hostTagsJSON(tags []string) string {
	if len(tags) == 0 {
		return "[]"
	}
	raw, err := json.Marshal(tags)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

// parseHostTags 反序列化标签列，容忍 NULL、空串与脏数据，始终返回非 nil 切片。
func parseHostTags(raw sql.NullString) []string {
	tags := make([]string, 0)
	if raw.Valid {
		if err := json.Unmarshal([]byte(raw.String), &tags); err != nil || tags == nil {
			tags = make([]string, 0)
		}
	}
	return tags
}
func nullInt(v sql.NullInt64) any {
	if v.Valid {
		return v.Int64
	}
	return nil
}
