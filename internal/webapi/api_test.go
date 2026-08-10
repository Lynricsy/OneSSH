package webapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"onessh/internal/cryptox"
	"onessh/internal/hostmanager"
	"onessh/internal/memoryx"
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
	hosts := hostmanager.New(st, box, pool)
	api := NewAPI(st, box, pool, hosts, nil, nil, nil, nil, memoryx.New(st, memoryx.EmbeddingConfig{}), nil).Handler()
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
	token := call(http.MethodPost, "/tokens", `{"name":"manager","all_hosts":false,"manage_hosts":true,"host_ids":[]}`)
	if token.Code != http.StatusCreated {
		t.Fatalf("创建管理令牌 %d %s", token.Code, token.Body.String())
	}
	var tokenOut map[string]any
	_ = json.Unmarshal(token.Body.Bytes(), &tokenOut)
	if !strings.HasPrefix(tokenOut["token"].(string), "osh_") || tokenOut["manage_hosts"] != true {
		t.Fatalf("管理令牌响应 %s", token.Body.String())
	}
	ordinary := call(http.MethodPost, "/tokens", `{"name":"ordinary","all_hosts":true}`)
	if ordinary.Code != http.StatusCreated {
		t.Fatalf("创建默认令牌 %d %s", ordinary.Code, ordinary.Body.String())
	}
	var ordinaryOut map[string]any
	_ = json.Unmarshal(ordinary.Body.Bytes(), &ordinaryOut)
	if ordinaryOut["manage_hosts"] != false {
		t.Fatalf("默认令牌意外获得管理权限: %s", ordinary.Body.String())
	}
	list := call(http.MethodGet, "/tokens", "")
	if list.Code != http.StatusOK || strings.Contains(list.Body.String(), "osh_") {
		t.Fatalf("列表泄漏令牌 %s", list.Body.String())
	}
	var listed []map[string]any
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	permissions := make(map[string]bool, len(listed))
	for _, item := range listed {
		permissions[item["name"].(string)] = item["manage_hosts"].(bool)
	}
	if !permissions["manager"] || permissions["ordinary"] {
		t.Fatalf("令牌列表权限异常: %#v", permissions)
	}
}

func TestHostErrorStatus(t *testing.T) {
	tests := []struct {
		kind hostmanager.ErrorKind
		want int
	}{
		{kind: hostmanager.ErrorInvalid, want: http.StatusBadRequest},
		{kind: hostmanager.ErrorNotFound, want: http.StatusNotFound},
		{kind: hostmanager.ErrorConflict, want: http.StatusConflict},
		{kind: hostmanager.ErrorConnection, want: http.StatusBadGateway},
		{kind: hostmanager.ErrorInternal, want: http.StatusInternalServerError},
	}
	for _, test := range tests {
		err := &hostmanager.Error{Kind: test.kind, Err: errors.New("测试错误")}
		if got := hostErrorStatus(err); got != test.want {
			t.Fatalf("错误类型 %d 状态码 = %d，期望 %d", test.kind, got, test.want)
		}
	}
}
