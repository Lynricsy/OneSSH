package mcpserver

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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

const serverInstructions = `OneSSH 是受主机权限控制的 SSH 运维网关：通过它连接远程主机、执行命令、读写文件、跑后台任务、看资源指标，并在跨会话的记忆库里积累运维知识。

【主机与权限】所有工具的 host 参数只接受 hosts_list 返回的名称，任务开始前先调用一次 hosts_list 确认目标主机与在线状态；名称不在授权列表时会返回 host not authorized。hosts_manage_list、host_create、host_update、host_test、host_reset_fingerprint、host_delete 属于全局配置管理，需要独立的 manage_hosts 权限，且该权限不会扩大命令与文件工具的可访问主机范围。主机可配置 jump_host 跳板，连接自动经跳板隧道建立，对命令与文件工具透明。

【优先用专用工具，不要拿 exec 兜底】搜内容用 grep，找路径用 find，看目录用 file_list，读文件用 file_read，整文件覆盖用 file_write，局部改写用 file_edit，跨主机复制用 file_transfer，看 CPU/内存/磁盘用 host_status，看图片用 image_view。这些工具返回结构化结果，并已处理引号转义、超时、大小上限与输出截断；用 exec 拼 cat/sed/scp/grep 更容易踩到引号与转义问题，只有在没有对应专用工具时才用 exec。

【执行】exec 是同步调用，timeout_s 上限 600 秒，适合秒级到分钟级的命令。预计更久、或希望连接中断后继续运行的，用 job_start 起后台任务，再用 job_status 判断是否结束、job_logs 增量看日志、必要时 job_kill 终止。exec 的工作目录按 host+session 持久保存，cd 之后下一次 exec 仍在该目录；环境变量用 session_env 设置。互不相干的并行工作请用不同 session 标签，避免彼此改动 cwd。输出被截断时结果里会带 artifact_id，用 output_read 分页或正则过滤，不要重跑命令再加 head/tail。同一条命令要在多台主机上跑用 exec_many。

【修改远程状态要谨慎】删除、覆盖、重启服务、改防火墙这类破坏性操作前，先读取现状确认目标正确。file_edit 传上一次 file_read 返回的 expected_sha256 开启乐观锁，避免覆盖他人的并发修改；file_write 是整文件覆盖，改配置优先 file_edit。命令里不要内联明文密码或私钥；所有工具调用都会写入网关审计日志。

【记忆】执行涉及某台主机的任务前，如果历史部署路径、服务拓扑、故障经验或运维约束可能相关，先调用 memory_recall，并用 host 指定目标主机、用 query 描述当前具体问题；召回内容可能过期，不得替代现场文件读取、命令输出或监控数据的验证。memory_recall 没有结果时继续正常调查，不得把“没有记忆”等同于事实不存在。

确认一个以后仍有价值的事实、解决方案或踩坑记录后，调用 memory_remember 写成简洁、自包含、便于检索的结论，并存入对应主机 bank；只有确实跨主机通用的规则才写入全局 bank。不得保存密码、私钥、访问令牌或其他秘密，不保存瞬时命令输出、未验证猜测或低价值重复信息。推断内容使用 veracity=inferred，工具直接观测的事实使用 veracity=tool，并按长期价值设置 importance。

事实发生变化时优先调用 memory_update 修正原记录；确定记录已经错误、失效或不应保留时才调用 memory_forget。memory_list 与 memory_stats 用于核对某个 bank 的内容与规模。memory_sleep 是按 bank 执行的维护工具，只在需要整理记忆时使用，不必在每次任务中调用。`

// New 的 publicURL 必须是已规范化的来源地址（无尾斜杠、无路径），由 oauthserver.New 校验后传入；
// 这里不再做第二次归一，避免同一份规则出现两套实现。
func New(st *store.Store, pool *sshpool.Pool, bus *events.Bus, hosts *hostmanager.Manager, memory *memoryx.Engine, dataDir, publicURL string, pollInterval time.Duration, searchHelper bool) *Server {
	s := &Server{Store: st, Pool: pool, Events: bus, HostManager: hosts, Memory: memory, Exec: execx.New(dataDir)}
	s.Jobs = jobs.New(st, pool, s.Exec, bus)
	s.Files = files.New(pool, s.Exec)
	s.Monitor = monitor.New(st, pool, s.Exec, pollInterval)
	s.MCP = newProtocolServer(publicURL)
	s.registerHosts()
	s.registerExec(s.Exec)
	s.registerJobs(s.Jobs)
	s.registerFiles(s.Files)
	s.registerSearch(searchx.New(pool, s.Files.Clients, searchHelper))
	s.registerMemory()
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

func newProtocolServer(publicURL string) *mcp.Server {
	return mcp.NewServer(serverInfo(publicURL), &mcp.ServerOptions{Instructions: serverInstructions})
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
			a.TokenName = sql.NullString{String: p.Token.Name, Valid: true}
		}
		if host != "" {
			a.Host = sql.NullString{String: host, Valid: true}
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
	for _, key := range []string{"query", "pattern", "content", "job_id", "artifact_id"} {
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
