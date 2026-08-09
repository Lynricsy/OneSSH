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
