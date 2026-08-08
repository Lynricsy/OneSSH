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
	if _, err = st.CreateToken(context.Background(), "agent", store.TokenHash("secret"), true, nil); err != nil {
		t.Fatal(err)
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
	Bearer(st, next).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("状态码 %d", rr.Code)
	}
	bad := httptest.NewRecorder()
	Bearer(st, next).ServeHTTP(bad, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if bad.Code != http.StatusUnauthorized {
		t.Fatalf("未授权状态码 %d", bad.Code)
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
