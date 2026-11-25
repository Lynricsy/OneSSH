package webapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	loginMaxFailures = 10
	loginWindow      = 15 * time.Minute
	loginLockout     = 15 * time.Minute
	loginMaxTracked  = 4096
)

type loginAttempt struct {
	failures    int
	windowStart time.Time
	lockedUntil time.Time
	lastSeen    time.Time
}

type AdminAuth struct {
	password string
	key      []byte
	now      func() time.Time
	mu       sync.Mutex
	attempts map[string]*loginAttempt
}

func NewAdminAuth(password string, masterKey []byte) *AdminAuth {
	// 把管理员密码摘要纳入会话密钥派生：密码轮换重启后，此前签发的所有管理会话
	// cookie 立即失效，无需额外引入服务端会话表。
	pwHash := sha256.Sum256([]byte(password))
	mac := hmac.New(sha256.New, masterKey)
	mac.Write([]byte("onessh-admin-session:"))
	mac.Write(pwHash[:])
	return &AdminAuth{
		password: password,
		key:      mac.Sum(nil),
		now:      time.Now,
		attempts: make(map[string]*loginAttempt),
	}
}

func clientIP(r *http.Request) string {
	// 不信任未经配置校验的 X-Forwarded-For，避免攻击者伪造请求头绕过登录限流。
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func (a *AdminAuth) checkRateLimit(ip string, now time.Time) (time.Duration, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	entry, ok := a.attempts[ip]
	if !ok {
		return 0, true
	}
	if now.Before(entry.lockedUntil) {
		return entry.lockedUntil.Sub(now), false
	}
	if !entry.lockedUntil.IsZero() || now.Sub(entry.windowStart) >= loginWindow {
		delete(a.attempts, ip)
	}
	return 0, true
}

func (a *AdminAuth) recordLoginFailure(ip string, now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	entry, ok := a.attempts[ip]
	if !ok {
		if len(a.attempts) >= loginMaxTracked {
			a.evictAttemptsLocked(now)
		}
		entry = &loginAttempt{windowStart: now}
		a.attempts[ip] = entry
	} else if now.Sub(entry.windowStart) >= loginWindow {
		entry.failures = 0
		entry.windowStart = now
		entry.lockedUntil = time.Time{}
	}
	entry.failures++
	entry.lastSeen = now
	if entry.failures >= loginMaxFailures {
		entry.lockedUntil = now.Add(loginLockout)
	}
}

func (a *AdminAuth) clearLoginFailures(ip string) {
	a.mu.Lock()
	delete(a.attempts, ip)
	a.mu.Unlock()
}

func (a *AdminAuth) evictAttemptsLocked(now time.Time) {
	for ip, entry := range a.attempts {
		if now.Before(entry.lockedUntil) {
			continue
		}
		if !entry.lockedUntil.IsZero() || now.Sub(entry.windowStart) >= loginWindow {
			delete(a.attempts, ip)
		}
	}
	if len(a.attempts) < loginMaxTracked {
		return
	}
	var oldestIP string
	var oldestTime time.Time
	for ip, entry := range a.attempts {
		if oldestIP == "" || entry.lastSeen.Before(oldestTime) {
			oldestIP = ip
			oldestTime = entry.lastSeen
		}
	}
	if oldestIP != "" {
		delete(a.attempts, oldestIP)
	}
}

func (a *AdminAuth) Login(w http.ResponseWriter, r *http.Request) {
	now := a.now()
	ip := clientIP(r)
	if retryAfter, ok := a.checkRateLimit(ip, now); !ok {
		seconds := int(retryAfter.Seconds())
		if seconds < 1 {
			seconds = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
		http.Error(w, "登录尝试过于频繁，请稍后再试", http.StatusTooManyRequests)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var in struct {
		Password string `json:"password"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || subtle.ConstantTimeCompare([]byte(in.Password), []byte(a.password)) != 1 {
		a.recordLoginFailure(ip, now)
		http.Error(w, "密码错误", http.StatusUnauthorized)
		return
	}
	a.clearLoginFailures(ip)
	exp := now.Add(24 * time.Hour).Unix()
	msg := strconv.FormatInt(exp, 10)
	mac := hmac.New(sha256.New, a.key)
	mac.Write([]byte(msg))
	value := msg + "." + hex.EncodeToString(mac.Sum(nil))
	http.SetCookie(w, &http.Cookie{Name: "onessh_session", Value: value, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: requestIsHTTPS(r), MaxAge: 86400})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}
func requestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	forwardedProto := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0])
	return strings.EqualFold(forwardedProto, "https")
}
func (a *AdminAuth) valid(r *http.Request) bool {
	c, err := r.Cookie("onessh_session")
	if err != nil {
		return false
	}
	parts := strings.Split(c.Value, ".")
	if len(parts) != 2 {
		return false
	}
	exp, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || a.now().Unix() > exp {
		return false
	}
	got, err := hex.DecodeString(parts[1])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, a.key)
	mac.Write([]byte(parts[0]))
	return hmac.Equal(got, mac.Sum(nil))
}
func (a *AdminAuth) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.valid(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
