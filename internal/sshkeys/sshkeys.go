// Package sshkeys 集中处理 SSH 私钥的生成、规范化与导入。
//
// WebUI 的密钥页、主机弹窗里的「上传私钥文件」以及 MCP 的 key_generate / key_import
// 共用这一套解析规则：同一份私钥文件，无论从哪个入口进来，被接受或被拒绝的理由都必须一致。
package sshkeys

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"

	"golang.org/x/crypto/ssh"
)

// MaxPrivateKeyBytes 是单把私钥原文的上限。16384 位 RSA 的 PEM 也只有十几 KiB，
// 超过这个量级基本可以断定选错了文件，没必要把整个文件读进内存再解析失败。
const MaxPrivateKeyBytes = 64 << 10

var (
	ErrEmpty              = errors.New("私钥内容为空")
	ErrTooLarge           = fmt.Errorf("私钥文件超过 %d KiB，请确认选择的是私钥文件", MaxPrivateKeyBytes>>10)
	ErrPublicKey          = errors.New("这是公钥（.pub）内容，请选择对应的私钥文件（通常是不带 .pub 后缀的同名文件）")
	ErrPuTTY              = errors.New("暂不支持 PuTTY .ppk 格式，请先用 PuTTYgen 的 Conversions → Export OpenSSH key 导出为 OpenSSH 私钥")
	ErrPassphraseRequired = errors.New("该私钥受口令保护，请填写口令")
	ErrWrongPassphrase    = errors.New("私钥口令错误")
)

// Generate 生成一把 ed25519 密钥，返回 OpenSSH 格式私钥与 authorized_keys 格式公钥。
func Generate(comment string) ([]byte, string, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", err
	}
	block, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return nil, "", err
	}
	private := pem.EncodeToMemory(block)
	clear(block.Bytes)
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, "", err
	}
	return private, string(ssh.MarshalAuthorizedKey(sshPub)), nil
}

// Normalize 把用户上传的私钥文件整理成解析器认得的样子：去掉 Windows 记事本常加的 UTF-8 BOM、
// 统一 CRLF 换行、去掉首尾空白并补一个结尾换行。同时提前识别两类最常见的「选错文件」——
// 公钥与 PuTTY .ppk，给出能直接照做的提示，而不是一句晦涩的 ssh: no key found。
// 返回的切片总是新分配的，调用方用完可以放心清零。
func Normalize(raw []byte) ([]byte, error) {
	if len(raw) > MaxPrivateKeyBytes {
		return nil, ErrTooLarge
	}
	text := bytes.ReplaceAll(bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf")), []byte("\r\n"), []byte("\n"))
	trimmed := bytes.TrimSpace(text)
	if len(trimmed) == 0 {
		return nil, ErrEmpty
	}
	if bytes.HasPrefix(trimmed, []byte("PuTTY-User-Key-File")) {
		return nil, ErrPuTTY
	}
	if _, _, _, _, err := ssh.ParseAuthorizedKey(trimmed); err == nil {
		return nil, ErrPublicKey
	}
	out := make([]byte, 0, len(trimmed)+1)
	out = append(out, trimmed...)
	out = append(out, '\n')
	clear(text)
	return out, nil
}

// Import 解析导入的私钥，返回待加密入库的私钥与 authorized_keys 格式公钥。
// 未加密私钥原样保存，此时 passphrase 被忽略；受口令保护的私钥在这里解密并重新编码为
// 无口令 OpenSSH 格式：静态保护交给 OneSSH 主密钥，连接阶段仍走 ssh.ParsePrivateKey，口令不落库。
func Import(raw, passphrase []byte, comment string) ([]byte, string, error) {
	defer clear(passphrase)
	pemBytes, err := Normalize(raw)
	if err != nil {
		return nil, "", err
	}
	signer, err := ssh.ParsePrivateKey(pemBytes)
	if err == nil {
		return pemBytes, string(ssh.MarshalAuthorizedKey(signer.PublicKey())), nil
	}
	var missing *ssh.PassphraseMissingError
	if !errors.As(err, &missing) {
		clear(pemBytes)
		return nil, "", fmt.Errorf("解析私钥: %w", err)
	}
	if len(passphrase) == 0 {
		clear(pemBytes)
		return nil, "", ErrPassphraseRequired
	}
	rawKey, err := ssh.ParseRawPrivateKeyWithPassphrase(pemBytes, passphrase)
	clear(pemBytes)
	if errors.Is(err, x509.IncorrectPasswordError) {
		return nil, "", ErrWrongPassphrase
	}
	if err != nil {
		return nil, "", fmt.Errorf("解密私钥: %w", err)
	}
	signer, err = ssh.NewSignerFromKey(rawKey)
	if err != nil {
		return nil, "", fmt.Errorf("解析私钥: %w", err)
	}
	// DSA 等 OpenSSH 新格式不支持的类型会在这里失败，不能退回保存加密原文，否则连接阶段无法使用。
	block, err := ssh.MarshalPrivateKey(rawKey, comment)
	if err != nil {
		return nil, "", fmt.Errorf("该私钥类型不支持口令导入: %w", err)
	}
	plain := pem.EncodeToMemory(block)
	clear(block.Bytes)
	return plain, string(ssh.MarshalAuthorizedKey(signer.PublicKey())), nil
}

// Describe 从 authorized_keys 格式公钥里取出算法与 SHA256 指纹，解析失败时返回空串。
func Describe(public string) (keyType, fingerprint string) {
	pk, _, _, _, err := ssh.ParseAuthorizedKey([]byte(public))
	if err != nil {
		return "", ""
	}
	return pk.Type(), ssh.FingerprintSHA256(pk)
}
