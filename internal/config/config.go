package config

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
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
}

func Load() (Config, error) {
	var cfg Config
	keyHex := os.Getenv("ONESSH_MASTER_KEY")
	key, err := hex.DecodeString(keyHex)
	if err != nil || len(key) != 32 {
		return cfg, errors.New("ONESSH_MASTER_KEY 必须是 64 位十六进制字符串；可用 openssl rand -hex 32 生成")
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
