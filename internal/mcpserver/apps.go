package mcpserver

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCP Apps（modelcontextprotocol/ext-apps）允许服务器为工具结果附带一份 HTML 视图：
// 工具描述符用 _meta.ui.resourceUri 指向一个 ui:// 资源，宿主读取该资源并在沙箱 iframe 里渲染，
// 通过 postMessage 上的 JSON-RPC 把 CallToolResult 推给页面。OneSSH 为全部工具各提供一张卡片。
//
// 资源正文必须完全自包含：沙箱 CSP 只允许内联样式与脚本，外链脚本、字体和网络请求一律被拦截，
// 因此 style.css / runtime.js / views/*.js 在启动时被拼进一份 HTML，而不是按 URL 引用。
const (
	appMIMEType = "text/html;profile=mcp-app"
	// 旧版 ChatGPT 只认 openai/outputTemplate，并且要求它指向的资源用 skybridge MIME。
	// 光发别名字段不发对应 MIME，旧客户端会直接拒绝渲染，等于白给了一个兼容承诺。
	appLegacyMIMEType = "text/html+skybridge"
	appLegacyPrefix   = "ui://onessh/legacy/"
	// 旧版 ChatGPT Apps SDK 的别名字段，与标准 ui.* 并存；只认标准字段的宿主会忽略它们。
	appLegacyTemplateKey    = "openai/outputTemplate"
	appLegacyAccessibleKey  = "openai/widgetAccessible"
	appLegacyCSPKey         = "openai/widgetCSP"
	appLegacyBorderKey      = "openai/widgetPrefersBorder"
	appLegacyDescriptionKey = "openai/widgetDescription"
	appInvokingKey          = "openai/toolInvocation/invoking"
	appInvokedKey           = "openai/toolInvocation/invoked"
	// 单份卡片 HTML 的硬上限：宿主要在每次工具调用时下发整页，过大的页面会拖慢渲染。
	appHTMLLimit = 256 << 10
)

//go:embed apps/shell.html apps/style.css apps/runtime.js apps/views/*.js
var appAssets embed.FS

// appBinding 描述一个工具的卡片：View 指向 apps/views/<View>.js，
// Callable 表示允许卡片自己回调该工具（只读导航），会写进 _meta.ui.visibility。
type appBinding struct {
	Tool     string
	View     string
	Title    string
	Summary  string
	Invoking string
	Invoked  string
	Callable bool
}

var appBindings = []appBinding{
	{Tool: "hosts_list", View: "hosts", Title: "主机列表", Summary: "当前令牌可访问的主机、标签与在线状态", Invoking: "正在读取主机列表…", Invoked: "已读取主机列表", Callable: true},
	{Tool: "hosts_manage_list", View: "hosts", Title: "主机配置", Summary: "全部主机的端口、认证方式、指纹与监控开关", Invoking: "正在读取主机配置…", Invoked: "已读取主机配置", Callable: true},
	{Tool: "host_create", View: "hosts", Title: "新增主机", Summary: "新建主机配置的完整结果", Invoking: "正在新增主机…", Invoked: "主机已新增"},
	{Tool: "host_update", View: "hosts", Title: "更新主机", Summary: "替换后的主机配置", Invoking: "正在更新主机…", Invoked: "主机已更新"},
	{Tool: "host_test", View: "hosts", Title: "连通性测试", Summary: "登录主机执行 uptime 的输出与退出码", Invoking: "正在测试连通性…", Invoked: "连通性测试完成"},
	{Tool: "host_reset_fingerprint", View: "hosts", Title: "重置指纹", Summary: "TOFU 指纹重置结果", Invoking: "正在重置指纹…", Invoked: "指纹已重置"},
	{Tool: "host_delete", View: "hosts", Title: "删除主机", Summary: "主机删除结果", Invoking: "正在删除主机…", Invoked: "主机已删除"},

	{Tool: "exec", View: "exec", Title: "命令执行", Summary: "命令输出、退出码、工作目录与截断信息", Invoking: "正在执行命令…", Invoked: "命令已执行"},
	{Tool: "session_env", View: "exec", Title: "会话环境变量", Summary: "持久会话的环境变量列表", Invoking: "正在更新会话环境…", Invoked: "会话环境已更新"},
	{Tool: "output_read", View: "exec", Title: "截断输出", Summary: "命令 artifact 的分页内容", Invoking: "正在读取输出…", Invoked: "输出已读取", Callable: true},
	{Tool: "exec_many", View: "exec", Title: "批量执行", Summary: "多台主机的执行结果汇总", Invoking: "正在批量执行…", Invoked: "批量执行完成"},

	{Tool: "job_start", View: "jobs", Title: "后台任务", Summary: "新启动的后台任务标识与 PID", Invoking: "正在启动后台任务…", Invoked: "后台任务已启动"},
	{Tool: "job_list", View: "jobs", Title: "任务列表", Summary: "当前令牌的后台任务与状态", Invoking: "正在读取任务列表…", Invoked: "已读取任务列表", Callable: true},
	{Tool: "job_status", View: "jobs", Title: "任务状态", Summary: "单个后台任务的运行状态与退出码", Invoking: "正在刷新任务状态…", Invoked: "任务状态已刷新", Callable: true},
	{Tool: "job_logs", View: "jobs", Title: "任务日志", Summary: "后台任务的日志内容", Invoking: "正在读取任务日志…", Invoked: "任务日志已读取", Callable: true},
	{Tool: "job_kill", View: "jobs", Title: "终止任务", Summary: "终止信号发送结果", Invoking: "正在终止任务…", Invoked: "终止信号已发送"},

	{Tool: "file_read", View: "files", Title: "文件内容", Summary: "远程文本文件的分页内容与校验和", Invoking: "正在读取文件…", Invoked: "文件已读取", Callable: true},
	{Tool: "file_write", View: "files", Title: "文件写入", Summary: "整文件覆盖写入的结果", Invoking: "正在写入文件…", Invoked: "文件已写入"},
	{Tool: "file_edit", View: "files", Title: "文件编辑", Summary: "结构化编辑产生的统一 diff", Invoking: "正在编辑文件…", Invoked: "文件已编辑"},
	{Tool: "file_list", View: "files", Title: "目录列表", Summary: "远程目录的一层条目", Invoking: "正在列出目录…", Invoked: "目录已列出", Callable: true},
	{Tool: "file_transfer", View: "files", Title: "文件传输", Summary: "跨主机传输的字节数与校验结果", Invoking: "正在传输文件…", Invoked: "文件传输完成"},

	{Tool: "grep", View: "search", Title: "内容搜索", Summary: "按文件分组的匹配行与上下文", Invoking: "正在搜索内容…", Invoked: "内容搜索完成"},
	{Tool: "find", View: "search", Title: "路径查找", Summary: "按 glob 命中的路径列表", Invoking: "正在查找路径…", Invoked: "路径查找完成"},

	{Tool: "memory_remember", View: "memory", Title: "保存记忆", Summary: "写入长期记忆的结果", Invoking: "正在保存记忆…", Invoked: "记忆已保存"},
	{Tool: "memory_recall", View: "memory", Title: "召回记忆", Summary: "按问题检索到的记忆与评分", Invoking: "正在召回记忆…", Invoked: "记忆已召回", Callable: true},
	{Tool: "memory_list", View: "memory", Title: "记忆列表", Summary: "某个 bank 的记忆分页", Invoking: "正在读取记忆…", Invoked: "记忆已读取", Callable: true},
	{Tool: "memory_update", View: "memory", Title: "更新记忆", Summary: "记忆修改结果", Invoking: "正在更新记忆…", Invoked: "记忆已更新"},
	{Tool: "memory_forget", View: "memory", Title: "删除记忆", Summary: "记忆删除结果", Invoking: "正在删除记忆…", Invoked: "记忆已删除"},
	{Tool: "memory_stats", View: "memory", Title: "记忆统计", Summary: "各 bank 的条数与向量覆盖率", Invoking: "正在统计记忆…", Invoked: "记忆统计完成", Callable: true},
	{Tool: "memory_sleep", View: "memory", Title: "记忆维护", Summary: "去重、衰减与清理的数量", Invoking: "正在整理记忆…", Invoked: "记忆整理完成"},

	{Tool: "host_status", View: "monitor", Title: "资源指标", Summary: "CPU、内存、负载与磁盘用量快照", Invoking: "正在读取资源指标…", Invoked: "资源指标已读取", Callable: true},

	{Tool: "image_view", View: "image", Title: "远程图片", Summary: "缩放后的远程图片与尺寸信息", Invoking: "正在读取图片…", Invoked: "图片已读取"},
}

type appEntry struct {
	binding   appBinding
	uri       string
	legacyURI string
	html      string
}

type appCatalog struct {
	enabled bool
	uiMeta  mcp.Meta
	entries map[string]appEntry
	order   []string
}

func newAppCatalog(enabled bool) (*appCatalog, error) {
	catalog := &appCatalog{enabled: enabled, entries: map[string]appEntry{}}
	if !enabled {
		return catalog, nil
	}
	catalog.uiMeta = appResourceMeta()
	for _, binding := range appBindings {
		html, err := assembleAppHTML(binding)
		if err != nil {
			return nil, err
		}
		if len(html) > appHTMLLimit {
			return nil, fmt.Errorf("卡片 %s 的 HTML 为 %d 字节，超过 %d 上限", binding.Tool, len(html), appHTMLLimit)
		}
		uri := appResourceURI(binding.Tool, html)
		catalog.entries[binding.Tool] = appEntry{
			binding: binding, uri: uri, legacyURI: appLegacyResourceURI(binding.Tool, uri), html: html,
		}
		catalog.order = append(catalog.order, binding.Tool)
	}
	return catalog, nil
}

// appResourceMeta 返回资源侧的标准 ui 元数据与旧版 openai 别名。
// csp 两个域名列表都留空表示卡片不发起任何外部请求，宿主据此收紧沙箱 CSP。
//
// 这里刻意不发布 ui.domain：规范把它描述为「宿主分配的专用沙箱源，格式由宿主决定」，
// 而我们手上只有网关自己的对外地址，两者不是一回事，填错反而可能让宿主分配不出沙箱。
// 卡片正文完全自包含、不读写任何存储，也就没有「需要一个稳定独立源」的理由。
func appResourceMeta() mcp.Meta {
	csp := map[string]any{"connectDomains": []string{}, "resourceDomains": []string{}, "frameDomains": []string{}}
	return mcp.Meta{
		"ui":                    map[string]any{"csp": csp, "prefersBorder": true},
		appLegacyCSPKey:         map[string]any{"connect_domains": []string{}, "resource_domains": []string{}},
		appLegacyBorderKey:      true,
		appLegacyDescriptionKey: "OneSSH 工具结果卡片",
	}
}

// appResourceURI 把 HTML 内容哈希写进 URI。宿主按 URI 缓存卡片正文，
// 内容变了 URI 就变，无需人工维护版本号，也不会让旧卡片留在宿主缓存里。
func appResourceURI(tool, html string) string {
	sum := sha256.Sum256([]byte(html))
	return "ui://onessh/" + tool + "?v=" + hex.EncodeToString(sum[:])[:8]
}

// 旧版 URI 把版本放进路径而不是查询串：它要由一条 URI 模板来匹配，
// 而模板变量不吃 "?"，带查询的地址会匹配不上。
func appLegacyResourceURI(tool, uri string) string {
	version := uri
	if index := strings.LastIndex(uri, "?v="); index >= 0 {
		version = uri[index+3:]
	}
	return appLegacyPrefix + version + "/" + tool
}

func assembleAppHTML(binding appBinding) (string, error) {
	shell, err := appAssets.ReadFile("apps/shell.html")
	if err != nil {
		return "", err
	}
	style, err := appAssets.ReadFile("apps/style.css")
	if err != nil {
		return "", err
	}
	runtime, err := appAssets.ReadFile("apps/runtime.js")
	if err != nil {
		return "", err
	}
	view, err := appAssets.ReadFile("apps/views/" + binding.View + ".js")
	if err != nil {
		return "", fmt.Errorf("卡片 %s 缺少视图 %s: %w", binding.Tool, binding.View, err)
	}
	for name, asset := range map[string][]byte{"style.css": style, "runtime.js": runtime, binding.View + ".js": view} {
		if strings.Contains(strings.ToLower(string(asset)), "</script") || strings.Contains(strings.ToLower(string(asset)), "</style") {
			return "", fmt.Errorf("卡片资产 %s 含有会提前闭合内联块的标记", name)
		}
	}
	html := strings.NewReplacer(
		"{{TOOL}}", binding.Tool,
		"{{TITLE}}", binding.Title,
		"{{STYLE}}", string(style),
		"{{RUNTIME}}", string(runtime),
		"{{VIEW}}", string(view),
	).Replace(string(shell))
	if strings.Contains(html, "{{") {
		return "", fmt.Errorf("卡片 %s 的模板占位符未被完全替换", binding.Tool)
	}
	return html, nil
}

// decorate 在工具注册前写入 _meta。tools/list 与 tools/call 都会带上它，
// 宿主凭 ui.resourceUri 找到卡片，凭 visibility 决定是否允许卡片回调该工具。
func (c *appCatalog) decorate(tool *mcp.Tool) {
	if c == nil || !c.enabled || tool == nil {
		return
	}
	entry, ok := c.entries[tool.Name]
	if !ok {
		return
	}
	visibility := []string{"model"}
	if entry.binding.Callable {
		visibility = append(visibility, "app")
	}
	meta := mcp.Meta{
		"ui": map[string]any{"resourceUri": entry.uri, "visibility": visibility},
		// 指向 skybridge 那一份：认标准字段的宿主走上面的 ui.resourceUri，
		// 只认旧字段的宿主读到的资源 MIME 也和它的预期一致。
		appLegacyTemplateKey:   entry.legacyURI,
		appLegacyAccessibleKey: entry.binding.Callable,
	}
	if entry.binding.Invoking != "" {
		meta[appInvokingKey] = entry.binding.Invoking
	}
	if entry.binding.Invoked != "" {
		meta[appInvokedKey] = entry.binding.Invoked
	}
	for key, value := range tool.Meta {
		meta[key] = value
	}
	tool.Meta = meta
}

func (c *appCatalog) registerResources(server *mcp.Server) {
	if c == nil || !c.enabled || server == nil {
		return
	}
	c.registerLegacyTemplate(server)
	for _, tool := range c.order {
		entry := c.entries[tool]
		server.AddResource(&mcp.Resource{
			URI:         entry.uri,
			Name:        "onessh-app-" + tool,
			Title:       "OneSSH " + entry.binding.Title,
			Description: entry.binding.Summary + "（" + tool + " 工具结果卡片）",
			MIMEType:    appMIMEType,
			Meta:        c.uiMeta,
		}, func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
				URI: entry.uri, MIMEType: appMIMEType, Text: entry.html, Meta: c.uiMeta,
			}}}, nil
		})
	}
}

// AppHTML 供预览画廊与测试取用组装后的卡片正文。
func (c *appCatalog) AppHTML(tool string) (string, bool) {
	if c == nil {
		return "", false
	}
	entry, ok := c.entries[tool]
	return entry.html, ok
}

// 旧版卡片走 URI 模板而不是 32 个资源条目：模板可以按 URI 读取，却不会出现在
// resources/list 里。这样只认旧字段的宿主仍能取到 skybridge 版本，而标准宿主
// 看到的资源清单还是干干净净的 32 条，不会被同一批卡片刷两遍。
func (c *appCatalog) registerLegacyTemplate(server *mcp.Server) {
	server.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: appLegacyPrefix + "{version}/{tool}",
		Name:        "onessh-app-legacy",
		Title:       "OneSSH 卡片（旧版 skybridge）",
		Description: "与 ui://onessh/<工具名> 同一份正文，供只认 openai/outputTemplate 的客户端读取",
		MIMEType:    appLegacyMIMEType,
		Meta:        c.uiMeta,
	}, func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		uri := ""
		if req != nil && req.Params != nil {
			uri = req.Params.URI
		}
		entry, ok := c.legacyEntry(uri)
		if !ok {
			return nil, mcp.ResourceNotFoundError(uri)
		}
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
			URI: entry.legacyURI, MIMEType: appLegacyMIMEType, Text: entry.html, Meta: c.uiMeta,
		}}}, nil
	})
}

// legacyEntry 只接受与当前正文哈希完全一致的旧版 URI：
// 版本对不上说明宿主拿的是缓存里的旧地址，此时返回未找到比回一份新正文更诚实。
func (c *appCatalog) legacyEntry(uri string) (appEntry, bool) {
	rest := strings.TrimPrefix(uri, appLegacyPrefix)
	if rest == uri {
		return appEntry{}, false
	}
	index := strings.Index(rest, "/")
	if index < 0 {
		return appEntry{}, false
	}
	entry, ok := c.entries[rest[index+1:]]
	if !ok || entry.legacyURI != uri {
		return appEntry{}, false
	}
	return entry, true
}
