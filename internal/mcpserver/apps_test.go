package mcpserver

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"onessh/internal/cryptox"
	"onessh/internal/events"
	"onessh/internal/hostmanager"
	"onessh/internal/memoryx"
	"onessh/internal/sshpool"
	"onessh/internal/store"
)

// newAppTestSession 起一个真实的 in-memory MCP 会话，从客户端角度观察工具与资源，
// 这样测的是宿主真正能看到的协议输出，而不是内部数据结构。
func newAppTestSession(t *testing.T, publicURL string, mcpApps bool) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, err := cryptox.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	pool := sshpool.New(st, box)
	t.Cleanup(pool.Close)
	server := New(st, pool, events.New(), hostmanager.New(st, box, pool), memoryx.New(st, memoryx.EmbeddingConfig{}), dir, publicURL, 0, true, mcpApps)
	t.Cleanup(server.Close)

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.MCP.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { serverSession.Close() })
	session, err := mcp.NewClient(&mcp.Implementation{Name: "apps-test", Version: "1"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

func listAppTools(t *testing.T, session *mcp.ClientSession) map[string]*mcp.Tool {
	t.Helper()
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]*mcp.Tool, len(tools.Tools))
	for _, tool := range tools.Tools {
		out[tool.Name] = tool
	}
	return out
}

func listAppResources(t *testing.T, session *mcp.ClientSession) map[string]*mcp.Resource {
	t.Helper()
	resources, err := session.ListResources(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]*mcp.Resource, len(resources.Resources))
	for _, resource := range resources.Resources {
		out[resource.URI] = resource
	}
	return out
}

// metaStrings 把经 JSON 传输后的元数据统一成字符串切片：
// 服务端写入的是 []string，客户端收到的是 []any，测试要覆盖真实的客户端视角。
func metaStrings(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil
			}
			out = append(out, text)
		}
		return out
	default:
		return nil
	}
}

func toolUIMeta(t *testing.T, tool *mcp.Tool) map[string]any {
	t.Helper()
	ui, ok := tool.Meta["ui"].(map[string]any)
	if !ok {
		t.Fatalf("%s 缺少 _meta.ui：%#v", tool.Name, tool.Meta)
	}
	return ui
}

// 每新增一个工具就必须为它配一张卡片，否则同一个服务器里会出现「有的工具有 UI、有的没有」的割裂体验。
func TestAppBindingsCoverEveryTool(t *testing.T) {
	session := newAppTestSession(t, "", true)
	tools := listAppTools(t, session)
	bound := make(map[string]bool, len(appBindings))
	for _, binding := range appBindings {
		if bound[binding.Tool] {
			t.Fatalf("卡片绑定表里 %s 重复", binding.Tool)
		}
		bound[binding.Tool] = true
		if tools[binding.Tool] == nil {
			t.Errorf("绑定表里的 %s 并未注册为工具", binding.Tool)
		}
	}
	for name := range tools {
		if !bound[name] {
			t.Errorf("工具 %s 没有对应的卡片绑定", name)
		}
	}
}

func TestAppsExposeResourceForEveryTool(t *testing.T) {
	const publicURL = "https://ssh.example.com"
	session := newAppTestSession(t, publicURL, true)
	tools := listAppTools(t, session)
	resources := listAppResources(t, session)
	if len(resources) != len(tools) {
		t.Fatalf("资源数 = %d，工具数 = %d，应当一一对应", len(resources), len(tools))
	}
	uriPattern := regexp.MustCompile(`^ui://onessh/[a-z_]+\?v=[0-9a-f]{8}$`)
	for name, tool := range tools {
		ui := toolUIMeta(t, tool)
		uri, _ := ui["resourceUri"].(string)
		if !uriPattern.MatchString(uri) {
			t.Errorf("%s 的 resourceUri = %q，不符合内容版本化格式", name, uri)
			continue
		}
		legacy, _ := tool.Meta[appLegacyTemplateKey].(string)
		if !strings.HasPrefix(legacy, appLegacyPrefix) || !strings.HasSuffix(legacy, "/"+name) {
			t.Errorf("%s 的旧版 outputTemplate = %q，应指向 skybridge 版本", name, legacy)
		}
		visibility := metaStrings(ui["visibility"])
		if len(visibility) == 0 || visibility[0] != "model" {
			t.Errorf("%s 的 visibility = %#v", name, ui["visibility"])
		}
		callable := len(visibility) > 1 && visibility[1] == "app"
		if accessible, _ := tool.Meta[appLegacyAccessibleKey].(bool); accessible != callable {
			t.Errorf("%s 的 widgetAccessible 与 visibility 不一致", name)
		}
		resource := resources[uri]
		if resource == nil {
			t.Errorf("%s 指向的资源 %s 未在 resources/list 中列出", name, uri)
			continue
		}
		if resource.MIMEType != appMIMEType {
			t.Errorf("资源 %s 的 mimeType = %q", uri, resource.MIMEType)
		}
		resourceUI, _ := resource.Meta["ui"].(map[string]any)
		if resourceUI == nil || resourceUI["prefersBorder"] != true {
			t.Errorf("资源 %s 缺少标准 ui 元数据：%#v", uri, resource.Meta)
		}
		if resourceUI["domain"] != publicURL {
			t.Errorf("资源 %s 的 domain = %#v，应为 %s", uri, resourceUI["domain"], publicURL)
		}
	}
}

// 卡片正文必须完全自包含：宿主沙箱会拦掉一切外链，任何 http(s) 引用都会变成加载不出来的空白。
func TestAppResourcesAreSelfContained(t *testing.T) {
	session := newAppTestSession(t, "", true)
	resources := listAppResources(t, session)
	if len(resources) == 0 {
		t.Fatal("没有列出任何卡片资源")
	}
	required := []string{
		`ui/initialize`,
		`ui/notifications/initialized`,
		`ui/notifications/tool-result`,
		`ui/notifications/tool-input`,
		`ui/notifications/size-changed`,
		`Content-Security-Policy`,
		`expectedTool`,
		`OneSSH.boot(`,
	}
	forbidden := []string{"innerHTML", "insertAdjacentHTML", "document.write", "http://", "https://", "{{", "@import"}
	for uri := range resources {
		read, err := session.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: uri})
		if err != nil {
			t.Fatalf("ReadResource(%s) 出错: %v", uri, err)
		}
		if len(read.Contents) != 1 {
			t.Fatalf("资源 %s 返回了 %d 段内容", uri, len(read.Contents))
		}
		content := read.Contents[0]
		if content.MIMEType != appMIMEType || content.URI != uri {
			t.Errorf("资源 %s 的内容头 = %#v", uri, content)
		}
		if _, ok := content.Meta["ui"].(map[string]any); !ok {
			t.Errorf("资源 %s 的内容缺少 ui 元数据", uri)
		}
		html := content.Text
		for _, marker := range required {
			if !strings.Contains(html, marker) {
				t.Errorf("资源 %s 缺少标记 %q", uri, marker)
			}
		}
		for _, marker := range forbidden {
			if strings.Contains(html, marker) {
				t.Errorf("资源 %s 含有禁止出现的内容 %q", uri, marker)
			}
		}
		if open, close := strings.Count(html, "<script"), strings.Count(html, "</script>"); open != close || open != 1 {
			t.Errorf("资源 %s 的 script 标签数量异常：%d 开 %d 闭", uri, open, close)
		}
		if len(html) > appHTMLLimit {
			t.Errorf("资源 %s 的正文 %d 字节，超过上限", uri, len(html))
		}
	}
}

// 每张卡片只带自己那一组视图：同组工具要能互相导航，跨组代码则不该占用体积。
func TestAppResourceCarriesOnlyItsOwnGroupViews(t *testing.T) {
	catalog, err := newAppCatalog("", true)
	if err != nil {
		t.Fatal(err)
	}
	groups := map[string][]string{}
	for _, binding := range appBindings {
		groups[binding.View] = append(groups[binding.View], binding.Tool)
	}
	for _, binding := range appBindings {
		html, ok := catalog.AppHTML(binding.Tool)
		if !ok {
			t.Fatalf("%s 没有组装出卡片", binding.Tool)
		}
		if !strings.Contains(html, `OneSSH.boot("`+binding.Tool+`")`) {
			t.Errorf("%s 的卡片没有绑定到自己的工具名", binding.Tool)
		}
		for view, tools := range groups {
			for _, tool := range tools {
				marker := `OneSSH.view("` + tool + `"`
				if view == binding.View {
					if !strings.Contains(html, marker) {
						t.Errorf("%s 的卡片缺少同组视图 %s", binding.Tool, tool)
					}
				} else if strings.Contains(html, marker) {
					t.Errorf("%s 的卡片混入了 %s 组的视图 %s", binding.Tool, view, tool)
				}
			}
		}
	}
}

func TestAppsDisabledRemovesResourcesAndMeta(t *testing.T) {
	session := newAppTestSession(t, "https://ssh.example.com", false)
	for name, tool := range listAppTools(t, session) {
		if tool.Meta["ui"] != nil || tool.Meta[appLegacyTemplateKey] != nil {
			t.Errorf("关闭卡片后 %s 仍带有 UI 元数据：%#v", name, tool.Meta)
		}
	}
	if resources, err := session.ListResources(context.Background(), nil); err == nil && len(resources.Resources) != 0 {
		t.Errorf("关闭卡片后仍列出 %d 个资源", len(resources.Resources))
	}
}

func TestAppWidgetDomainRequiresBareHTTPSOrigin(t *testing.T) {
	cases := map[string]string{
		"https://ssh.example.com":      "https://ssh.example.com",
		"https://ssh.example.com:8443": "https://ssh.example.com:8443",
		"https://ssh.example.com/":     "https://ssh.example.com",
		"http://127.0.0.1:8866":        "",
		"https://ssh.example.com/mcp":  "",
		"https://ssh.example.com?x=1":  "",
		"https://ssh.example.com#frag": "",
		"https://user@ssh.example.com": "",
		"https://ssh.example.com?":     "",
		"ftp://ssh.example.com":        "",
		"ssh.example.com":              "",
		"":                             "",
	}
	for input, want := range cases {
		if got := appWidgetDomain(input); got != want {
			t.Errorf("appWidgetDomain(%q) = %q，期望 %q", input, got, want)
		}
	}
}

func TestAppResourceURIIsStableForSameContent(t *testing.T) {
	first, err := newAppCatalog("", true)
	if err != nil {
		t.Fatal(err)
	}
	second, err := newAppCatalog("", true)
	if err != nil {
		t.Fatal(err)
	}
	for tool, entry := range first.entries {
		if second.entries[tool].uri != entry.uri {
			t.Errorf("%s 两次组装得到不同的 URI", tool)
		}
	}
}

func TestAssembleAppHTMLRejectsUnknownView(t *testing.T) {
	if _, err := assembleAppHTML(appBinding{Tool: "ghost", View: "nope"}); err == nil {
		t.Fatal("未知视图应当组装失败")
	}
}

// 只认 openai/outputTemplate 的旧客户端要求它指向的资源用 skybridge MIME。
// 这份资源走 URI 模板：能按 URI 读到，但不会把 resources/list 刷成两倍长。
func TestLegacyWidgetResourceServesSkybridgeMIME(t *testing.T) {
	session := newAppTestSession(t, "https://ssh.example.com", true)
	tools := listAppTools(t, session)
	standard := listAppResources(t, session)
	if len(standard) != len(tools) {
		t.Fatalf("resources/list = %d，应仍只列标准资源 %d 条", len(standard), len(tools))
	}
	for name, tool := range tools {
		legacy, _ := tool.Meta[appLegacyTemplateKey].(string)
		if standard[legacy] != nil {
			t.Errorf("旧版资源 %s 不应出现在 resources/list 中", legacy)
		}
		read, err := session.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: legacy})
		if err != nil {
			t.Fatalf("ReadResource(%s) 出错: %v", legacy, err)
		}
		if len(read.Contents) != 1 || read.Contents[0].MIMEType != appLegacyMIMEType {
			t.Fatalf("旧版资源 %s 的内容 = %#v", legacy, read.Contents)
		}
		if !strings.Contains(read.Contents[0].Text, `OneSSH.boot("`+name+`")`) {
			t.Errorf("旧版资源 %s 的正文不是 %s 的卡片", legacy, name)
		}
		ui, _ := tool.Meta["ui"].(map[string]any)
		standardURI, _ := ui["resourceUri"].(string)
		modern, err := session.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: standardURI})
		if err != nil {
			t.Fatal(err)
		}
		if modern.Contents[0].Text != read.Contents[0].Text {
			t.Errorf("%s 的新旧两份正文不一致", name)
		}
	}
	// 版本对不上说明宿主用的是缓存里的旧地址，此时应当报未找到而不是回一份新正文。
	stale := appLegacyPrefix + "00000000/exec"
	if _, err := session.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: stale}); err == nil {
		t.Fatal("过期的旧版 URI 应当读取失败")
	}
}
