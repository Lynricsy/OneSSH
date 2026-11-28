package sshkeys

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestGenerateRoundTrip(t *testing.T) {
	private, public, err := Generate("unit")
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.ParsePrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(ssh.MarshalAuthorizedKey(signer.PublicKey())); got != public {
		t.Fatalf("公钥不匹配: %q vs %q", got, public)
	}
	if kind, fp := Describe(public); kind != ssh.KeyAlgoED25519 || !strings.HasPrefix(fp, "SHA256:") {
		t.Fatalf("Describe = %q %q", kind, fp)
	}
}

// Windows 上用记事本另存过的私钥常带 BOM 与 CRLF，上传后必须照样能用。
func TestImportToleratesBOMAndCRLF(t *testing.T) {
	private, public, err := Generate("win")
	if err != nil {
		t.Fatal(err)
	}
	windows := append([]byte("\xef\xbb\xbf"), []byte(strings.ReplaceAll(string(private), "\n", "\r\n"))...)
	_, imported, err := Import(windows, nil, "win")
	if err != nil {
		t.Fatalf("导入带 BOM/CRLF 的私钥失败: %v", err)
	}
	if imported != public {
		t.Fatal("导入后的公钥与原公钥不一致")
	}
}

func TestImportRejectsCommonWrongFiles(t *testing.T) {
	_, public, err := Generate("pub")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		input []byte
		want  error
	}{
		"公钥":   {[]byte(public), ErrPublicKey},
		"ppk":  {[]byte("PuTTY-User-Key-File-3: ssh-ed25519\nEncryption: none\n"), ErrPuTTY},
		"空白":   {[]byte("  \r\n "), ErrEmpty},
		"超大文件": {make([]byte, MaxPrivateKeyBytes+1), ErrTooLarge},
	}
	for name, tc := range cases {
		if _, _, err := Import(tc.input, nil, name); !errors.Is(err, tc.want) {
			t.Fatalf("%s: err = %v，期望 %v", name, err, tc.want)
		}
	}
}

func TestImportPassphraseProtected(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "enc", []byte("s3cret"))
	if err != nil {
		t.Fatal(err)
	}
	encrypted := pem.EncodeToMemory(block)
	if _, _, err = Import(encrypted, nil, "enc"); !errors.Is(err, ErrPassphraseRequired) {
		t.Fatalf("缺口令应报 ErrPassphraseRequired，得到 %v", err)
	}
	if _, _, err = Import(encrypted, []byte("wrong"), "enc"); !errors.Is(err, ErrWrongPassphrase) {
		t.Fatalf("错口令应报 ErrWrongPassphrase，得到 %v", err)
	}
	plain, _, err := Import(encrypted, []byte("s3cret"), "enc")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ssh.ParsePrivateKey(plain); err != nil {
		t.Fatalf("解密后应为无口令私钥: %v", err)
	}
}
