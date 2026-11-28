package webapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"onessh/internal/store"
)

func newStoreAuth(t *testing.T) (*AdminAuth, http.Handler) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	auth := NewAdminAuth("Env-pass1", bytes.Repeat([]byte{3}, 32))
	if err := auth.UseStore(st.DB); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	auth.registerAccount(mux)
	return auth, auth.Require(mux)
}

func login(t *testing.T, auth *AdminAuth, body, ip string) *http.Cookie {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/login", strings.NewReader(body))
	req.RemoteAddr = ip
	rec := httptest.NewRecorder()
	auth.Login(rec, req)
	if rec.Code != 200 {
		return nil
	}
	return rec.Result().Cookies()[0]
}

func callWith(h http.Handler, cookie *http.Cookie, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAccountPasswordChangeAndSessions(t *testing.T) {
	auth, h := newStoreAuth(t)
	if login(t, auth, `{"username":"root","password":"Env-pass1"}`, "192.0.2.1:1") != nil {
		t.Fatal("错误用户名不应登录成功")
	}
	a := login(t, auth, `{"username":"ADMIN","password":"Env-pass1","remember":true}`, "192.0.2.1:1")
	b := login(t, auth, `{"username":"admin","password":"Env-pass1"}`, "192.0.2.2:1")
	if a == nil || b == nil {
		t.Fatal("默认用户名 admin（不区分大小写）应能登录")
	}
	if a.MaxAge != int(sessionLongTTL.Seconds()) || b.MaxAge != int(sessionShortTTL.Seconds()) {
		t.Fatalf("记住我时长不对: %d / %d", a.MaxAge, b.MaxAge)
	}

	var info accountInfo
	_ = json.Unmarshal(callWith(h, a, "GET", "/account", "").Body.Bytes(), &info)
	if info.Username != "admin" || info.PasswordSource != "env" || len(info.Sessions) != 2 || len(info.Logins) < 3 {
		t.Fatalf("账户信息 = %+v", info)
	}

	if w := callWith(h, a, "PUT", "/account/password", `{"current":"wrong","new":"New-pass99"}`); w.Code != 403 {
		t.Fatalf("当前密码错误应 403，得到 %d", w.Code)
	}
	if w := callWith(h, a, "PUT", "/account/password", `{"current":"Env-pass1","new":"short"}`); w.Code != 400 {
		t.Fatalf("弱密码应 400，得到 %d", w.Code)
	}
	if w := callWith(h, a, "PUT", "/account/password", `{"current":"Env-pass1","new":"New-pass99"}`); w.Code != 200 {
		t.Fatalf("改密码 %d %s", w.Code, w.Body.String())
	}
	if w := callWith(h, b, "GET", "/account", ""); w.Code != 401 {
		t.Fatalf("改密码后其他会话应失效，得到 %d", w.Code)
	}
	if w := callWith(h, a, "GET", "/account", ""); w.Code != 200 {
		t.Fatalf("改密码的会话应保留，得到 %d", w.Code)
	}
	if login(t, auth, `{"password":"Env-pass1"}`, "192.0.2.3:1") != nil {
		t.Fatal("改密码后旧的环境变量密码不应再能登录")
	}
	c := login(t, auth, `{"username":"admin","password":"New-pass99"}`, "192.0.2.3:1")
	if c == nil {
		t.Fatal("新密码应能登录")
	}

	if w := callWith(h, a, "PUT", "/account/profile", `{"username":"ops-lead"}`); w.Code != 200 {
		t.Fatalf("改用户名 %d %s", w.Code, w.Body.String())
	}
	if login(t, auth, `{"username":"ops-lead","password":"New-pass99"}`, "192.0.2.4:1") == nil {
		t.Fatal("新用户名应能登录")
	}
	if w := callWith(h, a, "POST", "/account/sessions/revoke-others", ""); w.Code != 200 {
		t.Fatalf("注销其他会话 %d", w.Code)
	}
	if w := callWith(h, c, "GET", "/account", ""); w.Code != 401 {
		t.Fatalf("被注销的会话应失效，得到 %d", w.Code)
	}

	// 退出登录后 cookie 立即失效
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/logout", nil)
	req.AddCookie(a)
	auth.Logout(rec, req)
	if w := callWith(h, a, "GET", "/account", ""); w.Code != 401 {
		t.Fatalf("退出后会话应失效，得到 %d", w.Code)
	}
}

func TestAccountResetFromEnv(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	auth := NewAdminAuth("Env-pass1", bytes.Repeat([]byte{3}, 32))
	_ = auth.UseStore(st.DB)
	_ = auth.setSetting("password_hash", "$2a$12$invalidhashinvalidhashinvalidhashinvalidhashinvalidha")
	if auth.checkPassword("Env-pass1") {
		t.Fatal("有自定义密码时不应接受环境变量密码")
	}
	t.Setenv("ONESSH_ADMIN_RESET", "1")
	reset := NewAdminAuth("Env-pass1", bytes.Repeat([]byte{3}, 32))
	if err := reset.UseStore(st.DB); err != nil {
		t.Fatal(err)
	}
	if !reset.checkPassword("Env-pass1") {
		t.Fatal("ONESSH_ADMIN_RESET 后应恢复环境变量密码")
	}
}

func TestGuardDelete(t *testing.T) {
	for p, blocked := range map[string]bool{"/": true, "/etc": true, "/root": true, "/home/u": true, "/etc/nginx": false, "/home/u/tmp": false, "/srv/app": false} {
		if got := guardDelete(p, "/home/u") != nil; got != blocked {
			t.Fatalf("guardDelete(%s) 拦截=%v，期望 %v", p, got, blocked)
		}
	}
}

func TestInlineTypeNeverServesHTML(t *testing.T) {
	for _, name := range []string{"index.html", "a.htm", "x.xhtml", "run.js"} {
		if typ, _ := inlineType(name); !strings.HasPrefix(typ, "text/plain") {
			t.Fatalf("%s 预览类型 = %s，必须是纯文本", name, typ)
		}
	}
	if typ, _ := inlineType("a.png"); typ != "image/png" {
		t.Fatalf("png 类型 = %s", typ)
	}
}

func TestSysinfoParse(t *testing.T) {
	out := "\n@@hostname@@\nweb-01\n@@os@@\nPRETTY_NAME=\"Ubuntu 24.04.4 LTS\"\nID=ubuntu\nVERSION_ID=\"24.04\"\n@@kernel@@\n6.8.0-136-generic\n@@arch@@\nx86_64\n" +
		"@@uptime@@\n2928000.12 100.0\n@@cpuinfo@@\nmodel name\t: Intel(R) Xeon(R) Gold 6278C CPU @ 2.60GHz\n@@cpus@@\n2\n" +
		"@@meminfo@@\nMemTotal:         787228 kB\nMemAvailable:     216240 kB\nSwapTotal:             0 kB\n@@loadavg@@\n0.00 0.33 5.42 1/200 99\n@@virt@@\nkvm\n" +
		"@@disks@@\nFilesystem Type 1024-blocks Used Available Capacity Mounted on\n/dev/vda1 ext4 41169244 7604332 31800000 20% /\n" +
		"@@ips@@\n2: eth0    inet 192.168.0.5/24 brd 192.168.0.255 scope global eth0\n" +
		"@@ports@@\nLISTEN 0 511 0.0.0.0:80 0.0.0.0:* users:((\"nginx\",pid=1,fd=9))\nLISTEN 0 4096 *:443 *:* users:((\"xray\",pid=2,fd=6))\n" +
		"@@top@@\n  PID USER %CPU %MEM RSS COMMAND\n  123 root 5.0 2.1 16000 nginx\n@@end@@\n"
	info := parseSysinfo(out)
	if info.Hostname != "web-01" || info.OSName != "Ubuntu 24.04.4 LTS" || info.Kernel != "6.8.0-136-generic" || info.CPUCores != 2 {
		t.Fatalf("基础字段 = %+v", info)
	}
	if !strings.Contains(info.CPUModel, "Xeon") || info.MemTotalKB != 787228 || info.Virtualization != "kvm" || len(info.Load) != 3 {
		t.Fatalf("硬件字段 = %+v", info)
	}
	if len(info.Disks) != 1 || info.Disks[0].Mount != "/" || info.Disks[0].FSType != "ext4" {
		t.Fatalf("磁盘 = %+v", info.Disks)
	}
	if len(info.Ports) != 2 || info.Ports[0].Port != "80" || info.Ports[0].Process != "nginx" || info.Ports[1].Port != "443" {
		t.Fatalf("端口 = %+v", info.Ports)
	}
	if len(info.Addrs) != 1 || info.Addrs[0].Iface != "eth0" || len(info.Top) != 1 || info.Top[0].Comm != "nginx" {
		t.Fatalf("网络/进程 = %+v %+v", info.Addrs, info.Top)
	}
}
