package mcpserver

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"onessh/internal/execx"
	"onessh/internal/hostmanager"
	"onessh/internal/store"
)

type HostItem struct {
	Name     string   `json:"name"`
	Addr     string   `json:"addr"`
	Username string   `json:"username"`
	Online   bool     `json:"online"`
	Tags     []string `json:"tags"`
}

type HostsOutput struct {
	Hosts []HostItem `json:"hosts"`
}

type HostRefInput struct {
	Host string `json:"host" jsonschema:"SSH 主机名，取自 hosts_manage_list 的 name"`
}

type HostUpdateInput struct {
	Host           string   `json:"host" jsonschema:"要修改的主机当前名称"`
	Name           string   `json:"name" jsonschema:"修改后的主机名，与当前名称相同表示不改名"`
	Addr           string   `json:"addr" jsonschema:"主机地址或 IP"`
	Port           int      `json:"port" jsonschema:"SSH 端口，0 视为 22"`
	Username       string   `json:"username" jsonschema:"SSH 登录用户名"`
	AuthType       string   `json:"auth_type" jsonschema:"认证方式：key 或 password"`
	KeyID          *int64   `json:"key_id,omitempty" jsonschema:"key 认证使用的密钥 ID，auth_type=key 时必填"`
	Password       *string  `json:"password,omitempty" jsonschema:"新的登录密码；auth_type 保持 password 且沿用原密码时可省略"`
	JumpHost       string   `json:"jump_host,omitempty" jsonschema:"跳板主机名，留空表示直连；整体替换语义下省略即清除原跳板配置"`
	MonitorEnabled *bool    `json:"monitor_enabled,omitempty" jsonschema:"是否纳入后台资源监控轮询，省略表示保持原值"`
	Tags           []string `json:"tags,omitempty" jsonschema:"主机标签，用于分组与筛选；整体替换语义下省略即清空标签"`
}

type ManagedHostItem struct {
	ID             int64    `json:"id"`
	Name           string   `json:"name"`
	Addr           string   `json:"addr"`
	Port           int      `json:"port"`
	Username       string   `json:"username"`
	AuthType       string   `json:"auth_type"`
	KeyID          *int64   `json:"key_id"`
	HostKeyFP      *string  `json:"hostkey_fp"`
	JumpHost       *string  `json:"jump_host"`
	MonitorEnabled bool     `json:"monitor_enabled"`
	Tags           []string `json:"tags"`
	CreatedAt      int64    `json:"created_at"`
	Online         bool     `json:"online"`
}

type ManagedHostsOutput struct {
	Hosts []ManagedHostItem `json:"hosts"`
}

func (s *Server) registerHosts() {
	register[Empty, HostsOutput](s, &mcp.Tool{
		Name:        "hosts_list",
		Title:       "列出可用主机",
		Description: "列出当前令牌可访问的 SSH 主机名、地址、登录用户、标签和实时连接状态。其他工具的 host 参数只接受这里返回的 name，开始任何主机相关任务前先调用一次。可按 tags 区分环境或用途后再选择目标主机。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: new(false)},
	}, s.hostsList)
	register[Empty, ManagedHostsOutput](s, &mcp.Tool{
		Name:        "hosts_manage_list",
		Title:       "列出全部主机配置",
		Description: "列出网关内全部 SSH 主机的完整配置：端口、认证方式、密钥 ID、已固定的 TOFU 指纹、监控开关与连接状态。用于主机运维，不代表当前令牌有执行权限；执行类工具的可用主机以 hosts_list 为准。需要 manage_hosts 权限。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: new(false)},
	}, s.hostsManageList)
	register[hostmanager.Input, ManagedHostItem](s, &mcp.Tool{
		Name:        "host_create",
		Title:       "新增主机配置",
		Description: "新增一台 SSH 主机配置。auth_type=key 必须给 key_id，auth_type=password 必须给 password；port 省略按 22 处理，name 必须唯一。只写入配置、不建立连接，创建后用 host_test 验证凭据并固定主机公钥指纹。新主机不会自动加入当前令牌的授权列表。可选 jump_host 指定跳板主机名，连接经该主机中转，支持最多 5 级链但不允许成环。需要 manage_hosts 权限。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: new(false), IdempotentHint: false, OpenWorldHint: new(false)},
	}, s.hostCreate)
	register[HostUpdateInput, ManagedHostItem](s, &mcp.Tool{
		Name:        "host_update",
		Title:       "替换主机配置",
		Description: "整体替换一台 SSH 主机的配置：host 指定当前名称，其余字段是替换后的完整值，未提供的字段按空值处理，因此改一个字段也要把其他字段一并回填（可先用 hosts_manage_list 取当前值）。name 与 host 不同即完成改名，主机记忆与授权按 ID 保留。jump_host 同样按整体替换处理，省略即改回直连。需要 manage_hosts 权限。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: new(true), IdempotentHint: true, OpenWorldHint: new(false)},
	}, s.hostUpdate)
	register[HostRefInput, execx.Result](s, &mcp.Tool{
		Name:        "host_test",
		Title:       "测试主机连通性",
		Description: "连接指定主机并执行只读的 uptime，验证网络、凭据与账号可用性，返回命令输出与退出码。首次连接会记录并固定该主机的公钥指纹（TOFU），之后指纹变化会导致连接被拒。可用于 hosts_list 之外的任意已配置主机。需要 manage_hosts 权限。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: new(false), IdempotentHint: false, OpenWorldHint: new(true)},
	}, s.hostTest)
	register[HostRefInput, OKOutput](s, &mcp.Tool{
		Name:        "host_reset_fingerprint",
		Title:       "重置主机指纹",
		Description: "清除主机已固定的 TOFU 公钥指纹，下次连接重新固定。仅在确认指纹变化来自重装系统、更换主机密钥等可信变更时使用；无法解释的指纹变化可能是中间人攻击，应先排查再重置。需要 manage_hosts 权限。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: new(true), IdempotentHint: true, OpenWorldHint: new(false)},
	}, s.hostResetFingerprint)
	register[HostRefInput, OKOutput](s, &mcp.Tool{
		Name:        "host_delete",
		Title:       "删除主机",
		Description: "删除主机配置及其令牌授权、持久会话、后台任务记录、监控指标和该主机 bank 的全部记忆，不可撤销。被其他主机用作跳板的主机不能删除，需先解除依赖。仅在主机确实下线时使用；只是临时不用请改为关闭 monitor_enabled。需要 manage_hosts 权限。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: new(true), IdempotentHint: false, OpenWorldHint: new(false)},
	}, s.hostDelete)
}

func (s *Server) hostsList(ctx context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, HostsOutput, error) {
	p, ok := FromContext(ctx)
	if !ok {
		return errorResult("unauthorized"), HostsOutput{}, nil
	}
	out := HostsOutput{Hosts: make([]HostItem, 0, len(p.Hosts))}
	for _, host := range p.Hosts {
		tags := host.Tags
		if tags == nil {
			tags = []string{}
		}
		out.Hosts = append(out.Hosts, HostItem{
			Name: host.Name, Addr: net.JoinHostPort(host.Addr, strconv.Itoa(host.Port)),
			Username: host.Username, Online: s.Pool.IsOnline(host.Name), Tags: tags,
		})
	}
	return nil, out, nil
}

func (s *Server) hostsManageList(ctx context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, ManagedHostsOutput, error) {
	if _, err := AuthorizedHostManagement(ctx); err != nil {
		return errorResult(err.Error()), ManagedHostsOutput{}, nil
	}
	hosts, err := s.Store.ListHosts(ctx)
	if err != nil {
		return nil, ManagedHostsOutput{}, err
	}
	out := ManagedHostsOutput{Hosts: make([]ManagedHostItem, 0, len(hosts))}
	for _, host := range hosts {
		out.Hosts = append(out.Hosts, s.managedHostItem(ctx, host))
	}
	return nil, out, nil
}

func (s *Server) hostCreate(ctx context.Context, _ *mcp.CallToolRequest, in hostmanager.Input) (*mcp.CallToolResult, ManagedHostItem, error) {
	if _, err := AuthorizedHostManagement(ctx); err != nil {
		return errorResult(err.Error()), ManagedHostItem{}, nil
	}
	host, err := s.HostManager.Create(ctx, in)
	if err != nil {
		return errorResult(err.Error()), ManagedHostItem{}, nil
	}
	return nil, s.managedHostItem(ctx, host), nil
}

func (s *Server) hostUpdate(ctx context.Context, _ *mcp.CallToolRequest, in HostUpdateInput) (*mcp.CallToolResult, ManagedHostItem, error) {
	if _, err := AuthorizedHostManagement(ctx); err != nil {
		return errorResult(err.Error()), ManagedHostItem{}, nil
	}
	host, err := s.hostByName(ctx, in.Host)
	if err != nil {
		return errorResult(err.Error()), ManagedHostItem{}, nil
	}
	updated, err := s.HostManager.Update(ctx, host.ID, hostmanager.Input{
		Name: in.Name, Addr: in.Addr, Port: in.Port, Username: in.Username, AuthType: in.AuthType,
		KeyID: in.KeyID, Password: in.Password, JumpHost: in.JumpHost, MonitorEnabled: in.MonitorEnabled, Tags: in.Tags,
	})
	if err != nil {
		return errorResult(err.Error()), ManagedHostItem{}, nil
	}
	return nil, s.managedHostItem(ctx, updated), nil
}

func (s *Server) hostTest(ctx context.Context, _ *mcp.CallToolRequest, in HostRefInput) (*mcp.CallToolResult, execx.Result, error) {
	if _, err := AuthorizedHostManagement(ctx); err != nil {
		return errorResult(err.Error()), execx.Result{}, nil
	}
	host, err := s.hostByName(ctx, in.Host)
	if err != nil {
		return errorResult(err.Error()), execx.Result{}, nil
	}
	result, err := s.HostManager.Test(ctx, host.ID, s.Exec)
	if err != nil {
		return errorResult(err.Error()), result, nil
	}
	return nil, result, nil
}

func (s *Server) hostResetFingerprint(ctx context.Context, _ *mcp.CallToolRequest, in HostRefInput) (*mcp.CallToolResult, OKOutput, error) {
	if _, err := AuthorizedHostManagement(ctx); err != nil {
		return errorResult(err.Error()), OKOutput{}, nil
	}
	host, err := s.hostByName(ctx, in.Host)
	if err != nil {
		return errorResult(err.Error()), OKOutput{}, nil
	}
	if err = s.HostManager.ResetFingerprint(ctx, host.ID); err != nil {
		return errorResult(err.Error()), OKOutput{}, nil
	}
	return nil, OKOutput{OK: true}, nil
}

func (s *Server) hostDelete(ctx context.Context, _ *mcp.CallToolRequest, in HostRefInput) (*mcp.CallToolResult, OKOutput, error) {
	if _, err := AuthorizedHostManagement(ctx); err != nil {
		return errorResult(err.Error()), OKOutput{}, nil
	}
	host, err := s.hostByName(ctx, in.Host)
	if err != nil {
		return errorResult(err.Error()), OKOutput{}, nil
	}
	if err = s.HostManager.Delete(ctx, host.ID); err != nil {
		return errorResult(err.Error()), OKOutput{}, nil
	}
	return nil, OKOutput{OK: true}, nil
}

func (s *Server) hostByName(ctx context.Context, name string) (store.Host, error) {
	if strings.TrimSpace(name) == "" {
		return store.Host{}, toolError("host 不能为空")
	}
	host, err := s.Store.GetHostByName(ctx, name)
	if errors.Is(err, sql.ErrNoRows) {
		return store.Host{}, toolError("unknown host: " + name)
	}
	return host, err
}

func (s *Server) managedHostItem(ctx context.Context, host store.Host) ManagedHostItem {
	view := host.View()
	item := ManagedHostItem{
		ID: view.ID, Name: view.Name, Addr: view.Addr, Port: view.Port, Username: view.Username,
		AuthType: view.AuthType, KeyID: view.KeyID, HostKeyFP: view.HostKeyFP,
		MonitorEnabled: view.MonitorEnabled, Tags: view.Tags, CreatedAt: view.CreatedAt, Online: s.Pool.IsOnline(host.Name),
	}
	if host.JumpHostID.Valid {
		if jump, err := s.Store.GetHost(ctx, host.JumpHostID.Int64); err == nil {
			item.JumpHost = &jump.Name
		}
	}
	return item
}
