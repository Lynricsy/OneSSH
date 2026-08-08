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
	protected := a.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookies[0])
	ok := httptest.NewRecorder()
	protected.ServeHTTP(ok, req)
	if ok.Code != http.StatusNoContent {
		t.Fatalf("鉴权状态码 %d", ok.Code)
	}
}
