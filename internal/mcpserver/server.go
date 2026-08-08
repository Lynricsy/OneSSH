package mcpserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"reflect"
	"strconv"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"onessh/internal/events"
	"onessh/internal/sshpool"
	"onessh/internal/store"
)

type Server struct {
	MCP    *mcp.Server
	Store  *store.Store
	Pool   *sshpool.Pool
	Events *events.Bus
}

func New(st *store.Store, pool *sshpool.Pool, bus *events.Bus) *Server {
	s := &Server{Store: st, Pool: pool, Events: bus}
	s.MCP = mcp.NewServer(&mcp.Implementation{Name: "OneSSH", Version: "dev"}, nil)
	register[Empty, HostsOutput](s, &mcp.Tool{Name: "hosts_list", Description: "列出当前令牌可访问的 SSH 主机及连接状态"}, s.hostsList)
	return s
}

type Empty struct{}
type HostItem struct {
	Name     string `json:"name"`
	Addr     string `json:"addr"`
	Username string `json:"username"`
	Online   bool   `json:"online"`
}
type HostsOutput struct {
	Hosts []HostItem `json:"hosts"`
}

func (s *Server) hostsList(ctx context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, HostsOutput, error) {
	p, ok := FromContext(ctx)
	if !ok {
		return errorResult("unauthorized"), HostsOutput{}, nil
	}
	out := HostsOutput{Hosts: make([]HostItem, 0, len(p.Hosts))}
	for _, h := range p.Hosts {
		out.Hosts = append(out.Hosts, HostItem{Name: h.Name, Addr: net.JoinHostPort(h.Addr, strconv.Itoa(h.Port)), Username: h.Username, Online: s.Pool.IsOnline(h.Name)})
	}
	return nil, out, nil
}
func errorResult(message string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: message}}, IsError: true}
}
func register[In, Out any](s *Server, tool *mcp.Tool, handler mcp.ToolHandlerFor[In, Out]) {
	mcp.AddTool(s.MCP, tool, func(ctx context.Context, req *mcp.CallToolRequest, in In) (result *mcp.CallToolResult, out Out, err error) {
		started := time.Now()
		result, out, err = handler(ctx, req, in)
		ok := err == nil && (result == nil || !result.IsError)
		params := redactedJSON(in)
		host := hostOf(in)
		p, _ := FromContext(ctx)
		a := store.Audit{Ts: store.NowAudit(), Tool: tool.Name, ParamsJSON: params, OK: ok, DurationMS: time.Since(started).Milliseconds()}
		if p.Token.ID != 0 {
			a.TokenID = sql.NullInt64{Int64: p.Token.ID, Valid: true}
		}
		if host != "" {
			a.Host = sql.NullString{String: host, Valid: true}
		}
		if raw, e := json.Marshal(out); e == nil {
			a.BytesOut = int64(len(raw))
		}
		_ = s.Store.AddAudit(context.Background(), a)
		s.Events.Publish("tool_call", map[string]any{"tool": tool.Name, "host": host, "ok": ok, "duration_ms": a.DurationMS})
		return
	})
}
func redactedJSON(v any) string {
	var raw any
	b, _ := json.Marshal(v)
	_ = json.Unmarshal(b, &raw)
	redact(raw)
	out, _ := json.Marshal(raw)
	return string(out)
}
func redact(v any) {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			if k == "content" || k == "edits" {
				b, _ := json.Marshal(val)
				x[k] = fmt.Sprintf("<len=%d>", len(b))
			} else {
				redact(val)
			}
		}
	case []any:
		for _, v := range x {
			redact(v)
		}
	}
}
func hostOf(v any) string {
	rv := reflect.Indirect(reflect.ValueOf(v))
	if rv.IsValid() && rv.Kind() == reflect.Struct {
		f := rv.FieldByName("Host")
		if f.IsValid() && f.Kind() == reflect.String {
			return f.String()
		}
	}
	return ""
}
