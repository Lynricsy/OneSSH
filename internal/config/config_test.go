package config

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"onessh/internal/toolgroups"
)

func TestLoadMasterKeyFromFile(t *testing.T) {
	key := strings.Repeat("cd", 32)
	path := filepath.Join(t.TempDir(), "master.key")
	if err := os.WriteFile(path, []byte(key+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ONESSH_MASTER_KEY", "invalid")
	t.Setenv("ONESSH_MASTER_KEY_FILE", path)
	t.Setenv("ONESSH_ADMIN_PASSWORD", "pw")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(cfg.MasterKey); got != key {
		t.Fatalf("主密钥 = %s，期望 %s", got, key)
	}
}

func TestLoadMasterKeyFileMissing(t *testing.T) {
	t.Setenv("ONESSH_MASTER_KEY_FILE", filepath.Join(t.TempDir(), "missing.key"))
	t.Setenv("ONESSH_ADMIN_PASSWORD", "pw")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "读取 ONESSH_MASTER_KEY_FILE") {
		t.Fatalf("缺少文件时应报错，实际 err = %v", err)
	}
}

func TestLoadMasterKeyFileInvalidContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.key")
	if err := os.WriteFile(path, []byte("not-a-hex-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ONESSH_MASTER_KEY_FILE", path)
	t.Setenv("ONESSH_ADMIN_PASSWORD", "pw")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "必须是 64 位十六进制字符串") {
		t.Fatalf("文件内容非法时应报错，实际 err = %v", err)
	}
}

func TestLoadReadsOptionalEmbeddingConfig(t *testing.T) {
	t.Setenv("ONESSH_MASTER_KEY", strings.Repeat("01", 32))
	t.Setenv("ONESSH_ADMIN_PASSWORD", "test-password")
	t.Setenv("ONESSH_EMBEDDING_API_URL", "https://embedding.example/v1///")
	t.Setenv("ONESSH_EMBEDDING_API_KEY", "secret")
	t.Setenv("ONESSH_EMBEDDING_MODEL", "text-embedding-test")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EmbeddingAPIURL != "https://embedding.example/v1" || cfg.EmbeddingAPIKey != "secret" || cfg.EmbeddingModel != "text-embedding-test" {
		t.Fatalf("embedding 配置异常: %#v", cfg)
	}
}

func TestLoadSearchHelperMode(t *testing.T) {
	t.Setenv("ONESSH_MASTER_KEY", strings.Repeat("01", 32))
	t.Setenv("ONESSH_ADMIN_PASSWORD", "test-password")
	t.Run("auto", func(t *testing.T) {
		t.Setenv("ONESSH_SEARCH_HELPER", "auto")
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if !cfg.SearchHelper {
			t.Fatal("auto 未启用搜索 helper")
		}
	})
	t.Run("off", func(t *testing.T) {
		t.Setenv("ONESSH_SEARCH_HELPER", "off")
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.SearchHelper {
			t.Fatal("off 未禁用搜索 helper")
		}
	})
	t.Run("invalid", func(t *testing.T) {
		t.Setenv("ONESSH_SEARCH_HELPER", "on")
		_, err := Load()
		if err == nil || !strings.Contains(err.Error(), "只接受 auto 或 off") {
			t.Fatalf("非法值错误 = %v", err)
		}
	})
}

func TestLoadMCPAppsSwitch(t *testing.T) {
	base := map[string]string{
		"ONESSH_MASTER_KEY":     strings.Repeat("ab", 32),
		"ONESSH_ADMIN_PASSWORD": "pw",
	}
	cases := []struct {
		value   string
		want    bool
		wantErr bool
	}{
		{value: "", want: true},
		{value: "on", want: true},
		{value: "off", want: false},
		{value: "yes", wantErr: true},
	}
	for _, tc := range cases {
		for key, value := range base {
			t.Setenv(key, value)
		}
		t.Setenv("ONESSH_MCP_APPS", tc.value)
		cfg, err := Load()
		if tc.wantErr {
			if err == nil {
				t.Errorf("ONESSH_MCP_APPS=%q 应当报错", tc.value)
			}
			continue
		}
		if err != nil {
			t.Errorf("ONESSH_MCP_APPS=%q 出错: %v", tc.value, err)
			continue
		}
		if cfg.MCPApps != tc.want {
			t.Errorf("ONESSH_MCP_APPS=%q 得到 %v，期望 %v", tc.value, cfg.MCPApps, tc.want)
		}
	}
}

func TestLoadParsesDisabledToolGroups(t *testing.T) {
	t.Setenv("ONESSH_MASTER_KEY", strings.Repeat("01", 32))
	t.Setenv("ONESSH_ADMIN_PASSWORD", "test-password")
	t.Run("groups", func(t *testing.T) {
		t.Setenv("ONESSH_DISABLED_TOOLS", "memory, image")
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.DisabledTools.Enabled(toolgroups.Memory) || cfg.DisabledTools.Enabled(toolgroups.Image) {
			t.Fatalf("工具组未禁用: %#v", cfg.DisabledTools)
		}
		if !cfg.DisabledTools.Enabled(toolgroups.Exec) {
			t.Fatal("未列出的工具组被误禁用")
		}
	})
	t.Run("invalid", func(t *testing.T) {
		t.Setenv("ONESSH_DISABLED_TOOLS", "memories")
		_, err := Load()
		if err == nil || !strings.Contains(err.Error(), "ONESSH_DISABLED_TOOLS 未知工具组") {
			t.Fatalf("非法值错误 = %v", err)
		}
	})
}
