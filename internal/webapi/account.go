package webapi

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

// 管理员账户：用户名、可在控制台修改的密码、服务端会话与登录记录。
//
// 没有调用 UseStore 时（单元测试、旧调用方）退回无状态模式：cookie 只靠 HMAC 校验，
// 密码只认 ONESSH_ADMIN_PASSWORD。接入数据库后：
//   - 在控制台改过密码，就以库里的 bcrypt 摘要为准，环境变量只作为兜底；
//     ONESSH_ADMIN_RESET=1 启动一次即可把密码恢复为环境变量里的值。
//   - 每次登录在 admin_sessions 记一行（只存会话令牌的 SHA-256），可列出、单独注销、
//     改密码时自动踢掉其他设备。

const (
	sessionCookie     = "onessh_session"
	sessionShortTTL   = 24 * time.Hour
	sessionLongTTL    = 30 * 24 * time.Hour
	sessionTouchEvery = time.Minute
	minPasswordLen    = 8
	maxPasswordLen    = 128
	loginLogKeep      = 300
	defaultUsername   = "admin"
)

type sessionCtxKey struct{}

// UseStore 打开数据库模式。表按需建立（不走迁移），回退旧版本二进制时多出的表不影响任何东西。
func (a *AdminAuth) UseStore(db *sql.DB) error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS admin_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS admin_sessions (
			id TEXT PRIMARY KEY, pub TEXT NOT NULL, created_at INTEGER NOT NULL, last_seen INTEGER NOT NULL,
			expires_at INTEGER NOT NULL, ip TEXT NOT NULL DEFAULT '', user_agent TEXT NOT NULL DEFAULT '', remember INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE IF NOT EXISTS admin_login_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT, ts INTEGER NOT NULL, ip TEXT NOT NULL DEFAULT '', user_agent TEXT NOT NULL DEFAULT '',
			username TEXT NOT NULL DEFAULT '', ok INTEGER NOT NULL, reason TEXT NOT NULL DEFAULT '')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	a.db = db
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("ONESSH_ADMIN_RESET"))); v == "1" || v == "true" || v == "yes" {
		if _, err := db.Exec(`DELETE FROM admin_settings WHERE key IN ('password_hash','password_changed_at')`); err != nil {
			return err
		}
		_, _ = db.Exec(`DELETE FROM admin_sessions`)
		log.Printf("ONESSH_ADMIN_RESET：管理员密码已恢复为 ONESSH_ADMIN_PASSWORD，所有登录会话已注销")
	}
	if a.setting("username") == "" {
		if err := a.setSetting("username", defaultUsername); err != nil {
			return err
		}
	}
	_, _ = db.Exec(`DELETE FROM admin_sessions WHERE expires_at < ?`, a.now().Unix())
	return nil
}

func (a *AdminAuth) setting(key string) string {
	if a.db == nil {
		return ""
	}
	var v string
	_ = a.db.QueryRow(`SELECT value FROM admin_settings WHERE key=?`, key).Scan(&v)
	return v
}

func (a *AdminAuth) setSetting(key, value string) error {
	_, err := a.db.Exec(`INSERT INTO admin_settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

// Username 返回当前管理员用户名；无状态模式下固定为 admin。
func (a *AdminAuth) Username() string {
	if name := a.setting("username"); name != "" {
		return name
	}
	return defaultUsername
}

func (a *AdminAuth) checkPassword(pw string) bool {
	if hash := a.setting("password_hash"); hash != "" {
		return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
	}
	return subtle.ConstantTimeCompare([]byte(pw), []byte(a.password)) == 1
}

func newSessionID() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func hashSessionID(id string) string {
	sum := sha256.Sum256([]byte("onessh-session:" + id))
	return hex.EncodeToString(sum[:])
}

func (a *AdminAuth) sign(msg string) string {
	mac := hmac.New(sha256.New, a.key)
	mac.Write([]byte(msg))
	return hex.EncodeToString(mac.Sum(nil))
}

func (a *AdminAuth) recordLogin(r *http.Request, username string, ok bool, reason string) {
	if a.db == nil {
		return
	}
	_, _ = a.db.Exec(`INSERT INTO admin_login_log(ts,ip,user_agent,username,ok,reason) VALUES(?,?,?,?,?,?)`,
		a.now().Unix(), clientIP(r), truncateUA(r.UserAgent()), truncateRunes(username, 64), boolInt(ok), reason)
	_, _ = a.db.Exec(`DELETE FROM admin_login_log WHERE id <= (SELECT id FROM admin_login_log ORDER BY id DESC LIMIT 1 OFFSET ?)`, loginLogKeep)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

func truncateUA(ua string) string { return truncateRunes(ua, 300) }

func (a *AdminAuth) Login(w http.ResponseWriter, r *http.Request) {
	now := a.now()
	ip := clientIP(r)
	if retryAfter, ok := a.checkRateLimit(ip, now); !ok {
		seconds := max(int(retryAfter.Seconds()), 1)
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
		a.recordLogin(r, "", false, "rate_limited")
		http.Error(w, fmt.Sprintf("登录失败次数过多，请 %d 分钟后再试", (seconds+59)/60), http.StatusTooManyRequests)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Remember bool   `json:"remember"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		http.Error(w, "请求格式错误", http.StatusBadRequest)
		return
	}
	in.Username = strings.TrimSpace(in.Username)
	// 用户名不区分大小写；不传用户名只校验密码（兼容旧客户端与脚本）
	userOK := in.Username == "" || strings.EqualFold(in.Username, a.Username())
	passOK := a.checkPassword(in.Password)
	if !userOK || !passOK {
		a.recordLoginFailure(ip, now)
		reason := "bad_password"
		if !userOK {
			reason = "bad_username"
		}
		a.recordLogin(r, in.Username, false, reason)
		http.Error(w, "用户名或密码错误", http.StatusUnauthorized)
		return
	}
	a.clearLoginFailures(ip)

	ttl := sessionShortTTL
	if in.Remember {
		ttl = sessionLongTTL
	}
	exp := now.Add(ttl).Unix()
	id, err := newSessionID()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if a.db != nil {
		hash := hashSessionID(id)
		if _, err := a.db.Exec(`INSERT INTO admin_sessions(id,pub,created_at,last_seen,expires_at,ip,user_agent,remember) VALUES(?,?,?,?,?,?,?,?)`,
			hash, hash[:16], now.Unix(), now.Unix(), exp, ip, truncateUA(r.UserAgent()), boolInt(in.Remember)); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = a.db.Exec(`DELETE FROM admin_sessions WHERE expires_at < ?`, now.Unix())
	}
	a.recordLogin(r, in.Username, true, "")
	msg := "v2." + id + "." + strconv.FormatInt(exp, 10)
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: msg + "." + a.sign(msg), Path: "/", HttpOnly: true,
		SameSite: http.SameSiteStrictMode, Secure: requestIsHTTPS(r), MaxAge: int(ttl.Seconds()),
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "username": a.Username(), "expires_at": exp})
}

// Logout 注销当前会话并清掉 cookie；没有有效会话也返回成功，保证「退出」按钮总能把人送回登录页。
func (a *AdminAuth) Logout(w http.ResponseWriter, r *http.Request) {
	if hash, ok := a.session(r); ok && a.db != nil {
		_, _ = a.db.Exec(`DELETE FROM admin_sessions WHERE id=?`, hash)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: requestIsHTTPS(r), MaxAge: -1})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

var (
	touchMu   sync.Mutex
	lastTouch = map[string]time.Time{}
)

// session 校验 cookie，返回会话令牌的摘要（无状态模式下为空串）。
func (a *AdminAuth) session(r *http.Request) (string, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return "", false
	}
	parts := strings.Split(c.Value, ".")
	if len(parts) != 4 || parts[0] != "v2" {
		return "", false
	}
	msg := strings.Join(parts[:3], ".")
	got, err := hex.DecodeString(parts[3])
	if err != nil {
		return "", false
	}
	want, _ := hex.DecodeString(a.sign(msg))
	if !hmac.Equal(got, want) {
		return "", false
	}
	exp, err := strconv.ParseInt(parts[2], 10, 64)
	now := a.now()
	if err != nil || now.Unix() > exp {
		return "", false
	}
	if a.db == nil {
		return "", true
	}
	hash := hashSessionID(parts[1])
	var expires int64
	if err := a.db.QueryRow(`SELECT expires_at FROM admin_sessions WHERE id=?`, hash).Scan(&expires); err != nil || now.Unix() > expires {
		return "", false
	}
	touchMu.Lock()
	stale := now.Sub(lastTouch[hash]) >= sessionTouchEvery
	if stale {
		lastTouch[hash] = now
	}
	touchMu.Unlock()
	if stale {
		_, _ = a.db.Exec(`UPDATE admin_sessions SET last_seen=?, ip=? WHERE id=?`, now.Unix(), clientIP(r), hash)
	}
	return hash, true
}

func (a *AdminAuth) valid(r *http.Request) bool {
	_, ok := a.session(r)
	return ok
}

func (a *AdminAuth) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hash, ok := a.session(r)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionCtxKey{}, hash)))
	})
}

func currentSession(r *http.Request) string {
	hash, _ := r.Context().Value(sessionCtxKey{}).(string)
	return hash
}

/* ── 账户接口（挂在 /api/v1 下，已经过 Require） ───────────────────────── */

type accountSession struct {
	ID        string `json:"id"`
	Current   bool   `json:"current"`
	CreatedAt int64  `json:"created_at"`
	LastSeen  int64  `json:"last_seen"`
	ExpiresAt int64  `json:"expires_at"`
	IP        string `json:"ip"`
	UserAgent string `json:"user_agent"`
	Remember  bool   `json:"remember"`
}

type accountLogin struct {
	Ts        int64  `json:"ts"`
	IP        string `json:"ip"`
	UserAgent string `json:"user_agent"`
	Username  string `json:"username"`
	OK        bool   `json:"ok"`
	Reason    string `json:"reason"`
}

type accountInfo struct {
	Username          string           `json:"username"`
	PasswordSource    string           `json:"password_source"`
	PasswordChangedAt int64            `json:"password_changed_at"`
	StoreEnabled      bool             `json:"store_enabled"`
	Sessions          []accountSession `json:"sessions"`
	Logins            []accountLogin   `json:"logins"`
	Failed24h         int              `json:"failed_24h"`
}

func (a *AdminAuth) registerAccount(mux *http.ServeMux) {
	mux.HandleFunc("GET /account", a.accountGet)
	mux.HandleFunc("PUT /account/password", a.accountPassword)
	mux.HandleFunc("PUT /account/profile", a.accountProfile)
	mux.HandleFunc("DELETE /account/sessions/{id}", a.accountRevoke)
	mux.HandleFunc("POST /account/sessions/revoke-others", a.accountRevokeOthers)
}

func (a *AdminAuth) accountGet(w http.ResponseWriter, r *http.Request) {
	info := accountInfo{Username: a.Username(), PasswordSource: "env", StoreEnabled: a.db != nil, Sessions: []accountSession{}, Logins: []accountLogin{}}
	if a.setting("password_hash") != "" {
		info.PasswordSource = "custom"
	}
	info.PasswordChangedAt, _ = strconv.ParseInt(a.setting("password_changed_at"), 10, 64)
	if a.db != nil {
		current := currentSession(r)
		rows, err := a.db.Query(`SELECT id,pub,created_at,last_seen,expires_at,ip,user_agent,remember FROM admin_sessions WHERE expires_at >= ? ORDER BY last_seen DESC`, a.now().Unix())
		if err != nil {
			apiError(w, 500, err)
			return
		}
		for rows.Next() {
			var s accountSession
			var id string
			var remember int
			if err := rows.Scan(&id, &s.ID, &s.CreatedAt, &s.LastSeen, &s.ExpiresAt, &s.IP, &s.UserAgent, &remember); err == nil {
				s.Current, s.Remember = id == current, remember == 1
				info.Sessions = append(info.Sessions, s)
			}
		}
		rows.Close()
		logs, err := a.db.Query(`SELECT ts,ip,user_agent,username,ok,reason FROM admin_login_log ORDER BY id DESC LIMIT 40`)
		if err == nil {
			for logs.Next() {
				var l accountLogin
				var ok int
				if logs.Scan(&l.Ts, &l.IP, &l.UserAgent, &l.Username, &ok, &l.Reason) == nil {
					l.OK = ok == 1
					info.Logins = append(info.Logins, l)
				}
			}
			logs.Close()
		}
		_ = a.db.QueryRow(`SELECT count(*) FROM admin_login_log WHERE ok=0 AND ts >= ?`, a.now().Add(-24*time.Hour).Unix()).Scan(&info.Failed24h)
	}
	jsonOut(w, 200, info)
}

func validatePassword(pw string) error {
	n := utf8.RuneCountInString(pw)
	if n < minPasswordLen {
		return fmt.Errorf("新密码至少 %d 个字符", minPasswordLen)
	}
	if n > maxPasswordLen {
		return fmt.Errorf("新密码最多 %d 个字符", maxPasswordLen)
	}
	var classes int
	var lower, upper, digit, other bool
	for _, c := range pw {
		switch {
		case unicode.IsLower(c):
			lower = true
		case unicode.IsUpper(c):
			upper = true
		case unicode.IsDigit(c):
			digit = true
		default:
			other = true
		}
	}
	for _, b := range []bool{lower, upper, digit, other} {
		if b {
			classes++
		}
	}
	if classes < 2 {
		return errors.New("新密码至少要包含字母、数字、符号中的两类")
	}
	return nil
}

func (a *AdminAuth) accountPassword(w http.ResponseWriter, r *http.Request) {
	if a.db == nil {
		apiError(w, 400, errors.New("当前实例未启用账户存储，只能通过 ONESSH_ADMIN_PASSWORD 修改密码"))
		return
	}
	var in struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&in); err != nil {
		apiError(w, 400, err)
		return
	}
	ip := clientIP(r)
	if _, ok := a.checkRateLimit(ip, a.now()); !ok {
		apiError(w, 429, errors.New("失败次数过多，请稍后再试"))
		return
	}
	if !a.checkPassword(in.Current) {
		a.recordLoginFailure(ip, a.now())
		a.recordLogin(r, a.Username(), false, "bad_current_password")
		apiError(w, 403, errors.New("当前密码不正确"))
		return
	}
	if in.New == in.Current {
		apiError(w, 400, errors.New("新密码不能与当前密码相同"))
		return
	}
	if err := validatePassword(in.New); err != nil {
		apiError(w, 400, err)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.New), 12)
	if err != nil {
		apiError(w, 500, err)
		return
	}
	if err := a.setSetting("password_hash", string(hash)); err != nil {
		apiError(w, 500, err)
		return
	}
	_ = a.setSetting("password_changed_at", strconv.FormatInt(a.now().Unix(), 10))
	// 改密码 = 其它设备一律下线，只保留正在操作的这个会话
	res, _ := a.db.Exec(`DELETE FROM admin_sessions WHERE id <> ?`, currentSession(r))
	revoked, _ := res.RowsAffected()
	a.recordLogin(r, a.Username(), true, "password_changed")
	jsonOut(w, 200, map[string]any{"ok": true, "revoked_sessions": revoked})
}

func (a *AdminAuth) accountProfile(w http.ResponseWriter, r *http.Request) {
	if a.db == nil {
		apiError(w, 400, errors.New("当前实例未启用账户存储"))
		return
	}
	var in struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&in); err != nil {
		apiError(w, 400, err)
		return
	}
	name := strings.TrimSpace(in.Username)
	if n := utf8.RuneCountInString(name); n < 2 || n > 32 {
		apiError(w, 400, errors.New("用户名长度需在 2 到 32 个字符之间"))
		return
	}
	for _, c := range name {
		if !(unicode.IsLetter(c) || unicode.IsDigit(c) || strings.ContainsRune("_-.@", c)) {
			apiError(w, 400, errors.New("用户名只能包含字母、数字以及 _ - . @"))
			return
		}
	}
	if err := a.setSetting("username", name); err != nil {
		apiError(w, 500, err)
		return
	}
	jsonOut(w, 200, map[string]any{"ok": true, "username": name})
}

func (a *AdminAuth) accountRevoke(w http.ResponseWriter, r *http.Request) {
	if a.db == nil {
		apiError(w, 400, errors.New("当前实例未启用账户存储"))
		return
	}
	res, err := a.db.Exec(`DELETE FROM admin_sessions WHERE pub=?`, r.PathValue("id"))
	if err != nil {
		apiError(w, 500, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		apiError(w, 404, errors.New("会话不存在或已过期"))
		return
	}
	w.WriteHeader(204)
}

func (a *AdminAuth) accountRevokeOthers(w http.ResponseWriter, r *http.Request) {
	if a.db == nil {
		apiError(w, 400, errors.New("当前实例未启用账户存储"))
		return
	}
	res, err := a.db.Exec(`DELETE FROM admin_sessions WHERE id <> ?`, currentSession(r))
	if err != nil {
		apiError(w, 500, err)
		return
	}
	n, _ := res.RowsAffected()
	jsonOut(w, 200, map[string]any{"ok": true, "revoked_sessions": n})
}
