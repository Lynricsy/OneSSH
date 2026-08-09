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
	Name     string `json:"name"`
	Addr     string `json:"addr"`
	Username string `json:"username"`
	Online   bool   `json:"online"`
}

type HostsOutput struct {
	Hosts []HostItem `json:"hosts"`
}

type HostRefInput struct {
	Host string `json:"host"`
}

type HostUpdateInput struct {
	Host           string  `json:"host"`
	Name           string  `json:"name"`
	Addr           string  `json:"addr"`
	Port           int     `json:"port"`
	Username       string  `json:"username"`
	AuthType       string  `json:"auth_type"`
	KeyID          *int64  `json:"key_id,omitempty"`
	Password       *string `json:"password,omitempty"`
	MonitorEnabled *bool   `json:"monitor_enabled,omitempty"`
}

type ManagedHostItem struct {
	ID             int64   `json:"id"`
	Name           string  `json:"name"`
	Addr           string  `json:"addr"`
	Port           int     `json:"port"`
	Username       string  `json:"username"`
	AuthType       string  `json:"auth_type"`
	KeyID          *int64  `json:"key_id"`
	HostKeyFP      *string `json:"hostkey_fp"`
	MonitorEnabled bool    `json:"monitor_enabled"`
	CreatedAt      int64   `json:"created_at"`
	Online         bool    `json:"online"`
}

type ManagedHostsOutput struct {
	Hosts []ManagedHostItem `json:"hosts"`
}

func (s *Server) registerHosts() {
	register[Empty, HostsOutput](s, &mcp.Tool{Name: "hosts_list", Description: "列出当前令牌可访问的 SSH 主机及连接状态"}, s.hostsList)
	register[Empty, ManagedHostsOutput](s, &mcp.Tool{
		Name:        "hosts_manage_list",
		Description: "列出全部 SSH 主机配置及连接状态；需要主机管理权限",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: new(false)},
	}, s.hostsManageList)
	register[hostmanager.Input, ManagedHostItem](s, &mcp.Tool{
		Name:        "host_create",
		Description: "新增 SSH 主机配置；保存配置但不自动连接",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: new(false), IdempotentHint: false, OpenWorldHint: new(false)},
	}, s.hostCreate)
	register[HostUpdateInput, ManagedHostItem](s, &mcp.Tool{
		Name:        "host_update",
		Description: "完整替换 SSH 主机配置；host 指定旧名称，name 可提交新名称",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: new(true), IdempotentHint: true, OpenWorldHint: new(false)},
	}, s.hostUpdate)
	register[HostRefInput, execx.Result](s, &mcp.Tool{
		Name:        "host_test",
		Description: "连接任意 SSH 主机并执行只读 uptime；首次连接会固定 TOFU 指纹",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: new(false), IdempotentHint: false, OpenWorldHint: new(true)},
	}, s.hostTest)
	register[HostRefInput, OKOutput](s, &mcp.Tool{
		Name:        "host_reset_fingerprint",
		Description: "清除 SSH 主机的 TOFU 指纹；下次连接将重新固定",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: new(true), IdempotentHint: true, OpenWorldHint: new(false)},
	}, s.hostResetFingerprint)
	register[HostRefInput, OKOutput](s, &mcp.Tool{
		Name:        "host_delete",
		Description: "删除 SSH 主机及关联授权、会话、已结束任务和指标",
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
		out.Hosts = append(out.Hosts, HostItem{Name: host.Name, Addr: net.JoinHostPort(host.Addr, strconv.Itoa(host.Port)), Username: host.Username, Online: s.Pool.IsOnline(host.Name)})
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
		out.Hosts = append(out.Hosts, s.managedHostItem(host))
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
	return nil, s.managedHostItem(host), nil
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
		KeyID: in.KeyID, Password: in.Password, MonitorEnabled: in.MonitorEnabled,
	})
	if err != nil {
		return errorResult(err.Error()), ManagedHostItem{}, nil
	}
	return nil, s.managedHostItem(updated), nil
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

func (s *Server) managedHostItem(host store.Host) ManagedHostItem {
	view := host.View()
	return ManagedHostItem{
		ID: view.ID, Name: view.Name, Addr: view.Addr, Port: view.Port, Username: view.Username,
		AuthType: view.AuthType, KeyID: view.KeyID, HostKeyFP: view.HostKeyFP,
		MonitorEnabled: view.MonitorEnabled, CreatedAt: view.CreatedAt, Online: s.Pool.IsOnline(host.Name),
	}
}
