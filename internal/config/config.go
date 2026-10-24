package config

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"onessh/internal/toolgroups"
)

type Config struct {
	MasterKey       []byte
	AdminPassword   string
	Listen          string
	DataDir         string
	PublicURL       string
	PollInterval    time.Duration
	EmbeddingAPIURL string
	EmbeddingAPIKey string
	EmbeddingModel  string
	SearchHelper    bool
	MCPApps         bool
	DisabledTools   toolgroups.Disabled
}

func Load() (Config, error) {
	var cfg Config
	keyHex := os.Getenv("ONESSH_MASTER_KEY")
	// ONESSH_MASTER_KEY_FILE 优先：允许把主密钥放在只读文件中
	// （如 systemd credential、KMS 解密产物），避免明文出现在环境变量。
	if file := os.Getenv("ONESSH_MASTER_KEY_FILE"); file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return cfg, fmt.Errorf("读取 ONESSH_MASTER_KEY_FILE: %w", err)
		}
		keyHex = strings.TrimSpace(string(b))
	}
	key, err := hex.DecodeString(keyHex)
	if err != nil || len(key) != 32 {
		return cfg, errors.New("ONESSH_MASTER_KEY 或 ONESSH_MASTER_KEY_FILE 必须是 64 位十六进制字符串；可用 openssl rand -hex 32 生成")
	}
	cfg.MasterKey = key
	cfg.AdminPassword = os.Getenv("ONESSH_ADMIN_PASSWORD")
	if cfg.AdminPassword == "" {
		return cfg, errors.New("ONESSH_ADMIN_PASSWORD 不能为空")
	}
	cfg.Listen = envDefault("ONESSH_LISTEN", ":8866")
	cfg.DataDir = envDefault("ONESSH_DATA_DIR", "/data")
	cfg.PublicURL = os.Getenv("ONESSH_PUBLIC_URL")
	cfg.EmbeddingAPIURL = strings.TrimRight(os.Getenv("ONESSH_EMBEDDING_API_URL"), "/")
	cfg.EmbeddingAPIKey = os.Getenv("ONESSH_EMBEDDING_API_KEY")
	cfg.EmbeddingModel = os.Getenv("ONESSH_EMBEDDING_MODEL")
	switch envDefault("ONESSH_SEARCH_HELPER", "auto") {
	case "auto":
		cfg.SearchHelper = true
	case "off":
	default:
		return cfg, errors.New("ONESSH_SEARCH_HELPER 只接受 auto 或 off")
	}
	switch envDefault("ONESSH_MCP_APPS", "on") {
	case "on":
		cfg.MCPApps = true
	case "off":
	default:
		return cfg, errors.New("ONESSH_MCP_APPS 只接受 on 或 off")
	}
	disabled, err := toolgroups.Parse(os.Getenv("ONESSH_DISABLED_TOOLS"))
	if err != nil {
		return cfg, fmt.Errorf("ONESSH_DISABLED_TOOLS %w", err)
	}
	cfg.DisabledTools = disabled
	seconds, err := strconv.Atoi(envDefault("ONESSH_POLL_INTERVAL", "60"))
	if err != nil || seconds < 0 {
		return cfg, fmt.Errorf("ONESSH_POLL_INTERVAL 必须是非负整数")
	}
	cfg.PollInterval = time.Duration(seconds) * time.Second
	return cfg, nil
}

func envDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
