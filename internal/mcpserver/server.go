package mcpserver

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"reflect"
	"strings"
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
	"onessh/internal/toolgroups"
)

type Server struct {
	MCP           *mcp.Server
	Store         *store.Store
	Pool          *sshpool.Pool
	Events        *events.Bus
	HostManager   *hostmanager.Manager
	Exec          *execx.Runner
	Files         *files.Manager
	Jobs          *jobs.Manager
	Monitor       *monitor.Manager
	Memory        *memoryx.Engine
	apps          *appCatalog
	disabledTools toolgroups.Disabled
}

// Options 收拢 New 的可调参数：按组关闭工具属于实例级配置，再往位置参数上堆会让调用点
// 变成一串没有名字的字面量。
type Options struct {
	DataDir string
	// PublicURL 必须是已规范化的来源地址（无尾斜杠、无路径），由 oauthserver.New 校验后传入；
	// 这里不再做第二次归一，避免同一份规则出现两套实现。
	PublicURL    string
	PollInterval time.Duration
	SearchHelper bool
	// MCPApps 控制是否发布 MCP Apps 卡片资源。
	MCPApps bool
	// DisabledTools 列出不对外暴露的工具组，同时决定服务器提示词里能提到哪些工具。
	DisabledTools toolgroups.Disabled
}

func New(st *store.Store, pool *sshpool.Pool, bus *events.Bus, hosts *hostmanager.Manager, memory *memoryx.Engine, opts Options) *Server {
	s := &Server{Store: st, Pool: pool, Events: bus, HostManager: hosts, Memory: memory, Exec: execx.New(opts.DataDir), disabledTools: opts.DisabledTools}
	if err := st.RecoverInterruptedCommandRuns(context.Background(), time.Now().UnixMilli()); err != nil {
		log.Printf("恢复中断的命令执行记录失败: %v", err)
	}
	s.Jobs = jobs.New(st, pool, s.Exec, bus)
	s.Files = files.New(pool, s.Exec)
	s.Monitor = monitor.New(st, pool, s.Exec, opts.PollInterval)
	s.MCP = newProtocolServer(opts.PublicURL, serverInstructions(opts.DisabledTools))
	// 卡片资产在编译期内嵌，组装失败只可能是资产本身有问题，属于必须立刻暴露的构建错误。
	apps, err := newAppCatalog(opts.MCPApps, opts.DisabledTools)
	if err != nil {
		log.Fatalf("组装 MCP Apps 卡片失败: %v", err)
	}
	s.apps = apps
	// 监控采集、文件与任务管理器始终构建：WebUI 的 REST 接口依赖它们，关闭的只是 MCP 暴露面。
	on := opts.DisabledTools.Enabled
	if on(toolgroups.Hosts) {
		s.registerHosts()
	}
	if on(toolgroups.Exec) {
		s.registerExec(s.Exec)
	}
	if on(toolgroups.Jobs) {
		s.registerJobs(s.Jobs)
	}
	if on(toolgroups.Files) {
		s.registerFiles(s.Files)
	}
	if on(toolgroups.Search) {
		s.registerSearch(searchx.New(pool, s.Files.Clients, opts.SearchHelper))
	}
	if on(toolgroups.Memory) {
		s.registerMemory()
	}
	if on(toolgroups.Image) {
		s.registerImage(s.Files)
	}
	if on(toolgroups.Monitor) {
		s.registerMonitor(s.Monitor)
	}
	if on(toolgroups.Fanout) {
		s.registerFanout()
	}
	s.apps.registerResources(s.MCP)
	s.installTokenDenylist()
	if names := opts.DisabledTools.Names(); len(names) > 0 {
		log.Printf("已禁用 MCP 工具组: %s", strings.Join(names, ","))
	}
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

func newProtocolServer(publicURL, instructions string) *mcp.Server {
	return mcp.NewServer(serverInfo(publicURL), &mcp.ServerOptions{Instructions: instructions})
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
	s.apps.decorate(tool)
	mcp.AddTool(s.MCP, tool, func(ctx context.Context, req *mcp.CallToolRequest, in In) (result *mcp.CallToolResult, out Out, err error) {
		started := time.Now()
		result, out, err = handler(ctx, req, in)
		ok := err == nil && (result == nil || !result.IsError)
		var exitCode *int64
		if outcome, found := any(out).(auditOutcome); found && ok {
			ok, exitCode = outcome.auditOutcome()
		}
		params := redactedJSON(in)
		host := hostOf(in)
		p, _ := FromContext(ctx)
		a := store.Audit{Ts: store.NowAudit(), Tool: tool.Name, ParamsJSON: params, OK: ok, DurationMS: time.Since(started).Milliseconds()}
		if p.Token.ID != 0 {
			a.TokenID = sql.NullInt64{Int64: p.Token.ID, Valid: true}
			a.TokenName = sql.NullString{String: p.Token.Name, Valid: true}
		}
		if host != "" {
			a.Host = sql.NullString{String: host, Valid: true}
		}
		if exitCode != nil {
			a.ExitCode = sql.NullInt64{Int64: *exitCode, Valid: true}
		}
		if linked, found := any(out).(auditCommandRuns); found {
			a.RunIDs = linked.auditCommandRunIDs()
		}
		if raw, e := json.Marshal(out); e == nil {
			a.BytesOut = int64(len(raw))
		}
		_ = s.Store.AddAudit(context.Background(), a)
		event := map[string]any{"tool": tool.Name, "host": host, "ok": ok, "duration_ms": a.DurationMS}
		if summary := callSummary(in); summary != "" {
			event["summary"] = summary
		}
		s.Events.Publish("tool_call", event)
		return
	})
}

type auditOutcome interface {
	auditOutcome() (ok bool, exitCode *int64)
}

type auditCommandRuns interface {
	auditCommandRunIDs() []string
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

// callSummary 从工具入参抽出活动流上一眼能看懂的摘要。优先 command，其次 path / 搜索式 / 传输路径。
// 实时事件只带这段短文本，完整参数仍在审计的 params_json 里。
func callSummary(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	var raw any
	if err = json.Unmarshal(b, &raw); err != nil {
		return ""
	}
	return truncateSummary(summarizeParams(raw))
}

func summarizeParams(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	if command := stringField(m, "command"); command != "" {
		return command
	}
	path := stringField(m, "path")
	pattern := stringField(m, "pattern")
	if path != "" && pattern != "" {
		return pattern + "  " + path
	}
	if path != "" {
		return path
	}
	src := stringField(m, "src_path")
	dst := stringField(m, "dst_path")
	if src != "" && dst != "" {
		return src + " → " + dst
	}
	if src != "" {
		return src
	}
	for _, key := range []string{"query", "pattern", "job_id", "artifact_id"} {
		if value := stringField(m, key); value != "" {
			return value
		}
	}
	return ""
}

func stringField(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return strings.TrimSpace(s)
}

func truncateSummary(s string) string {
	const max = 240
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}
