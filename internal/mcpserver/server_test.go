package mcpserver

import (
	"context"
	"encoding/json"
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

// 工具描述和参数说明是 Agent 选型的唯一依据：少一句「什么时候该用我」，模型就会退回 exec 硬拼命令。
// 这里把「每个工具都要有标题、有足够长的描述、有行为注解，且每个参数都有说明」固化成契约，
// 避免以后新增工具时又退化成一句话描述。
func TestToolCatalogGivesAgentsEnoughContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	box, err := cryptox.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	pool := sshpool.New(st, box)
	defer pool.Close()
	server := New(st, pool, events.New(), hostmanager.New(st, box, pool), memoryx.New(st, memoryx.EmbeddingConfig{}), dir, "", 0)
	defer server.Close()

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.MCP.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "catalog-test", Version: "1"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) < 25 {
		t.Fatalf("工具数量 = %d，疑似注册缺失", len(tools.Tools))
	}
	for _, tool := range tools.Tools {
		if tool.Title == "" {
			t.Errorf("%s 缺少 Title", tool.Name)
		}
		if length := len([]rune(tool.Description)); length < 40 {
			t.Errorf("%s 描述过短（%d 字），说不清适用场景与边界", tool.Name, length)
		}
		if tool.Annotations == nil {
			t.Errorf("%s 缺少 ToolAnnotations，客户端无法判断只读还是破坏性", tool.Name)
		}
		for name, description := range schemaPropertyDescriptions(t, tool.Name, tool.InputSchema) {
			if strings.TrimSpace(description) == "" {
				t.Errorf("%s 的参数 %s 缺少说明", tool.Name, name)
			}
		}
	}
}

func schemaPropertyDescriptions(t *testing.T, tool string, schema any) map[string]string {
	t.Helper()
	raw, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("%s 输入 schema 无法序列化: %v", tool, err)
	}
	var parsed struct {
		Properties map[string]struct {
			Description string `json:"description"`
		} `json:"properties"`
	}
	if err = json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("%s 输入 schema 无法解析: %v", tool, err)
	}
	out := make(map[string]string, len(parsed.Properties))
	for name, property := range parsed.Properties {
		out[name] = property.Description
	}
	return out
}

func TestRedactedJSONRemovesNestedPasswords(t *testing.T) {
	input := map[string]any{
		"host": "old-name",
		"config": map[string]any{
			"addr":     "127.0.0.1",
			"password": "audit-secret",
			"nested":   []any{map[string]any{"password": "second-secret", "username": "root"}},
		},
	}
	result := redactedJSON(input)
	if !strings.Contains(result, `"addr":"127.0.0.1"`) || !strings.Contains(result, `"username":"root"`) {
		t.Fatalf("非敏感配置丢失: %s", result)
	}
	if strings.Count(result, `"password":"<redacted>"`) != 2 {
		t.Fatalf("密码未完整脱敏: %s", result)
	}
	if strings.Contains(result, "audit-secret") || strings.Contains(result, "second-secret") {
		t.Fatalf("审计参数泄漏密码: %s", result)
	}
}

func TestHostOfPrefersHostAndFallsBackToName(t *testing.T) {
	if got := hostOf(HostUpdateInput{Host: "old", Name: "new"}); got != "old" {
		t.Fatalf("更新审计主机 = %q", got)
	}
	if got := hostOf(HostUpdateInput{Name: "new"}); got != "new" {
		t.Fatalf("空 Host 未回退 Name: %q", got)
	}
	if got := hostOf(hostmanager.Input{Name: "created"}); got != "created" {
		t.Fatalf("创建审计主机 = %q", got)
	}
}

func TestServerInfoAdvertisesSameOriginPNGIcon(t *testing.T) {
	info := serverInfo("https://ssh.example.com")
	if len(info.Icons) != 1 {
		t.Fatalf("图标数量 = %d", len(info.Icons))
	}
	icon := info.Icons[0]
	// src 必须是与 /mcp 同源的绝对 URI：相对路径客户端解析不了，跨域会被规范建议拒绝
	if icon.Source != "https://ssh.example.com/logo.png" {
		t.Fatalf("图标地址 = %q", icon.Source)
	}
	// 只有 image/png 与 image/jpeg 被要求所有渲染图标的客户端支持，换成 svg 会牺牲互操作性
	if icon.MIMEType != "image/png" {
		t.Fatalf("图标 MIME = %q", icon.MIMEType)
	}
}

func TestServerInfoOmitsIconWithoutPublicURL(t *testing.T) {
	// 拿不到对外地址时只能凑出相对路径，宁可不发图标也不发客户端解析不了的 src
	if info := serverInfo(""); len(info.Icons) != 0 {
		t.Fatalf("未配置对外地址仍发布图标: %+v", info.Icons)
	}
}
