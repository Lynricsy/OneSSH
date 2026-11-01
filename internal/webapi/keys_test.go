package webapi

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"onessh/internal/cryptox"
	"onessh/internal/hostmanager"
	"onessh/internal/memoryx"
	"onessh/internal/sshpool"
	"onessh/internal/store"
)

func TestImportPrivateKeyWithPassphrase(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	box, err := cryptox.New(bytes.Repeat([]byte{8}, 32))
	if err != nil {
		t.Fatal(err)
	}
	pool := sshpool.New(st, box)
	defer pool.Close()
	api := NewAPI(st, box, pool, hostmanager.New(st, box, pool), nil, nil, nil, nil, memoryx.New(st, memoryx.EmbeddingConfig{}), nil).Handler()
	importKey := func(name string, private []byte, passphrase string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(keyInput{Name: name, Mode: "import", PrivateKey: string(private), Passphrase: passphrase})
		r := httptest.NewRequest(http.MethodPost, "/keys", bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)
		return w
	}

	_, edKey, _ := ed25519.GenerateKey(rand.Reader)
	ecKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	rsaKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	encrypted := func(t *testing.T, key crypto.PrivateKey) []byte {
		block, err := ssh.MarshalPrivateKeyWithPassphrase(key, "", []byte("correct horse"))
		if err != nil {
			t.Fatal(err)
		}
		return pem.EncodeToMemory(block)
	}

	for _, tc := range []struct {
		name string
		key  crypto.PrivateKey
	}{{"ed25519", edKey}, {"ecdsa", ecKey}, {"rsa", rsaKey}} {
		t.Run(tc.name, func(t *testing.T) {
			private := encrypted(t, tc.key)

			if w := importKey(tc.name+"-missing", private, ""); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "口令保护") {
				t.Fatalf("缺少口令应提示填写: %d %s", w.Code, w.Body.String())
			}
			if w := importKey(tc.name+"-wrong", private, "wrong"); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "口令错误") {
				t.Fatalf("错误口令应明确报错: %d %s", w.Code, w.Body.String())
			}

			w := importKey(tc.name, private, "correct horse")
			if w.Code != http.StatusCreated {
				t.Fatalf("正确口令导入失败: %d %s", w.Code, w.Body.String())
			}
			var out struct {
				ID        int64  `json:"id"`
				PublicKey string `json:"public_key"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			signer, err := ssh.NewSignerFromKey(tc.key)
			if err != nil {
				t.Fatal(err)
			}
			want := string(ssh.MarshalAuthorizedKey(signer.PublicKey()))
			if out.PublicKey != want {
				t.Fatalf("公钥 = %q，期望 %q", out.PublicKey, want)
			}

			// 入库的私钥必须能被连接阶段的 ssh.ParsePrivateKey 直接使用，不再需要口令。
			stored, err := st.GetKey(context.Background(), out.ID)
			if err != nil {
				t.Fatal(err)
			}
			plain, err := box.Open(stored.PrivateKeyEnc)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := ssh.ParsePrivateKey(plain)
			if err != nil {
				t.Fatalf("入库私钥无法无口令解析: %v", err)
			}
			if got := string(ssh.MarshalAuthorizedKey(parsed.PublicKey())); got != want {
				t.Fatalf("入库私钥公钥 = %q，期望 %q", got, want)
			}
		})
	}

	t.Run("未加密私钥忽略口令", func(t *testing.T) {
		block, err := ssh.MarshalPrivateKey(edKey, "")
		if err != nil {
			t.Fatal(err)
		}
		if w := importKey("plain", pem.EncodeToMemory(block), "unused"); w.Code != http.StatusCreated {
			t.Fatalf("未加密私钥附带口令导入失败: %d %s", w.Code, w.Body.String())
		}
	})
}
