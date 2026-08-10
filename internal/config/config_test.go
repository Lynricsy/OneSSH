package config

import (
	"strings"
	"testing"
)

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
