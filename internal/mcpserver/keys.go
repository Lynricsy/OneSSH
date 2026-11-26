package mcpserver

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"onessh/internal/hostmanager"
	"onessh/internal/sshkeys"
	"onessh/internal/store"
)

// KeyItem 是对 Agent 暴露的密钥视图：只有公钥与元数据，私钥永远不出网关。
type KeyItem struct {
	ID          int64    `json:"id"`
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Fingerprint string   `json:"fingerprint"`
	PublicKey   string   `json:"public_key"`
	CreatedAt   int64    `json:"created_at"`
	UsedBy      []string `json:"used_by"`
}

type KeysOutput struct {
	Keys []KeyItem `json:"keys"`
}

// 入参字段刻意不叫 Name / Host：审计的 hostOf 会按这两个字段名取「目标主机」，
// 密钥名或来源主机被记成目标主机会让活动页的主机列出现一台并不存在的机器。
type KeyGenerateInput struct {
	KeyName string `json:"name" jsonschema:"新密钥名称，网关内唯一；之后 host_create 用返回的 id 作为 key_id 引用"`
}

type KeyImportInput struct {
	KeyName    string `json:"name" jsonschema:"密钥名称，网关内唯一；之后 host_create 用返回的 id 作为 key_id 引用"`
	PrivateKey string `json:"private_key,omitempty" jsonschema:"私钥文件全文，OpenSSH 或 PEM 格式（以 -----BEGIN 开头）；与 from_host + from_path 二选一，审计中固定脱敏"`
	Passphrase string `json:"passphrase,omitempty" jsonschema:"私钥口令，仅受口令保护的私钥需要；只用于本次解密，不落库，审计中固定脱敏"`
	FromHost   string `json:"from_host,omitempty" jsonschema:"从这台授权主机上读取私钥文件（取自 hosts_list 的 name）；与 private_key 二选一，需同时给 from_path"`
	FromPath   string `json:"from_path,omitempty" jsonschema:"from_host 上私钥文件的路径，例如 ~/.ssh/id_ed25519；文件上限 64KiB"`
}

func (s *Server) registerKeys() {
	register[Empty, KeysOutput](s, &mcp.Tool{
		Name:        "keys_list",
		Title:       "列出 SSH 密钥",
		Description: "列出网关保存的全部 SSH 密钥：ID、名称、算法、SHA256 指纹、公钥以及正在使用它的主机。私钥只以主密钥加密的形式存在网关内，任何工具都不会返回。host_create / host_update 的 key_id 取自这里。需要 manage_hosts 权限。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: new(false), IdempotentHint: true, OpenWorldHint: new(false)},
	}, s.keysList)
	register[KeyGenerateInput, KeyItem](s, &mcp.Tool{
		Name:        "key_generate",
		Title:       "生成 SSH 密钥",
		Description: "在网关内生成一把新的 ed25519 密钥并加密保存，只返回公钥与指纹。典型用法：生成后把 public_key 追加到目标主机的 ~/.ssh/authorized_keys，再用 host_create（auth_type=key、key_id=返回的 id）登记主机并 host_test 验证。名称必须唯一。需要 manage_hosts 权限。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: new(false), IdempotentHint: false, OpenWorldHint: new(false)},
	}, s.keyGenerate)
	register[KeyImportInput, KeyItem](s, &mcp.Tool{
		Name:        "key_import",
		Title:       "上传 SSH 私钥",
		Description: "上传（导入）一把已有的 SSH 私钥并经主密钥加密保存，返回 id、公钥与指纹。私钥来源二选一：private_key 直接给私钥文件全文；或 from_host + from_path 让网关经 SFTP 读取某台授权主机上的私钥文件（例如 ~/.ssh/id_ed25519），私钥不经过模型上下文。支持 OpenSSH 与 PEM（RSA/ECDSA/ed25519），受口令保护的私钥需填 passphrase，口令不落库；公钥 .pub 与 PuTTY .ppk 会被拒绝并提示原因。导入后用 host_create 的 key_id 引用。需要 manage_hosts 权限。",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: new(false), IdempotentHint: false, OpenWorldHint: new(true)},
	}, s.keyImport)
}

func (s *Server) keysList(ctx context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, KeysOutput, error) {
	if _, err := AuthorizedHostManagement(ctx); err != nil {
		return errorResult(err.Error()), KeysOutput{}, nil
	}
	keys, err := s.Store.ListKeys(ctx)
	if err != nil {
		return nil, KeysOutput{}, err
	}
	hosts, err := s.Store.ListHosts(ctx)
	if err != nil {
		return nil, KeysOutput{}, err
	}
	usage := make(map[int64][]string)
	for _, host := range hosts {
		if id := host.View().KeyID; id != nil && host.View().AuthType == "key" {
			usage[*id] = append(usage[*id], host.Name)
		}
	}
	out := KeysOutput{Keys: make([]KeyItem, 0, len(keys))}
	for _, key := range keys {
		out.Keys = append(out.Keys, keyItem(key, usage[key.ID]))
	}
	return nil, out, nil
}

func (s *Server) keyGenerate(ctx context.Context, _ *mcp.CallToolRequest, in KeyGenerateInput) (*mcp.CallToolResult, KeyItem, error) {
	if _, err := AuthorizedHostManagement(ctx); err != nil {
		return errorResult(err.Error()), KeyItem{}, nil
	}
	key, err := s.HostManager.CreateKey(ctx, hostmanager.KeyInput{Name: in.KeyName, Mode: "generate"})
	if err != nil {
		return errorResult(err.Error()), KeyItem{}, nil
	}
	return nil, keyItem(key, nil), nil
}

func (s *Server) keyImport(ctx context.Context, _ *mcp.CallToolRequest, in KeyImportInput) (*mcp.CallToolResult, KeyItem, error) {
	if _, err := AuthorizedHostManagement(ctx); err != nil {
		return errorResult(err.Error()), KeyItem{}, nil
	}
	hasText := strings.TrimSpace(in.PrivateKey) != ""
	hasFile := strings.TrimSpace(in.FromHost) != "" || strings.TrimSpace(in.FromPath) != ""
	switch {
	case hasText && hasFile:
		return errorResult("private_key 与 from_host/from_path 只能二选一"), KeyItem{}, nil
	case !hasText && !hasFile:
		return errorResult("请提供 private_key，或用 from_host + from_path 指定远端主机上的私钥文件"), KeyItem{}, nil
	}
	material := []byte(in.PrivateKey)
	if hasFile {
		if strings.TrimSpace(in.FromHost) == "" || strings.TrimSpace(in.FromPath) == "" {
			return errorResult("from_host 与 from_path 必须同时提供"), KeyItem{}, nil
		}
		// 读远端文件属于对该主机的文件访问，沿用执行类工具的主机授权，不因 manage_hosts 放宽。
		if _, err := AuthorizedHost(ctx, in.FromHost); err != nil {
			return errorResult(err.Error()), KeyItem{}, nil
		}
		data, err := s.Files.RawRead(ctx, in.FromHost, in.FromPath, sshkeys.MaxPrivateKeyBytes)
		if err != nil {
			return errorResult("读取远端私钥文件失败: " + err.Error()), KeyItem{}, nil
		}
		material = data
	}
	defer clear(material)
	key, err := s.HostManager.CreateKey(ctx, hostmanager.KeyInput{
		Name: in.KeyName, Mode: "import", PrivateKey: material, Passphrase: []byte(in.Passphrase),
	})
	if err != nil {
		return errorResult(err.Error()), KeyItem{}, nil
	}
	return nil, keyItem(key, nil), nil
}

func keyItem(key store.Key, usedBy []string) KeyItem {
	kind, fingerprint := sshkeys.Describe(key.PublicKey)
	if usedBy == nil {
		usedBy = []string{}
	}
	return KeyItem{
		ID: key.ID, Name: key.Name, Type: kind, Fingerprint: fingerprint,
		PublicKey: strings.TrimSpace(key.PublicKey), CreatedAt: key.CreatedAt, UsedBy: usedBy,
	}
}
