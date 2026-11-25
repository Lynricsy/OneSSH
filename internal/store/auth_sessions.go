package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"onessh/internal/cmdpolicy"
	"onessh/internal/toolgroups"
)

func TokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

type TokenCreate struct {
	Name          string
	Hash          string
	AllHosts      bool
	ManageHosts   bool
	DisabledTools []string
	CommandPolicy cmdpolicy.TokenPolicy
	HostIDs       []int64
	Source        string
	ExpiresAt     int64
	Resource      string
	ClientID      string
}

// TokenUpdate 用于手动更新已有令牌的主机范围、MCP 工具组 denylist 与命令策略。
type TokenUpdate struct {
	Name          string
	AllHosts      bool
	ManageHosts   bool
	HostIDs       []int64
	DisabledTools []string
	// CommandPolicy 为 nil 时保留原策略：旧客户端/旧前端不会因为没带这个字段
	// 就把已有的收紧策略静默清空；显式传 &TokenPolicy{Mode:"off"} 才清除。
	CommandPolicy *cmdpolicy.TokenPolicy
}

// tokenColumns 固定令牌查询的列顺序。数据库加列时必须同时改到这里与 scanToken：
// 两处分开了，查询出来的字段会静默错位到别的名字上。
const tokenColumns = `id,name,all_hosts,manage_hosts,disabled_tools_json,command_policy_json,source,created_at,last_used_at,expires_at,resource,client_id`

// rowScanner 是 *sql.Row 与 *sql.Rows 的公共部分，令牌查询三种场景共用一份扫描逻辑。
// 类型定义在 command_runs.go，这里直接复用。
func scanToken(row rowScanner) (Token, error) {
	var t Token
	var allHosts, manageHosts int
	var disabledJSON, policyJSON string
	if err := row.Scan(&t.ID, &t.Name, &allHosts, &manageHosts, &disabledJSON, &policyJSON, &t.Source, &t.CreatedAt, &t.LastUsedAt, &t.ExpiresAt, &t.Resource, &t.ClientID); err != nil {
		return Token{}, err
	}
	t.AllHosts = allHosts != 0
	t.ManageHosts = manageHosts != 0
	disabledTools, err := decodeDisabledTools(disabledJSON)
	if err != nil {
		return Token{}, err
	}
	t.DisabledTools = disabledTools
	// 命令策略损坏时 DecodeTokenPolicy 返回全拒策略，不返回错误：
	// 与 denylist 的 fail-closed 一致，但不至于让令牌彻底不可用。
	t.CommandPolicy = cmdpolicy.DecodeTokenPolicy(policyJSON)
	return t, nil
}

func (s *Store) CreateToken(ctx context.Context, in TokenCreate) (Token, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Token{}, err
	}
	defer tx.Rollback()
	token, err := createTokenTx(ctx, tx, in, time.Now().Unix())
	if err != nil {
		return Token{}, err
	}
	if err = tx.Commit(); err != nil {
		return Token{}, err
	}
	return token, nil
}

func createTokenTx(ctx context.Context, tx *sql.Tx, in TokenCreate, now int64) (Token, error) {
	source := in.Source
	if source == "" {
		source = "manual"
	}
	var expiresAt any
	if in.ExpiresAt > 0 {
		expiresAt = in.ExpiresAt
	}
	disabledJSON, disabledTools, err := encodeDisabledTools(in.DisabledTools)
	if err != nil {
		return Token{}, err
	}
	policyJSON, commandPolicy, err := cmdpolicy.EncodeTokenPolicy(in.CommandPolicy)
	if err != nil {
		return Token{}, err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO tokens(name,token_hash,all_hosts,manage_hosts,disabled_tools_json,command_policy_json,created_at,source,expires_at,resource,client_id) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, in.Name, in.Hash, boolInt(in.AllHosts), boolInt(in.ManageHosts), disabledJSON, policyJSON, now, source, expiresAt, nullableString(in.Resource), nullableString(in.ClientID))
	if err != nil {
		return Token{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Token{}, err
	}
	for _, hostID := range in.HostIDs {
		if _, err = tx.ExecContext(ctx, `INSERT INTO token_hosts(token_id,host_id) VALUES(?,?)`, id, hostID); err != nil {
			return Token{}, err
		}
	}
	token := Token{ID: id, Name: in.Name, AllHosts: in.AllHosts, ManageHosts: in.ManageHosts, DisabledTools: disabledTools, CommandPolicy: commandPolicy, Source: source, CreatedAt: now}
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
	q := `SELECT ` + tokenColumns + ` FROM tokens WHERE token_hash=? AND (expires_at IS NULL OR expires_at>?)`
	args := []any{hash, time.Now().Unix()}
	if requireResource {
		q += ` AND (source='manual' OR resource=?)`
		args = append(args, resource)
	}
	t, err := scanToken(s.DB.QueryRowContext(ctx, q, args...))
	if err != nil {
		return t, nil, err
	}
	_, _ = s.DB.ExecContext(ctx, `UPDATE tokens SET last_used_at=? WHERE id=?`, time.Now().Unix(), t.ID)
	// 归档主机对所有令牌都不可见：在这里过滤即覆盖 hosts_list 与全部按主机鉴权的 MCP 工具。
	// 列名均未与 token_hosts 冲突，JOIN 时无需加表前缀。
	hostQuery := `SELECT ` + hostColumns + ` FROM hosts h`
	hostArgs := []any{}
	if !t.AllHosts {
		hostQuery += ` JOIN token_hosts th ON th.host_id=h.id WHERE th.token_id=? AND h.archived_at IS NULL`
		hostArgs = append(hostArgs, t.ID)
	} else {
		hostQuery += ` WHERE h.archived_at IS NULL`
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
	rows, err := s.DB.QueryContext(ctx, `SELECT `+tokenColumns+` FROM tokens ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Token, 0)
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) UpdateToken(ctx context.Context, id int64, in TokenUpdate) (Token, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Token{}, err
	}
	defer tx.Rollback()
	disabledJSON, _, err := encodeDisabledTools(in.DisabledTools)
	if err != nil {
		return Token{}, err
	}
	var policyJSON any
	if in.CommandPolicy != nil {
		encoded, _, err := cmdpolicy.EncodeTokenPolicy(*in.CommandPolicy)
		if err != nil {
			return Token{}, err
		}
		policyJSON = encoded
	}
	// COALESCE 让“字段缺失”与“显式 off”走不同路径：前者保留原策略，后者写入 off。
	result, err := tx.ExecContext(ctx, `UPDATE tokens SET name=?,all_hosts=?,manage_hosts=?,disabled_tools_json=?,command_policy_json=COALESCE(?,command_policy_json) WHERE id=?`, in.Name, boolInt(in.AllHosts), boolInt(in.ManageHosts), disabledJSON, policyJSON, id)
	if err != nil {
		return Token{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Token{}, err
	}
	if affected == 0 {
		return Token{}, sql.ErrNoRows
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM token_hosts WHERE token_id=?`, id); err != nil {
		return Token{}, err
	}
	hostIDs := in.HostIDs
	if hostIDs == nil {
		hostIDs = []int64{}
	}
	for _, hostID := range hostIDs {
		if _, err = tx.ExecContext(ctx, `INSERT INTO token_hosts(token_id,host_id) VALUES(?,?)`, id, hostID); err != nil {
			return Token{}, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE oauth_refresh_tokens SET all_hosts=?,manage_hosts=?,host_ids_json=?,disabled_tools_json=?,command_policy_json=COALESCE(?,command_policy_json) WHERE access_token_id=? AND used_at IS NULL AND revoked_at IS NULL`, boolInt(in.AllHosts), boolInt(in.ManageHosts), mustJSON(hostIDs), disabledJSON, policyJSON, id); err != nil {
		return Token{}, err
	}
	if err = tx.Commit(); err != nil {
		return Token{}, err
	}
	return s.GetToken(ctx, id)
}

func (s *Store) GetToken(ctx context.Context, id int64) (Token, error) {
	return scanToken(s.DB.QueryRowContext(ctx, `SELECT `+tokenColumns+` FROM tokens WHERE id=?`, id))
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

func encodeDisabledTools(names []string) (string, []string, error) {
	// 持久化边界强制校验：未知分组名直接拒绝，API / OAuth 层的 NormalizeList 只是 UX。
	// 新列必须同时改到 tokenColumns 与 scanToken，否则所有令牌查询都会错位。
	normalized, err := toolgroups.NormalizeList(names)
	if err != nil {
		return "", nil, err
	}
	if normalized == nil {
		normalized = []string{}
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return "", nil, err
	}
	return string(raw), normalized, nil
}

func decodeDisabledTools(raw string) ([]string, error) {
	if raw == "" {
		return []string{}, nil
	}
	var names []string
	if err := json.Unmarshal([]byte(raw), &names); err != nil {
		return nil, err
	}
	// JSON null 解成 nil：不能当成空 denylist fail-open，否则 MCP 会放开全部工具组。
	if names == nil {
		return nil, errors.New("disabled_tools_json must be a JSON array")
	}
	return names, nil
}

func mustJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return "[]"
	}
	return string(raw)
}
