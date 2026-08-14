package hostmanager

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
	"onessh/internal/cryptox"
	"onessh/internal/execx"
	"onessh/internal/sshpool"
	"onessh/internal/store"
)

// maxJumpChain 须与 sshpool 中的同名常量保持一致。
const maxJumpChain = 5

const (
	maxTagLength = 32
	maxTagsCount = 16
)

type Input struct {
	Name           string   `json:"name" jsonschema:"主机名，网关内唯一，后续所有工具用它引用这台主机"`
	Addr           string   `json:"addr" jsonschema:"主机地址或 IP"`
	Port           int      `json:"port" jsonschema:"SSH 端口，省略或 0 表示 22"`
	Username       string   `json:"username" jsonschema:"SSH 登录用户名"`
	AuthType       string   `json:"auth_type" jsonschema:"认证方式：key 或 password"`
	KeyID          *int64   `json:"key_id,omitempty" jsonschema:"key 认证使用的密钥 ID，auth_type=key 时必填"`
	Password       *string  `json:"password,omitempty" jsonschema:"登录密码；auth_type 为 password 时必填，只写不可读，审计中固定脱敏"`
	JumpHost       string   `json:"jump_host,omitempty" jsonschema:"跳板主机名（可选）：连接时先经该主机建立隧道再连目标，留空表示直连。跳板主机必须已存在；链路最多 5 级且不能成环"`
	MonitorEnabled *bool    `json:"monitor_enabled,omitempty" jsonschema:"是否纳入后台资源监控轮询，新建时默认开启"`
	Tags           []string `json:"tags,omitempty" jsonschema:"主机标签，用于分组与筛选；自动去空白去重，单个最长 32 字符，最多 16 个"`
}

type ErrorKind uint8

const (
	ErrorInvalid ErrorKind = iota + 1
	ErrorNotFound
	ErrorConflict
	ErrorConnection
	ErrorInternal
)

type Error struct {
	Kind ErrorKind
	Err  error
}

func (e *Error) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func KindOf(err error) ErrorKind {
	var target *Error
	if errors.As(err, &target) {
		return target.Kind
	}
	return ErrorInternal
}

type Manager struct {
	store *store.Store
	box   *cryptox.Box
	pool  *sshpool.Pool
}

func New(st *store.Store, box *cryptox.Box, pool *sshpool.Pool) *Manager {
	return &Manager{store: st, box: box, pool: pool}
}

func (m *Manager) Create(ctx context.Context, in Input) (store.Host, error) {
	host, err := m.hostFromInput(ctx, store.Host{}, in, false)
	if err != nil {
		return store.Host{}, err
	}
	host, err = m.store.CreateHost(ctx, host)
	if err != nil {
		return store.Host{}, classifyStoreWrite(err)
	}
	return host, nil
}

func (m *Manager) Update(ctx context.Context, id int64, in Input) (store.Host, error) {
	old, err := m.store.GetHost(ctx, id)
	if err != nil {
		return store.Host{}, classifyLookup(err)
	}
	host, err := m.hostFromInput(ctx, old, in, true)
	if err != nil {
		return store.Host{}, err
	}
	if err = m.store.UpdateHost(ctx, host); err != nil {
		return store.Host{}, classifyStoreWrite(err)
	}
	m.pool.Invalidate(old.Name)
	if host.Name != old.Name {
		m.pool.Invalidate(host.Name)
	}
	if names, err := m.store.JumpDependentNames(ctx, old.ID); err == nil {
		for _, name := range names {
			m.pool.Invalidate(name)
		}
	}
	return host, nil
}

func (m *Manager) Delete(ctx context.Context, id int64) error {
	host, err := m.store.GetHost(ctx, id)
	if err != nil {
		return classifyLookup(err)
	}
	m.pool.Invalidate(host.Name)
	if err = m.store.DeleteHost(ctx, id); err != nil {
		switch {
		case errors.Is(err, store.ErrHostHasRunningJobs):
			return &Error{Kind: ErrorConflict, Err: err}
		case errors.Is(err, store.ErrHostIsJumpHost):
			return &Error{Kind: ErrorConflict, Err: err}
		case errors.Is(err, sql.ErrNoRows):
			return classifyLookup(err)
		default:
			return &Error{Kind: ErrorInternal, Err: err}
		}
	}
	return nil
}

func (m *Manager) Test(ctx context.Context, id int64, runner *execx.Runner) (execx.Result, error) {
	host, err := m.store.GetHost(ctx, id)
	if err != nil {
		return execx.Result{}, classifyLookup(err)
	}
	m.pool.Invalidate(host.Name)
	client, err := m.pool.Get(ctx, host.Name)
	if err != nil {
		return execx.Result{}, &Error{Kind: ErrorConnection, Err: err}
	}
	result, err := runner.Run(ctx, client, "uptime", "~", nil, execx.Options{Timeout: 15 * time.Second, MaxLines: 20})
	if err != nil {
		return result, &Error{Kind: ErrorConnection, Err: err}
	}
	return result, nil
}

func (m *Manager) ResetFingerprint(ctx context.Context, id int64) error {
	host, err := m.store.GetHost(ctx, id)
	if err != nil {
		return classifyLookup(err)
	}
	if err = m.store.UpdateHostFingerprint(ctx, id, nil); err != nil {
		return &Error{Kind: ErrorInternal, Err: err}
	}
	m.pool.Invalidate(host.Name)
	return nil
}

func (m *Manager) hostFromInput(ctx context.Context, old store.Host, in Input, updating bool) (store.Host, error) {
	host := old
	host.Name = strings.TrimSpace(in.Name)
	host.Addr = strings.TrimSpace(in.Addr)
	host.Port = in.Port
	if host.Port == 0 {
		host.Port = 22
	}
	host.Username = strings.TrimSpace(in.Username)
	host.AuthType = in.AuthType
	if in.MonitorEnabled != nil {
		host.MonitorEnabled = *in.MonitorEnabled
	} else if !updating {
		host.MonitorEnabled = true
	}
	if host.Name == "" || host.Addr == "" || host.Username == "" {
		return store.Host{}, invalid("name、addr、username 不能为空")
	}
	if host.Port < 1 || host.Port > 65535 {
		return store.Host{}, invalid("port 必须在 1 到 65535 之间")
	}
	switch host.AuthType {
	case "key":
		if in.KeyID == nil {
			return store.Host{}, invalid("key 认证必须提供 key_id")
		}
		if _, err := m.store.GetKey(ctx, *in.KeyID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return store.Host{}, invalid("key_id 不存在")
			}
			return store.Host{}, &Error{Kind: ErrorInternal, Err: err}
		}
		host.KeyID = sql.NullInt64{Int64: *in.KeyID, Valid: true}
		host.PasswordEnc = nil
	case "password":
		host.KeyID = sql.NullInt64{}
		if in.Password != nil {
			if *in.Password == "" {
				return store.Host{}, invalid("password 认证必须提供 password")
			}
			plain := []byte(*in.Password)
			enc, err := m.box.Seal(plain)
			clear(plain)
			if err != nil {
				return store.Host{}, &Error{Kind: ErrorInternal, Err: err}
			}
			host.PasswordEnc = enc
		} else if !updating || old.AuthType != "password" || len(old.PasswordEnc) == 0 {
			return store.Host{}, invalid("password 认证必须提供 password")
		}
	default:
		return store.Host{}, invalid("auth_type 必须是 key 或 password")
	}
	host.JumpHostID = sql.NullInt64{}
	if name := strings.TrimSpace(in.JumpHost); name != "" {
		jump, err := m.store.GetHostByName(ctx, name)
		if errors.Is(err, sql.ErrNoRows) {
			return store.Host{}, invalid("jump_host 不存在: " + name)
		}
		if err != nil {
			return store.Host{}, &Error{Kind: ErrorInternal, Err: err}
		}
		seen := make(map[int64]bool)
		cur := jump
		for depth := 1; ; depth++ {
			if updating && cur.ID == old.ID {
				return store.Host{}, invalid("跳板链存在循环")
			}
			if seen[cur.ID] {
				return store.Host{}, invalid("跳板链存在循环")
			}
			seen[cur.ID] = true
			if depth > maxJumpChain {
				return store.Host{}, invalid(fmt.Sprintf("跳板链超过 %d 级", maxJumpChain))
			}
			if !cur.JumpHostID.Valid {
				break
			}
			if cur, err = m.store.GetHost(ctx, cur.JumpHostID.Int64); err != nil {
				return store.Host{}, &Error{Kind: ErrorInternal, Err: err}
			}
		}
		host.JumpHostID = sql.NullInt64{Int64: jump.ID, Valid: true}
	}
	tags, err := normalizeTags(in.Tags)
	if err != nil {
		return store.Host{}, err
	}
	host.Tags = tags
	return host, nil
}

// normalizeTags 归一化标签：去首尾空白、丢弃空串、按首次出现顺序去重，并限制长度与数量。
func normalizeTags(raw []string) ([]string, error) {
	tags := make([]string, 0, len(raw))
	seen := make(map[string]bool, len(raw))
	for _, tag := range raw {
		tag = strings.TrimSpace(tag)
		if tag == "" || seen[tag] {
			continue
		}
		if len([]rune(tag)) > maxTagLength {
			return nil, invalid(fmt.Sprintf("标签 %q 超过 %d 字符", tag, maxTagLength))
		}
		seen[tag] = true
		tags = append(tags, tag)
	}
	if len(tags) > maxTagsCount {
		return nil, invalid(fmt.Sprintf("标签最多 %d 个", maxTagsCount))
	}
	return tags, nil
}

func invalid(message string) error {
	return &Error{Kind: ErrorInvalid, Err: errors.New(message)}
}

func classifyLookup(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return &Error{Kind: ErrorNotFound, Err: errors.New("主机不存在")}
	}
	return &Error{Kind: ErrorInternal, Err: err}
}

func classifyStoreWrite(err error) error {
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		switch sqliteErr.Code() {
		case sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY, sqlite3.SQLITE_CONSTRAINT_UNIQUE:
			return &Error{Kind: ErrorConflict, Err: err}
		}
	}
	return &Error{Kind: ErrorInternal, Err: err}
}
