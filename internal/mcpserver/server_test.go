package mcpserver

import (
	"strings"
	"testing"

	"onessh/internal/hostmanager"
)

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
