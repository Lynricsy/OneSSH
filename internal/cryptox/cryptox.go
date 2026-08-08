package cryptox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"
)

type Box struct{ aead cipher.AEAD }

func New(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("AES-256-GCM 主密钥必须为 32 字节")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

func (b *Box) Seal(plain []byte) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("生成 nonce: %w", err)
	}
	return b.aead.Seal(nonce, nonce, plain, nil), nil
}

func (b *Box) Open(blob []byte) ([]byte, error) {
	n := b.aead.NonceSize()
	if len(blob) < n {
		return nil, fmt.Errorf("密文长度无效")
	}
	plain, err := b.aead.Open(nil, blob[:n], blob[n:], nil)
	if err != nil {
		return nil, fmt.Errorf("解密失败: %w", err)
	}
	return plain, nil
}
