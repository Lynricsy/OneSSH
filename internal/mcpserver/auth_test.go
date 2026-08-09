package mcpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"onessh/internal/store"
)

func TestBearerAttachesPrincipal(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err = st.CreateToken(context.Background(), store.TokenCreate{Name: "agent", Hash: store.TokenHash("secret"), AllHosts: true}); err != nil {
		t.Fatal(err)
	}
	resolve := func(*http.Request) (string, string) {
		return "https://onessh.example/mcp", "https://onessh.example/.well-known/oauth-protected-resource/mcp"
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := FromContext(r.Context())
		if !ok || p.Token.Name != "agent" {
			t.Fatal("缺少 principal")
		}
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rr := httptest.NewRecorder()
	Bearer(st, resolve, next).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("状态码 %d", rr.Code)
	}
	bad := httptest.NewRecorder()
	Bearer(st, resolve, next).ServeHTTP(bad, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if bad.Code != http.StatusUnauthorized {
		t.Fatalf("未授权状态码 %d", bad.Code)
	}
	if challenge := bad.Header().Get("WWW-Authenticate"); challenge != `Bearer resource_metadata="https://onessh.example/.well-known/oauth-protected-resource/mcp", scope="mcp"` {
		t.Fatalf("鉴权挑战异常: %s", challenge)
	}
}

func TestAuthorizedHostDistinguishesUnknown(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err = st.CreateHost(context.Background(), store.Host{Name: "known", Addr: "127.0.0.1", Port: 22, Username: "user", AuthType: "password"}); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), principalKey{}, Principal{Hosts: map[string]store.Host{}, Store: st})
	if _, err = AuthorizedHost(ctx, "missing"); err == nil || err.Error() != "unknown host: missing" {
		t.Fatalf("未知主机错误: %v", err)
	}
	if _, err = AuthorizedHost(ctx, "known"); err == nil || err.Error() != "host not authorized: known" {
		t.Fatalf("越权主机错误: %v", err)
	}
}

func TestAuthorizedHostManagementDoesNotExpandExecutionHosts(t *testing.T) {
	if _, err := AuthorizedHostManagement(context.Background()); err == nil || err.Error() != "unauthorized" {
		t.Fatalf("缺少身份错误: %v", err)
	}
	denied := context.WithValue(context.Background(), principalKey{}, Principal{Token: store.Token{ManageHosts: false}})
	if _, err := AuthorizedHostManagement(denied); err == nil || err.Error() != "host management not authorized" {
		t.Fatalf("缺少管理权限错误: %v", err)
	}
	ctx := context.WithValue(context.Background(), principalKey{}, Principal{
		Token: store.Token{ManageHosts: true},
		Hosts: map[string]store.Host{},
	})
	if _, err := AuthorizedHostManagement(ctx); err != nil {
		t.Fatalf("管理权限被拒绝: %v", err)
	}
	if _, err := AuthorizedHost(ctx, "any"); err == nil || err.Error() != "host not authorized: any" {
		t.Fatalf("管理权限扩张了执行授权: %v", err)
	}
}
