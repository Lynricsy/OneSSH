package mcpserver

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"onessh/internal/events"
	"onessh/internal/execx"
	"onessh/internal/files"
	"onessh/internal/hostmanager"
	"onessh/internal/jobs"
	"onessh/internal/memoryx"
	"onessh/internal/monitor"
	"onessh/internal/searchx"
	"onessh/internal/sshpool"
	"onessh/internal/store"
)

type Server struct {
	MCP         *mcp.Server
	Store       *store.Store
	Pool        *sshpool.Pool
	Events      *events.Bus
	HostManager *hostmanager.Manager
	Exec        *execx.Runner
	Files       *files.Manager
	Jobs        *jobs.Manager
	Monitor     *monitor.Manager
	Memory      *memoryx.Engine
}

// New 的 publicURL 必须是已规范化的来源地址（无尾斜杠、无路径），由 oauthserver.New 校验后传入；
// 这里不再做第二次归一，避免同一份规则出现两套实现。
func New(st *store.Store, pool *sshpool.Pool, bus *events.Bus, hosts *hostmanager.Manager, memory *memoryx.Engine, dataDir, publicURL string, pollInterval time.Duration) *Server {
	s := &Server{Store: st, Pool: pool, Events: bus, HostManager: hosts, Memory: memory, Exec: execx.New(dataDir)}
	s.Jobs = jobs.New(st, pool, s.Exec, bus)
	s.Files = files.New(pool, s.Exec)
	s.Monitor = monitor.New(st, pool, s.Exec, pollInterval)
	s.MCP = mcp.NewServer(serverInfo(publicURL), nil)
	s.registerHosts()
	s.registerExec(s.Exec)
	s.registerJobs(s.Jobs)
	s.registerFiles(s.Files)
	s.registerSearch(searchx.New(pool))
	s.registerImage(s.Files)
	s.registerMonitor(s.Monitor)
	s.registerFanout()
	s.Monitor.Start(context.Background())
	return s
}

// serverInfo 组装 initialize 回给客户端的 serverInfo。
//
// 图标走 spec 2025-11-25 起的 Implementation.icons。选 PNG 不是因为 SVG 不合规——服务器发 SVG
// 完全合法——而是互操作性：规范只要求「支持渲染图标的客户端」必须认 image/png 和 image/jpeg，
// SVG 与 WebP 仅为 SHOULD，且 SVG 可内嵌可执行脚本，客户端有理由拒绝渲染。
//
// src 要求是绝对 URI，而 initialize 时拿不到 http.Request，无法像 OAuth 元数据那样按请求推导；
// 因此未配置 ONESSH_PUBLIC_URL 时宁可不发图标，也不发一个客户端解析不了的相对路径。
// 该地址与 /mcp 同源，满足规范「图标 URL 应来自同域或可信域」的建议。
func serverInfo(publicURL string) *mcp.Implementation {
	info := &mcp.Implementation{Name: "OneSSH", Version: "dev"}
	if publicURL != "" {
		info.Icons = []mcp.Icon{{
			Source:   publicURL + "/logo.png",
			MIMEType: "image/png",
			Sizes:    []string{"256x256"},
		}}
	}
	return info
}
func (s *Server) Close() {
	s.Monitor.Stop()
	s.Files.Clients.Close()
}

type Empty struct{}

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
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(raw)
	return string(bytes.TrimSuffix(out.Bytes(), []byte("\n")))
}
func redact(v any) {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			switch k {
			case "password":
				x[k] = "<redacted>"
			case "content", "edits":
				b, _ := json.Marshal(val)
				x[k] = fmt.Sprintf("<len=%d>", len(b))
			default:
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
		for _, name := range []string{"Host", "Name"} {
			f := rv.FieldByName(name)
			if f.IsValid() && f.Kind() == reflect.String && f.String() != "" {
				return f.String()
			}
		}
	}
	return ""
}
