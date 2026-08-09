package webapi

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAdminLoginCookie(t *testing.T) {
	a := NewAdminAuth("pw", bytes.Repeat([]byte{1}, 32))
	rr := httptest.NewRecorder()
	a.Login(rr, httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"password":"pw"}`)))
	if rr.Code != http.StatusOK {
		t.Fatalf("登录状态码 %d", rr.Code)
	}
	cookies := rr.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("未设置 cookie")
	}
	if cookies[0].Secure {
		t.Fatal("HTTP 登录不应设置 Secure cookie")
	}
	protected := a.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookies[0])
	ok := httptest.NewRecorder()
	protected.ServeHTTP(ok, req)
	if ok.Code != http.StatusNoContent {
		t.Fatalf("鉴权状态码 %d", ok.Code)
	}
}

func TestAdminLoginCookieSecureForHTTPS(t *testing.T) {
	a := NewAdminAuth("pw", bytes.Repeat([]byte{1}, 32))
	for _, test := range []struct {
		name           string
		target         string
		forwardedProto string
		wantSecure     bool
	}{
		{name: "direct HTTPS", target: "https://onessh.example/api/v1/login", wantSecure: true},
		{name: "trusted HTTPS proxy", target: "http://onessh.internal/api/v1/login", forwardedProto: "https", wantSecure: true},
		{name: "plain HTTP proxy", target: "http://onessh.internal/api/v1/login", forwardedProto: "http", wantSecure: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, test.target, bytes.NewBufferString(`{"password":"pw"}`))
			if test.forwardedProto != "" {
				request.Header.Set("X-Forwarded-Proto", test.forwardedProto)
			}
			response := httptest.NewRecorder()
			a.Login(response, request)
			cookies := response.Result().Cookies()
			if len(cookies) != 1 {
				t.Fatalf("cookie 数量 = %d", len(cookies))
			}
			if cookies[0].Secure != test.wantSecure {
				t.Fatalf("Secure = %t, want %t", cookies[0].Secure, test.wantSecure)
			}
		})
	}
}
