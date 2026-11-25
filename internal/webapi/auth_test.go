package webapi

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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

func postLogin(auth *AdminAuth, remoteAddr, password string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/login", bytes.NewBufferString(`{"password":"`+password+`"}`))
	req.RemoteAddr = remoteAddr
	rec := httptest.NewRecorder()
	auth.Login(rec, req)
	return rec
}

func TestAdminAuthPasswordRotationInvalidatesSession(t *testing.T) {
	masterKey := bytes.Repeat([]byte{0x42}, 32)
	auth1 := NewAdminAuth("old-secret", masterKey)
	rec := postLogin(auth1, "192.0.2.10:12345", "old-secret")
	if rec.Code != http.StatusOK {
		t.Fatalf("登录状态码 = %d，期望 200", rec.Code)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("登录成功后未下发 session cookie")
	}

	protected := func(auth *AdminAuth) int {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/hosts", nil)
		req.AddCookie(cookies[0])
		r := httptest.NewRecorder()
		auth.Require(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(r, req)
		return r.Code
	}

	if code := protected(auth1); code != http.StatusOK {
		t.Fatalf("原密码实例应接受旧 cookie，实际 %d", code)
	}
	// 仅轮换管理员密码、主密钥不变：此前签发的 cookie 必须立即失效。
	auth2 := NewAdminAuth("new-secret", masterKey)
	if code := protected(auth2); code != http.StatusUnauthorized {
		t.Fatalf("密码轮换后旧 cookie 仍有效，状态码 = %d", code)
	}
	// 轮换主密钥同样使旧 cookie 失效。
	auth3 := NewAdminAuth("old-secret", bytes.Repeat([]byte{0x99}, 32))
	if code := protected(auth3); code != http.StatusUnauthorized {
		t.Fatalf("主密钥轮换后旧 cookie 仍有效，状态码 = %d", code)
	}
}

func TestAdminAuthLoginRateLimit(t *testing.T) {
	masterKey := bytes.Repeat([]byte{0x11}, 32)
	current := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	auth := NewAdminAuth("correct-pass", masterKey)
	auth.now = func() time.Time { return current }

	for i := range loginMaxFailures {
		rec := postLogin(auth, "198.51.100.7:50000", "wrong-pass")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("第 %d 次错误登录状态码 = %d，期望 401", i+1, rec.Code)
		}
	}

	// 达到失败上限后即使提交正确密码也应被限流，并返回 Retry-After。
	locked := postLogin(auth, "198.51.100.7:50001", "correct-pass")
	if locked.Code != http.StatusTooManyRequests {
		t.Fatalf("超限后状态码 = %d，期望 429", locked.Code)
	}
	if locked.Header().Get("Retry-After") == "" {
		t.Fatal("429 响应缺少 Retry-After 头")
	}

	// 其他 IP 不受影响，且伪造 X-Forwarded-For 不影响本 IP 限流。
	other := postLogin(auth, "198.51.100.8:50002", "correct-pass")
	if other.Code != http.StatusOK {
		t.Fatalf("其他 IP 登录状态码 = %d，期望 200", other.Code)
	}
	spoofReq := httptest.NewRequest(http.MethodPost, "/api/v1/login", bytes.NewBufferString(`{"password":"correct-pass"}`))
	spoofReq.RemoteAddr = "198.51.100.7:50003"
	spoofReq.Header.Set("X-Forwarded-For", "203.0.113.99")
	spoofRec := httptest.NewRecorder()
	auth.Login(spoofRec, spoofReq)
	if spoofRec.Code != http.StatusTooManyRequests {
		t.Fatalf("伪造 X-Forwarded-For 不应绕过限流，实际状态码 = %d", spoofRec.Code)
	}

	// 锁定期满后恢复正常登录，且成功后清空失败计数。
	current = current.Add(loginLockout + time.Second)
	recovered := postLogin(auth, "198.51.100.7:50004", "correct-pass")
	if recovered.Code != http.StatusOK {
		t.Fatalf("锁定解除后登录状态码 = %d，期望 200", recovered.Code)
	}
	if fail := postLogin(auth, "198.51.100.7:50005", "wrong-pass"); fail.Code != http.StatusUnauthorized {
		t.Fatalf("成功登录后失败计数应清零，实际状态码 = %d", fail.Code)
	}
}
