package config

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"onessh/internal/cmdpolicy"
	"onessh/internal/execx"
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
	// CommandPolicy 是实例级命令策略，默认 off；CommandEnvKeys 在内置敏感环境变量
	// 之外额外拦截的键名。
	CommandPolicy  cmdpolicy.Policy
	CommandEnvKeys []string
}

func Load() (Config, error) {
	var cfg Config
	keyHex := os.Getenv("ONESSH_MASTER_KEY")
	// ONESSH_MASTER_KEY_FILE 允许把主密钥放在只读文件中（如 Docker secrets、
	// systemd credential），避免明文出现在环境变量。两者互斥：同时设置时无法判断
	// 运维意图，用错主密钥只会在解密凭据时才暴露，因此启动即拒绝。
	if file := os.Getenv("ONESSH_MASTER_KEY_FILE"); file != "" {
		if keyHex != "" {
			return cfg, errors.New("ONESSH_MASTER_KEY 与 ONESSH_MASTER_KEY_FILE 只能设置其一")
		}
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
	// 命令策略的规则用多行文本配置（一行一条正则，便于在 docker-compose 里写注释），
	// 非法正则与未知档位都在这里直接报错，不能等到第一次调用才发现策略没生效。
	commandPolicy, err := cmdpolicy.Parse(os.Getenv("ONESSH_COMMAND_POLICY"),
		cmdpolicy.SplitRules(os.Getenv("ONESSH_COMMAND_DENY")),
		cmdpolicy.SplitRules(os.Getenv("ONESSH_COMMAND_ALLOW")))
	if err != nil {
		return cfg, fmt.Errorf("ONESSH_COMMAND_POLICY/ONESSH_COMMAND_DENY/ONESSH_COMMAND_ALLOW %w", err)
	}
	cfg.CommandPolicy = commandPolicy
	cfg.CommandEnvKeys = splitKeys(os.Getenv("ONESSH_COMMAND_ENV_KEYS"))
	// 追加键必须是合法 shell 标识符：运行时的环境键名都按这个规则校验，
	// 拼错的 `HTTP-PROXY` 永远匹配不到任何键，等于没配却看似生效，启动即报错。
	for _, key := range cfg.CommandEnvKeys {
		if !execx.ValidEnvName(key) {
			return cfg, fmt.Errorf("ONESSH_COMMAND_ENV_KEYS 含非法环境变量名 %q：必须是 [A-Za-z_][A-Za-z0-9_]*", key)
		}
	}
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

// splitKeys 解析逗号分隔的键名列表；空项忽略。大小写归一由 cmdpolicy.SensitiveEnv 负责，
// 这里保留运维写的原样，方便报错信息回指配置内容。
func splitKeys(raw string) []string {
	keys := make([]string, 0, strings.Count(raw, ",")+1)
	for key := range strings.SplitSeq(raw, ",") {
		if key = strings.TrimSpace(key); key != "" {
			keys = append(keys, key)
		}
	}
	return keys
}
