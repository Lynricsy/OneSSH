package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"onessh/internal/store"
	"onessh/internal/toolgroups"
)

func TestTokenDenylistHidesMemoryAndExecFromListAndCall(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	server := newTestServer(t, Options{MCPApps: true, SearchHelper: true})
	st := server.Store
	token, err := st.CreateToken(ctx, store.TokenCreate{
		Name: "restricted-agent", Hash: store.TokenHash("secret-deny"), AllHosts: true,
		DisabledTools: []string{"memory", "exec"},
	})
	if err != nil {
		t.Fatal(err)
	}

	resolve := func(*http.Request) (string, string) {
		return "https://onessh.example/mcp", "https://onessh.example/.well-known/oauth-protected-resource/mcp"
	}
	httpServer := httptest.NewServer(Handler(st, server, resolve))
	t.Cleanup(httpServer.Close)

	client := mcp.NewClient(&mcp.Implementation{Name: "deny-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   httpServer.URL,
		HTTPClient: &http.Client{Transport: bearerTransport{token: "secret-deny"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })

	for _, name := range listToolNames(t, ctx, session) {
		if strings.HasPrefix(name, "memory_") || name == "exec" || name == "session_env" || name == "output_read" {
			t.Fatalf("令牌 denylist 仍暴露工具: %s", name)
		}
	}
	instructions := session.InitializeResult().Instructions
	if strings.Contains(instructions, "memory_") || strings.Contains(instructions, "记忆库") {
		t.Fatalf("提示词仍提到记忆: %s", instructions)
	}
	if strings.Contains(instructions, "session_env") || strings.Contains(instructions, "output_read") {
		t.Fatalf("提示词仍提到 exec 组工具: %s", instructions)
	}

	resources, err := session.ListResources(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, resource := range resources.Resources {
		if strings.Contains(resource.Name, "memory_") || strings.Contains(resource.URI, "/exec?") || strings.Contains(resource.URI, "/session_env") {
			t.Fatalf("禁用工具的卡片仍发布: %s", resource.URI)
		}
	}

	denied, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "memory_stats"})
	if err != nil {
		t.Fatal(err)
	}
	if denied == nil || !denied.IsError {
		t.Fatalf("禁用工具调用应返回 IsError，实际 %#v", denied)
	}
	if text := toolText(denied); !strings.Contains(text, "tool not authorized: memory_stats") {
		t.Fatalf("拒绝文案 = %q", text)
	}

	audit, err := st.ListAudit(ctx, nil, nil, nil, nil, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(audit) == 0 || audit[0].OK || audit[0].Tool != "memory_stats" {
		t.Fatalf("拒绝审计 = %#v", audit)
	}
	if !audit[0].TokenID.Valid || audit[0].TokenID.Int64 != token.ID {
		t.Fatalf("审计令牌 ID = %#v", audit[0].TokenID)
	}
	if !audit[0].TokenName.Valid || audit[0].TokenName.String != token.Name {
		t.Fatalf("审计令牌名 = %#v", audit[0].TokenName)
	}
}

func TestTokenDenylistUnionsWithInstanceDisabledTools(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	server := newTestServer(t, Options{
		MCPApps: true, SearchHelper: true,
		DisabledTools: toolgroups.Disabled{toolgroups.Image: true},
	})
	st := server.Store
	if _, err := st.CreateToken(ctx, store.TokenCreate{
		Name: "union-agent", Hash: store.TokenHash("secret-union"), AllHosts: true,
		DisabledTools: []string{"files"},
	}); err != nil {
		t.Fatal(err)
	}
	resolve := func(*http.Request) (string, string) {
		return "https://onessh.example/mcp", "https://onessh.example/.well-known/oauth-protected-resource/mcp"
	}
	httpServer := httptest.NewServer(Handler(st, server, resolve))
	t.Cleanup(httpServer.Close)

	session, err := mcp.NewClient(&mcp.Implementation{Name: "union-test", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   httpServer.URL,
		HTTPClient: &http.Client{Transport: bearerTransport{token: "secret-union"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })

	for _, name := range listToolNames(t, ctx, session) {
		if name == "image_view" || strings.HasPrefix(name, "file_") {
			t.Fatalf("并集禁用后仍暴露: %s", name)
		}
	}
}

func toolText(result *mcp.CallToolResult) string {
	if result == nil {
		return ""
	}
	var parts []string
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func TestTokenDenylistParseFailureFailClosed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	server := newTestServer(t, Options{MCPApps: true, SearchHelper: true})
	st := server.Store
	token, err := st.CreateToken(ctx, store.TokenCreate{
		Name: "corrupt-agent", Hash: store.TokenHash("secret-corrupt"), AllHosts: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	// 绕过持久化校验，模拟库里已有损坏的 denylist。
	if _, err = st.DB.ExecContext(ctx, `UPDATE tokens SET disabled_tools_json=? WHERE id=?`, `["not-a-real-group"]`, token.ID); err != nil {
		t.Fatal(err)
	}

	resolve := func(*http.Request) (string, string) {
		return "https://onessh.example/mcp", "https://onessh.example/.well-known/oauth-protected-resource/mcp"
	}
	httpServer := httptest.NewServer(Handler(st, server, resolve))
	t.Cleanup(httpServer.Close)

	session, err := mcp.NewClient(&mcp.Implementation{Name: "corrupt-test", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   httpServer.URL,
		HTTPClient: &http.Client{Transport: bearerTransport{token: "secret-corrupt"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })

	if names := listToolNames(t, ctx, session); len(names) != 0 {
		t.Fatalf("解析失败应 fail-closed 隐藏全部工具，实际 %v", names)
	}
	denied, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "hosts_list"})
	if err != nil {
		t.Fatal(err)
	}
	if denied == nil || !denied.IsError {
		t.Fatalf("解析失败后调用应拒绝，实际 %#v", denied)
	}
}

func TestTokenDenylistCrossTokenIsolation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	server := newTestServer(t, Options{MCPApps: true, SearchHelper: true})
	st := server.Store
	if _, err := st.CreateToken(ctx, store.TokenCreate{
		Name: "full", Hash: store.TokenHash("secret-full"), AllHosts: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateToken(ctx, store.TokenCreate{
		Name: "restricted", Hash: store.TokenHash("secret-restricted"), AllHosts: true,
		DisabledTools: []string{"memory"},
	}); err != nil {
		t.Fatal(err)
	}

	resolve := func(*http.Request) (string, string) {
		return "https://onessh.example/mcp", "https://onessh.example/.well-known/oauth-protected-resource/mcp"
	}
	httpServer := httptest.NewServer(Handler(st, server, resolve))
	t.Cleanup(httpServer.Close)

	connect := func(token string) *mcp.ClientSession {
		t.Helper()
		session, err := mcp.NewClient(&mcp.Implementation{Name: "iso", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{
			Endpoint:   httpServer.URL,
			HTTPClient: &http.Client{Transport: bearerTransport{token: token}},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { session.Close() })
		return session
	}

	fullNames := map[string]bool{}
	for _, name := range listToolNames(t, ctx, connect("secret-full")) {
		fullNames[name] = true
	}
	if !fullNames["memory_stats"] {
		t.Fatal("无 denylist 令牌应看到 memory_stats")
	}

	for _, name := range listToolNames(t, ctx, connect("secret-restricted")) {
		if strings.HasPrefix(name, "memory_") {
			t.Fatalf("受限令牌仍暴露 %s", name)
		}
	}
}

func TestTokenDenylistAuditsResourceReadDenial(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	server := newTestServer(t, Options{MCPApps: true, SearchHelper: true})
	st := server.Store
	if _, err := st.CreateToken(ctx, store.TokenCreate{
		Name: "resource-deny", Hash: store.TokenHash("secret-resource"), AllHosts: true,
		DisabledTools: []string{"exec"},
	}); err != nil {
		t.Fatal(err)
	}

	resolve := func(*http.Request) (string, string) {
		return "https://onessh.example/mcp", "https://onessh.example/.well-known/oauth-protected-resource/mcp"
	}
	httpServer := httptest.NewServer(Handler(st, server, resolve))
	t.Cleanup(httpServer.Close)

	// 用无 denylist 的令牌拿一张 exec 卡片 URI，再用受限令牌直接读。
	if _, err := st.CreateToken(ctx, store.TokenCreate{
		Name: "full-for-uri", Hash: store.TokenHash("secret-uri"), AllHosts: true,
	}); err != nil {
		t.Fatal(err)
	}
	fullSession, err := mcp.NewClient(&mcp.Implementation{Name: "uri", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   httpServer.URL,
		HTTPClient: &http.Client{Transport: bearerTransport{token: "secret-uri"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fullSession.Close() })
	resources, err := fullSession.ListResources(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var execURI string
	for _, resource := range resources.Resources {
		if strings.Contains(resource.URI, "/exec") || resource.Name == "onessh-app-exec" || strings.HasSuffix(resource.URI, "/exec") || strings.Contains(resource.URI, "ui://onessh/exec") {
			execURI = resource.URI
			break
		}
	}
	if execURI == "" {
		// 兜底：直接用标准卡片 URI。
		execURI = "ui://onessh/exec"
	}

	session, err := mcp.NewClient(&mcp.Implementation{Name: "resource-deny", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   httpServer.URL,
		HTTPClient: &http.Client{Transport: bearerTransport{token: "secret-resource"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })

	_, err = session.ReadResource(ctx, &mcp.ReadResourceParams{URI: execURI})
	if err == nil {
		t.Fatalf("受限令牌读取禁用工具卡片应失败: %s", execURI)
	}

	audit, err := st.ListAudit(ctx, nil, nil, nil, nil, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range audit {
		if !row.OK && row.Tool == "exec" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("resources/read 拒绝应写入审计, audit=%#v", audit)
	}
}

func TestTokenDenylistTrimsServerDiscoverInstructions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	server := newTestServer(t, Options{MCPApps: true, SearchHelper: true})
	st := server.Store
	if _, err := st.CreateToken(ctx, store.TokenCreate{
		Name: "discover-deny", Hash: store.TokenHash("secret-discover"), AllHosts: true,
		DisabledTools: []string{"memory", "exec"},
	}); err != nil {
		t.Fatal(err)
	}

	resolve := func(*http.Request) (string, string) {
		return "https://onessh.example/mcp", "https://onessh.example/.well-known/oauth-protected-resource/mcp"
	}
	httpServer := httptest.NewServer(Handler(st, server, resolve))
	t.Cleanup(httpServer.Close)

	body := `{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientInfo":{"name":"discover-test","version":"1"},"io.modelcontextprotocol/clientCapabilities":{}}}}`
	req, err := http.NewRequest(http.MethodPost, httpServer.URL, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer secret-discover")
	req.Header.Set("Mcp-Protocol-Version", "2026-07-28")
	req.Header.Set("Mcp-Method", "server/discover")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("discover status = %d body = %s", resp.StatusCode, raw)
	}
	payload := raw
	if i := bytes.Index(raw, []byte("data: ")); i >= 0 {
		payload = raw[i+len("data: "):]
		if j := bytes.IndexByte(payload, '\n'); j >= 0 {
			payload = payload[:j]
		}
	}
	var rpc struct {
		Result *mcp.DiscoverResult `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(payload, &rpc); err != nil {
		t.Fatalf("unmarshal discover %q: %v", raw, err)
	}
	if rpc.Error != nil {
		t.Fatalf("discover error: %#v body=%s", rpc.Error, raw)
	}
	if rpc.Result == nil {
		t.Fatalf("discover missing result: %s", raw)
	}
	instructions := rpc.Result.Instructions
	if strings.Contains(instructions, "memory_") || strings.Contains(instructions, "记忆库") {
		t.Fatalf("discover 提示词仍提到记忆: %s", instructions)
	}
	if strings.Contains(instructions, "session_env") || strings.Contains(instructions, "output_read") {
		t.Fatalf("discover 提示词仍提到 exec 组工具: %s", instructions)
	}
	if !strings.Contains(instructions, "hosts_list") {
		t.Fatalf("discover 提示词缺少仍允许的工具: %s", instructions)
	}
}

func TestTokenDenylistPublishesDeniedToolCallEvent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	server := newTestServer(t, Options{MCPApps: true, SearchHelper: true})
	st := server.Store
	if _, err := st.CreateToken(ctx, store.TokenCreate{
		Name: "event-deny", Hash: store.TokenHash("secret-event"), AllHosts: true,
		DisabledTools: []string{"memory"},
	}); err != nil {
		t.Fatal(err)
	}

	ch, unsub := server.Events.Subscribe()
	t.Cleanup(unsub)

	resolve := func(*http.Request) (string, string) {
		return "https://onessh.example/mcp", "https://onessh.example/.well-known/oauth-protected-resource/mcp"
	}
	httpServer := httptest.NewServer(Handler(st, server, resolve))
	t.Cleanup(httpServer.Close)

	session, err := mcp.NewClient(&mcp.Implementation{Name: "event-deny", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   httpServer.URL,
		HTTPClient: &http.Client{Transport: bearerTransport{token: "secret-event"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })

	denied, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "memory_stats"})
	if err != nil {
		t.Fatal(err)
	}
	if denied == nil || !denied.IsError {
		t.Fatalf("禁用工具调用应返回 IsError，实际 %#v", denied)
	}

	deadline := time.After(3 * time.Second)
	for {
		select {
		case event := <-ch:
			if event.Type != "tool_call" {
				continue
			}
			data, _ := event.Data.(map[string]any)
			if data["tool"] != "memory_stats" {
				continue
			}
			if data["ok"] != false {
				t.Fatalf("拒绝事件 ok = %#v", data["ok"])
			}
			if data["denied"] != true {
				t.Fatalf("拒绝事件 denied = %#v", data["denied"])
			}
			return
		case <-deadline:
			t.Fatal("未收到拒绝 tool_call 事件")
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}

func TestTokenDenylistHidesLegacyAppTemplateWhenNoAppsRemain(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	server := newTestServer(t, Options{MCPApps: true, SearchHelper: true})
	st := server.Store
	// 禁用全部工具组 → 无任何仍允许的 App 卡片，legacy 模板也应隐藏。
	allGroups := make([]string, 0, len(toolgroups.All))
	for _, def := range toolgroups.All {
		allGroups = append(allGroups, string(def.Group))
	}
	if _, err := st.CreateToken(ctx, store.TokenCreate{
		Name: "no-apps", Hash: store.TokenHash("secret-no-apps"), AllHosts: true,
		DisabledTools: allGroups,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateToken(ctx, store.TokenCreate{
		Name: "full-apps", Hash: store.TokenHash("secret-full-apps"), AllHosts: true,
	}); err != nil {
		t.Fatal(err)
	}

	resolve := func(*http.Request) (string, string) {
		return "https://onessh.example/mcp", "https://onessh.example/.well-known/oauth-protected-resource/mcp"
	}
	httpServer := httptest.NewServer(Handler(st, server, resolve))
	t.Cleanup(httpServer.Close)

	connect := func(token string) *mcp.ClientSession {
		t.Helper()
		session, err := mcp.NewClient(&mcp.Implementation{Name: "tmpl", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{
			Endpoint:   httpServer.URL,
			HTTPClient: &http.Client{Transport: bearerTransport{token: token}},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { session.Close() })
		return session
	}

	full, err := connect("secret-full-apps").ListResourceTemplates(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	foundLegacy := false
	for _, tmpl := range full.ResourceTemplates {
		if tmpl != nil && tmpl.Name == "onessh-app-legacy" {
			foundLegacy = true
			break
		}
	}
	if !foundLegacy {
		t.Fatal("无 denylist 令牌应看到 onessh-app-legacy 模板")
	}

	restricted, err := connect("secret-no-apps").ListResourceTemplates(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tmpl := range restricted.ResourceTemplates {
		if tmpl != nil && tmpl.Name == "onessh-app-legacy" {
			t.Fatal("全部 App 被禁用时仍暴露 onessh-app-legacy")
		}
	}
}
