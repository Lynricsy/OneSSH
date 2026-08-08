package webapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"onessh/internal/cryptox"
	"onessh/internal/sshpool"
	"onessh/internal/store"
)

func TestAdminKeyHostTokenLifecycle(t *testing.T) {
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
	api := NewAPI(st, box, pool, nil, nil, nil, nil, nil).Handler()
	call := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)
		return w
	}
	key := call(http.MethodPost, "/keys", `{"name":"generated","mode":"generate"}`)
	if key.Code != http.StatusCreated {
		t.Fatalf("创建密钥 %d %s", key.Code, key.Body.String())
	}
	var keyOut map[string]any
	if json.Unmarshal(key.Body.Bytes(), &keyOut) != nil || keyOut["public_key"] == "" {
		t.Fatalf("公钥响应 %s", key.Body.String())
	}
	if strings.Contains(key.Body.String(), "PRIVATE KEY") {
		t.Fatal("响应泄漏私钥")
	}
	host := call(http.MethodPost, "/hosts", `{"name":"ssh","addr":"127.0.0.1","port":2222,"username":"test","auth_type":"password","password":"pass"}`)
	if host.Code != http.StatusCreated {
		t.Fatalf("创建主机 %d %s", host.Code, host.Body.String())
	}
	if strings.Contains(host.Body.String(), `"password":"pass"`) || strings.Contains(host.Body.String(), "password_enc") {
		t.Fatal("响应泄漏密码")
	}
	token := call(http.MethodPost, "/tokens", `{"name":"agent","all_hosts":true}`)
	if token.Code != http.StatusCreated {
		t.Fatalf("创建令牌 %d %s", token.Code, token.Body.String())
	}
	var tokenOut map[string]any
	_ = json.Unmarshal(token.Body.Bytes(), &tokenOut)
	if !strings.HasPrefix(tokenOut["token"].(string), "osh_") {
		t.Fatalf("令牌响应 %s", token.Body.String())
	}
	list := call(http.MethodGet, "/tokens", "")
	if list.Code != http.StatusOK || strings.Contains(list.Body.String(), "osh_") {
		t.Fatalf("列表泄漏令牌 %s", list.Body.String())
	}
}
