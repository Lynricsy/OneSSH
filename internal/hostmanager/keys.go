package hostmanager

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"onessh/internal/sshkeys"
	"onessh/internal/store"
)

// maxKeyNameLength 与主机名同一量级，足够放下 deploy-prod-ed25519 这类描述性名称。
const maxKeyNameLength = 64

// KeyInput 是新建密钥的统一入参：Mode 为 generate 时忽略私钥与口令；
// 为 import 时 PrivateKey 是私钥文件原文（OpenSSH / PEM），Passphrase 只用于本次解密。
type KeyInput struct {
	Name       string
	Mode       string
	PrivateKey []byte
	Passphrase []byte
}

// CreateKey 生成或导入一把 SSH 私钥，经主密钥加密后入库，返回不含私钥的密钥记录。
// WebUI 之外的入口（MCP key_generate / key_import）都走这里，保证校验、加密与错误分类一致。
func (m *Manager) CreateKey(ctx context.Context, in KeyInput) (store.Key, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return store.Key{}, &Error{Kind: ErrorInvalid, Err: errors.New("密钥名称不能为空")}
	}
	if utf8.RuneCountInString(name) > maxKeyNameLength {
		return store.Key{}, &Error{Kind: ErrorInvalid, Err: fmt.Errorf("密钥名称最长 %d 个字符", maxKeyNameLength)}
	}
	var (
		private []byte
		public  string
		err     error
	)
	switch in.Mode {
	case "generate":
		private, public, err = sshkeys.Generate(name)
		if err != nil {
			return store.Key{}, &Error{Kind: ErrorInternal, Err: err}
		}
	case "import":
		private, public, err = sshkeys.Import(in.PrivateKey, in.Passphrase, name)
		if err != nil {
			return store.Key{}, &Error{Kind: ErrorInvalid, Err: err}
		}
	default:
		return store.Key{}, &Error{Kind: ErrorInvalid, Err: errors.New("mode 必须是 generate 或 import")}
	}
	enc, err := m.box.Seal(private)
	clear(private)
	if err != nil {
		return store.Key{}, &Error{Kind: ErrorInternal, Err: err}
	}
	key, err := m.store.CreateKey(ctx, name, enc, public)
	if err != nil {
		if KindOf(classifyStoreWrite(err)) == ErrorConflict {
			return store.Key{}, &Error{Kind: ErrorConflict, Err: fmt.Errorf("密钥名称 %q 已存在，请换一个名称", name)}
		}
		return store.Key{}, &Error{Kind: ErrorInternal, Err: err}
	}
	key.PrivateKeyEnc = nil
	return key, nil
}
